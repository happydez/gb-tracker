package storage

import (
	"path/filepath"
	"testing"
)

func open(t *testing.T) *Storage {
	t.Helper()

	s, err := Open(t.Context(), filepath.Join(t.TempDir(), "test.db"))
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	t.Cleanup(func() {
		if err := s.Close(); err != nil {
			t.Errorf("close: %v", err)
		}
	})

	return s
}

func sampleMod() Mod {
	return Mod{
		ModID:    718166,
		Name:     "bhop_scopex",
		MDate:    1789673474,
		Status:   StatusDone,
		MapCount: 1,
		BSPSize:  54 << 20,
		BZ2Size:  12 << 20,
		Uploaded: true,
		RCONDone: true,
	}
}

func TestProcessedOnEmptyDatabase(t *testing.T) {
	s := open(t)

	mdate, ok, err := s.Processed(t.Context(), 1)
	if err != nil {
		t.Fatalf("processed: %v", err)
	}

	if ok || mdate != 0 {
		t.Errorf("got (%d, %v), want (0, false)", mdate, ok)
	}
}

func TestSaveModThenProcessed(t *testing.T) {
	s := open(t)
	m := sampleMod()

	if err := s.SaveMod(t.Context(), m); err != nil {
		t.Fatalf("save mod: %v", err)
	}

	mdate, ok, err := s.Processed(t.Context(), m.ModID)
	if err != nil {
		t.Fatalf("processed: %v", err)
	}

	if !ok {
		t.Fatal("ok = false after save")
	}

	if mdate != m.MDate {
		t.Errorf("mdate = %d, want %d", mdate, m.MDate)
	}
}

func TestSaveModUpdateKeepsCreatedAt(t *testing.T) {
	s := open(t)
	m := sampleMod()

	if err := s.SaveMod(t.Context(), m); err != nil {
		t.Fatalf("save mod: %v", err)
	}

	var createdAt int64
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT created_at FROM mods WHERE mod_id = ?`, m.ModID,
	).Scan(&createdAt); err != nil {
		t.Fatalf("read created_at: %v", err)
	}

	if _, err := s.db.ExecContext(t.Context(),
		`UPDATE mods SET created_at = ? WHERE mod_id = ?`, createdAt-86400, m.ModID,
	); err != nil {
		t.Fatalf("backdate: %v", err)
	}

	updated := m
	updated.MDate = m.MDate + 1000
	updated.MapCount = 3

	if err := s.SaveMod(t.Context(), updated); err != nil {
		t.Fatalf("save update: %v", err)
	}

	var (
		gotCreated int64
		gotMDate   int64
		gotCount   int
	)
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT created_at, mdate, map_count FROM mods WHERE mod_id = ?`, m.ModID,
	).Scan(&gotCreated, &gotMDate, &gotCount); err != nil {
		t.Fatalf("read row: %v", err)
	}

	if gotCreated != createdAt-86400 {
		t.Errorf("created_at = %d, want the original %d", gotCreated, createdAt-86400)
	}

	if gotMDate != updated.MDate || gotCount != 3 {
		t.Errorf("mdate = %d, map_count = %d; want %d and 3", gotMDate, gotCount, updated.MDate)
	}
}

func TestSaveModStoresFlagsAndSizes(t *testing.T) {
	s := open(t)

	m := sampleMod()
	m.Uploaded = false
	m.RCONDone = false
	m.Status = StatusFailed
	m.LastError = "ftp refused the connection"

	if err := s.SaveMod(t.Context(), m); err != nil {
		t.Fatalf("save mod: %v", err)
	}

	var (
		status   string
		uploaded int
		rcon     int
		bsp      int64
		bz2      int64
		lastErr  string
	)
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT status, uploaded, rcon_done, bsp_size, bz2_size, last_error FROM mods WHERE mod_id = ?`,
		m.ModID,
	).Scan(&status, &uploaded, &rcon, &bsp, &bz2, &lastErr); err != nil {
		t.Fatalf("read row: %v", err)
	}

	if status != StatusFailed || uploaded != 0 || rcon != 0 {
		t.Errorf("status = %q, uploaded = %d, rcon = %d", status, uploaded, rcon)
	}

	if bsp != m.BSPSize || bz2 != m.BZ2Size {
		t.Errorf("sizes = %d/%d, want %d/%d", bsp, bz2, m.BSPSize, m.BZ2Size)
	}

	if lastErr != m.LastError {
		t.Errorf("last_error = %q", lastErr)
	}
}

func TestFileDedup(t *testing.T) {
	s := open(t)

	done, err := s.FileDone(t.Context(), "1160715")
	if err != nil {
		t.Fatalf("file done: %v", err)
	}
	if done {
		t.Fatal("file reported as done on an empty database")
	}

	if err = s.MarkFile(t.Context(), "1160715", 718166, "f07cbbdf"); err != nil {
		t.Fatalf("mark file: %v", err)
	}

	done, err = s.FileDone(t.Context(), "1160715")
	if err != nil {
		t.Fatalf("file done: %v", err)
	}
	if !done {
		t.Error("file not reported as done after marking")
	}

	if err = s.MarkFile(t.Context(), "1160715", 718166, "f07cbbdf"); err != nil {
		t.Errorf("second mark: %v", err)
	}
}

func TestSaveMaps(t *testing.T) {
	s := open(t)

	maps := []Map{
		{Name: "bhop_scopex", ModID: 718166, RemotePath: "maps/bhop_scopex.bsp.bz2", BZ2Size: 100},
		{Name: "bhop_scopex_v2", ModID: 718166, RemotePath: "maps/bhop_scopex_v2.bsp.bz2", BZ2Size: 200},
	}

	if err := s.SaveMaps(t.Context(), maps); err != nil {
		t.Fatalf("save maps: %v", err)
	}

	if err := s.SaveMaps(t.Context(), maps); err != nil {
		t.Fatalf("save maps again: %v", err)
	}

	var n int
	if err := s.db.QueryRowContext(t.Context(), `SELECT count(*) FROM maps`).Scan(&n); err != nil {
		t.Fatalf("count maps: %v", err)
	}

	if n != 2 {
		t.Errorf("maps = %d, want 2", n)
	}
}

func TestSaveMapsEmpty(t *testing.T) {
	s := open(t)

	if err := s.SaveMaps(t.Context(), nil); err != nil {
		t.Errorf("save maps: %v, want a no-op", err)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	first, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if err = first.SaveMod(t.Context(), sampleMod()); err != nil {
		t.Fatalf("save: %v", err)
	}

	if err = first.Close(); err != nil {
		t.Fatalf("close: %v", err)
	}

	second, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("reopen: %v", err)
	}
	defer func() {
		_ = second.Close()
	}()

	n, err := second.CountMods(t.Context())
	if err != nil {
		t.Fatalf("count: %v", err)
	}

	if n != 1 {
		t.Errorf("mods = %d after reopening, want 1", n)
	}
}

func TestMigrationFilesAreOrdered(t *testing.T) {
	files, err := migrationFiles()
	if err != nil {
		t.Fatalf("migration files: %v", err)
	}

	if len(files) == 0 {
		t.Fatal("no migrations embedded")
	}

	for i := 1; i < len(files); i++ {
		if files[i-1].version >= files[i].version {
			t.Errorf("migrations out of order: %q then %q", files[i-1].name, files[i].name)
		}
	}
}
