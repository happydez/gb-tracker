// Package storage records what this service has already processed.
package storage

import (
	"context"
	"database/sql"
	"embed"
	"errors"
	"fmt"
	"io/fs"
	"net/url"
	"sort"
	"strings"
	"time"

	_ "modernc.org/sqlite"
)

//go:embed migrations/*.sql
var migrations embed.FS

const (
	StatusDone    = "done"
	StatusSkipped = "skipped"
	StatusFailed  = "failed"
)

// Mod is the outcome of processing one mod.
type Mod struct {
	ModID     int
	Name      string
	MDate     int64
	Status    string
	MapCount  int
	BSPSize   int64
	BZ2Size   int64
	Uploaded  bool
	RCONDone  bool
	LastError string
}

// Map is one uploaded map.
type Map struct {
	Name       string
	ModID      int
	RemotePath string
	BZ2Size    int64
}

type Storage struct {
	db *sql.DB
}

func Open(ctx context.Context, path string) (*Storage, error) {
	db, err := sql.Open("sqlite", dsn(path))
	if err != nil {
		return nil, fmt.Errorf("open database: %w", err)
	}

	db.SetMaxOpenConns(1)

	if err := db.PingContext(ctx); err != nil {
		_ = db.Close()

		return nil, fmt.Errorf("connect to database: %w", err)
	}

	s := &Storage{db: db}
	if err := s.migrate(ctx); err != nil {
		_ = db.Close()

		return nil, err
	}

	return s, nil
}

// WAL keeps readers from blocking the writer; busy_timeout waits instead of
// failing outright when they collide anyway.
func dsn(path string) string {
	q := url.Values{}
	q.Add("_pragma", "journal_mode(WAL)")
	q.Add("_pragma", "busy_timeout(5000)")
	q.Add("_pragma", "foreign_keys(on)")

	return "file:" + path + "?" + q.Encode()
}

func (s *Storage) Close() error {
	return s.db.Close()
}

// Processed reports the stored mdate of a mod and whether it is known at all.
// A newer mdate upstream means the mod was updated and has to be redone.
func (s *Storage) Processed(ctx context.Context, modID int) (int64, bool, error) {
	var mdate int64

	err := s.db.QueryRowContext(ctx,
		`SELECT mdate FROM mods WHERE mod_id = ?`, modID,
	).Scan(&mdate)

	if errors.Is(err, sql.ErrNoRows) {
		return 0, false, nil
	}
	if err != nil {
		return 0, false, fmt.Errorf("look up mod %d: %w", modID, err)
	}

	return mdate, true, nil
}

// FileDone reports whether a GameBanana file id has already been handled.
func (s *Storage) FileDone(ctx context.Context, fileID string) (bool, error) {
	var one int

	err := s.db.QueryRowContext(ctx,
		`SELECT 1 FROM files WHERE file_id = ?`, fileID,
	).Scan(&one)

	if errors.Is(err, sql.ErrNoRows) {
		return false, nil
	}
	if err != nil {
		return false, fmt.Errorf("look up file %s: %w", fileID, err)
	}

	return true, nil
}

func (s *Storage) MarkFile(ctx context.Context, fileID string, modID int, md5 string) error {
	_, err := s.db.ExecContext(ctx, `
		INSERT INTO files (file_id, mod_id, md5, processed_at)
		VALUES (?, ?, ?, ?)
		ON CONFLICT(file_id) DO UPDATE SET
			mod_id = excluded.mod_id,
			md5 = excluded.md5,
			processed_at = excluded.processed_at
	`, fileID, modID, md5, time.Now().Unix())
	if err != nil {
		return fmt.Errorf("mark file %s: %w", fileID, err)
	}

	return nil
}

