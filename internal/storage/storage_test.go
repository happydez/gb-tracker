package storage

import (
	"path/filepath"
	"testing"

	"github.com/google/go-cmp/cmp"
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
		ModID:      699260,
		CategoryID: 5568,
		Name:       "bhop_ln_portal",
		ProfileURL: "https://gamebanana.com/mods/699260",
		DateAdded:  1785528621,
		MDate:      1785528621,
		EventID:    "01JBQX7K9M2NPVWXYZ3ABCDEFG",
		RecordJSON: []byte(`{"_idRow":699260}`),
		DetailJSON: []byte(`{"name":"bhop_ln_portal"}`),
	}
}

func TestSeenOnEmptyDatabase(t *testing.T) {
	s := open(t)

	mdate, ok, err := s.Seen(t.Context(), 699260)
	if err != nil {
		t.Fatalf("seen: %v", err)
	}

	if ok {
		t.Errorf("ok = true, want false for a mod never announced")
	}

	if mdate != 0 {
		t.Errorf("mdate = %d, want 0", mdate)
	}
}

func TestSaveThenSeen(t *testing.T) {
	s := open(t)
	m := sampleMod()

	if err := s.Save(t.Context(), m); err != nil {
		t.Fatalf("save: %v", err)
	}

	mdate, ok, err := s.Seen(t.Context(), m.ModID)
	if err != nil {
		t.Fatalf("seen: %v", err)
	}

	if !ok {
		t.Fatal("ok = false, want true after save")
	}

	if mdate != m.MDate {
		t.Errorf("mdate = %d, want %d", mdate, m.MDate)
	}
}

// TestSaveUpdateKeepsCreatedAt is the reason Save uses ON CONFLICT DO UPDATE
// instead of INSERT OR REPLACE: the latter deletes the row, taking created_at
// with it.
func TestSaveUpdateKeepsCreatedAt(t *testing.T) {
	s := open(t)
	m := sampleMod()

	if err := s.Save(t.Context(), m); err != nil {
		t.Fatalf("save: %v", err)
	}

	var createdAt int64
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT created_at FROM mods WHERE mod_id = ?`, m.ModID,
	).Scan(&createdAt); err != nil {
		t.Fatalf("read created_at: %v", err)
	}

	if createdAt == 0 {
		t.Fatal("created_at is 0 after the first save")
	}

	if _, err := s.db.ExecContext(t.Context(),
		`UPDATE mods SET created_at = ? WHERE mod_id = ?`, createdAt-86400, m.ModID,
	); err != nil {
		t.Fatalf("backdate created_at: %v", err)
	}

	updated := m
	updated.MDate = m.MDate + 1000
	updated.EventID = "01JBQX7K9M2NPVWXYZ3ABCDEFH"
	updated.Name = "bhop_ln_portal_v2"

	if err := s.Save(t.Context(), updated); err != nil {
		t.Fatalf("save update: %v", err)
	}

	var (
		gotCreatedAt int64
		gotMDate     int64
		gotEventID   string
		gotName      string
	)
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT created_at, mdate, event_id, name FROM mods WHERE mod_id = ?`, m.ModID,
	).Scan(&gotCreatedAt, &gotMDate, &gotEventID, &gotName); err != nil {
		t.Fatalf("read row: %v", err)
	}

	if gotCreatedAt != createdAt-86400 {
		t.Errorf("created_at = %d, want the original %d", gotCreatedAt, createdAt-86400)
	}

	if gotMDate != updated.MDate {
		t.Errorf("mdate = %d, want %d", gotMDate, updated.MDate)
	}

	if gotEventID != updated.EventID {
		t.Errorf("event_id = %q, want %q", gotEventID, updated.EventID)
	}

	if gotName != updated.Name {
		t.Errorf("name = %q, want %q", gotName, updated.Name)
	}
}

func TestSaveStoresRawJSON(t *testing.T) {
	s := open(t)
	m := sampleMod()

	if err := s.Save(t.Context(), m); err != nil {
		t.Fatalf("save: %v", err)
	}

	var record, detail string
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT record_json, detail_json FROM mods WHERE mod_id = ?`, m.ModID,
	).Scan(&record, &detail); err != nil {
		t.Fatalf("read json: %v", err)
	}

	if diff := cmp.Diff(string(m.RecordJSON), record); diff != "" {
		t.Errorf("record_json mismatch (-want +got):\n%s", diff)
	}

	if diff := cmp.Diff(string(m.DetailJSON), detail); diff != "" {
		t.Errorf("detail_json mismatch (-want +got):\n%s", diff)
	}
}

func TestSaveWithoutDetail(t *testing.T) {
	s := open(t)

	m := sampleMod()
	m.DetailJSON = nil

	if err := s.Save(t.Context(), m); err != nil {
		t.Fatalf("save: %v", err)
	}

	var detail string
	if err := s.db.QueryRowContext(t.Context(),
		`SELECT detail_json FROM mods WHERE mod_id = ?`, m.ModID,
	).Scan(&detail); err != nil {
		t.Fatalf("read detail_json: %v", err)
	}

	if detail != "" {
		t.Errorf("detail_json = %q, want empty", detail)
	}
}

func TestCount(t *testing.T) {
	s := open(t)

	n, err := s.Count(t.Context())
	if err != nil {
		t.Fatalf("count: %v", err)
	}
	if n != 0 {
		t.Fatalf("count = %d, want 0 on a fresh database", n)
	}

	first := sampleMod()
	second := sampleMod()
	second.ModID = 699123

	for _, m := range []Mod{first, second, first} {
		if err = s.Save(t.Context(), m); err != nil {
			t.Fatalf("save: %v", err)
		}
	}

	n, err = s.Count(t.Context())
	if err != nil {
		t.Fatalf("count: %v", err)
	}

	if n != 2 {
		t.Errorf("count = %d, want 2", n)
	}
}

func TestOpenIsIdempotent(t *testing.T) {
	path := filepath.Join(t.TempDir(), "test.db")

	first, err := Open(t.Context(), path)
	if err != nil {
		t.Fatalf("open: %v", err)
	}

	if err = first.Save(t.Context(), sampleMod()); err != nil {
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

	n, err := second.Count(t.Context())
	if err != nil {
		t.Fatalf("count: %v", err)
	}

	if n != 1 {
		t.Errorf("count = %d after reopening, want 1", n)
	}

	var version int
	if err := second.db.QueryRowContext(t.Context(),
		`SELECT version FROM schema_version`,
	).Scan(&version); err != nil {
		t.Fatalf("read schema version: %v", err)
	}

	if version != 1 {
		t.Errorf("schema version = %d, want 1", version)
	}
}

func TestOpenRejectsUnwritablePath(t *testing.T) {
	ctx := t.Context()
	if _, err := Open(ctx, t.TempDir()); err == nil {
		t.Fatal("open: expected an error for a path that is a directory")
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
			t.Errorf("migrations out of order or duplicated: %q then %q", files[i-1].name, files[i].name)
		}
	}
}
