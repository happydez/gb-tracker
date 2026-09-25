// Package storage keeps the set of mods the tracker has already announced.
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

// Mod is one announced mod, as written to the database.
type Mod struct {
	ModID      int
	CategoryID int
	Name       string
	ProfileURL string
	DateAdded  int64
	MDate      int64
	EventID    string
	RecordJSON []byte
	DetailJSON []byte
}

type Storage struct {
	db *sql.DB
}

// Open connects to the SQLite file at path, creating it if needed, and applies
// any pending migrations. Pass ":memory:" for a throwaway database.
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

// Seen returns the stored mdate of a mod, and whether it has been announced at
// all. A stored mdate lower than the one in a fresh listing means the mod was
// updated and is due to be announced again.
func (s *Storage) Seen(ctx context.Context, modID int) (int64, bool, error) {
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

// Save records a mod as announced. It is called after the event is published,
// so a row appearing here always means the announcement went out.
func (s *Storage) Save(ctx context.Context, m Mod) error {
	now := time.Now().Unix()

	_, err := s.db.ExecContext(ctx, `
		INSERT INTO mods (
			mod_id, category_id, name, profile_url, date_added, mdate,
			event_id, record_json, detail_json, created_at, published_at
		)
		VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
		ON CONFLICT(mod_id) DO UPDATE SET
			category_id  = excluded.category_id,
			name         = excluded.name,
			profile_url  = excluded.profile_url,
			date_added   = excluded.date_added,
			mdate        = excluded.mdate,
			event_id     = excluded.event_id,
			record_json  = excluded.record_json,
			detail_json  = excluded.detail_json,
			published_at = excluded.published_at
	`,
		m.ModID, m.CategoryID, m.Name, m.ProfileURL, m.DateAdded, m.MDate,
		m.EventID, string(m.RecordJSON), string(m.DetailJSON), now, now,
	)
	if err != nil {
		return fmt.Errorf("save mod %d: %w", m.ModID, err)
	}

	return nil
}

// Count returns how many mods have been announced, for logging on startup.
func (s *Storage) Count(ctx context.Context) (int, error) {
	var n int
	if err := s.db.QueryRowContext(ctx, `SELECT count(*) FROM mods`).Scan(&n); err != nil {
		return 0, fmt.Errorf("count mods: %w", err)
	}

	return n, nil
}

// migrate applies every embedded migration whose number is above the version
// recorded in the database, in order.
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

// migrationFiles lists the embedded migrations in version order. Names start
// with a zero-padded number, as in "0001_init.sql".
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
			return nil, fmt.Errorf("migration %q: name must start with a version, as in 0001_init.sql", name)
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