// SaveMod records the outcome. created_at is left alone on update so the first
// sighting survives.
func (s *Storage) SaveMod(ctx context.Context, m Mod) error {
	now := time.Now().Unix()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO mods (
			mod_id, name, mdate, status, map_count, bsp_size, bz2_size,
			uploaded, rcon_done, last_error, created_at, updated_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(mod_id) DO UPDATE SET
			name = excluded.name,
			mdate = excluded.mdate,
			status = excluded.status,
			map_count = excluded.map_count,
			bsp_size = excluded.bsp_size,
			bz2_size = excluded.bz2_size,
			uploaded = excluded.uploaded,
			rcon_done = excluded.rcon_done,
			last_error = excluded.last_error,
			updated_at = excluded.updated_at
	`,
		m.ModID, m.Name, m.MDate, m.Status, m.MapCount, m.BSPSize, m.BZ2Size,
		boolToInt(m.Uploaded), boolToInt(m.RCONDone), m.LastError, now, now,
	)
	if err != nil {
		return fmt.Errorf("save mod %d: %w", m.ModID, err)
	}

	return nil
}

func (s *Storage) SaveMaps(ctx context.Context, maps []Map) error {
	if len(maps) == 0 {
		return nil
	}

	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	now := time.Now().Unix()

	for _, m := range maps {
		if _, err := tx.ExecContext(ctx, `
			INSERT INTO maps (name, mod_id, remote_path, bz2_size, uploaded_at)
			VALUES (?, ?, ?, ?, ?)
			ON CONFLICT(name, mod_id) DO UPDATE SET
				remote_path = excluded.remote_path,
				bz2_size = excluded.bz2_size,
				uploaded_at = excluded.uploaded_at
		`, m.Name, m.ModID, m.RemotePath, m.BZ2Size, now); err != nil {
			return fmt.Errorf("save map %s: %w", m.Name, err)
		}
	}

	return tx.Commit()
}

func (s *Storage) CountMods(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM mods`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count mods: %w", err)
	}

	return n, nil
}

func boolToInt(b bool) int {
	if b {
		return 1
	}

	return 0
}

func (s *Storage) migrate(ctx context.Context) error {
	if _, err := s.db.ExecContext(ctx,
		`CREATE TABLE IF NOT EXISTS schema_version (version INTEGER NOT NULL) STRICT`,
	); err != nil {
		return fmt.Errorf("create schema_version: %w", err)
	}

	var current int
	err := s.db.QueryRowContext(ctx, `SELECT version FROM schema_version`).Scan(&current)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return fmt.Errorf("read schema version: %w", err)
	}

	files, err := migrationFiles()
	if err != nil {
		return err
	}

	for _, f := range files {
		if f.version <= current {
			continue
		}

		body, err := migrations.ReadFile("migrations/" + f.name)
		if err != nil {
			return fmt.Errorf("read migration %s: %w", f.name, err)
		}

		if err := s.applyMigration(ctx, string(body), f.version); err != nil {
			return fmt.Errorf("apply migration %s: %w", f.name, err)
		}
	}

	return nil
}

// One transaction per migration
func (s *Storage) applyMigration(ctx context.Context, body string, version int) error {
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer func() {
		_ = tx.Rollback()
	}()

	if _, err := tx.ExecContext(ctx, body); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `DELETE FROM schema_version`); err != nil {
		return err
	}

	if _, err := tx.ExecContext(ctx, `INSERT INTO schema_version (version) VALUES (?)`, version); err != nil {
		return err
	}

	return tx.Commit()
}

type migration struct {
	version int
	name    string
}

func migrationFiles() ([]migration, error) {
	entries, err := fs.ReadDir(migrations, "migrations")
	if err != nil {
		return nil, fmt.Errorf("read migrations: %w", err)
	}

	out := make([]migration, 0, len(entries))
	for _, e := range entries {
		name := e.Name()

		prefix, _, ok := strings.Cut(name, "_")
		if !ok {
			return nil, fmt.Errorf("migration %q: name must start with a version", name)
		}

		var version int
		if _, err := fmt.Sscanf(prefix, "%d", &version); err != nil || version <= 0 {
			return nil, fmt.Errorf("migration %q: %q is not a positive version", name, prefix)
		}

		out = append(out, migration{version: version, name: name})
	}

	sort.Slice(out, func(i, j int) bool { return out[i].version < out[j].version })

	return out, nil
}
