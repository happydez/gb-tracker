package match

import "testing"

func routes() []Route {
	// Order matters: kz_bhop_ has to win over kz_.
	return []Route{
		{Prefix: "kz_bhop_", Dir: "kz_bhop"},
		{Prefix: "kz_", Dir: "kz"},
		{Prefix: "bhop_", Dir: "bhop"},
	}
}

func TestNew(t *testing.T) {
	tests := []struct {
		name       string
		routes     []Route
		fallback   string
		byCategory map[int]string
		wantErr    bool
	}{
		{name: "routes only", routes: routes()},
		{name: "fallback only", fallback: "other"},
		{name: "neither", wantErr: true},
		{name: "empty prefix", routes: []Route{{Dir: "x"}}, wantErr: true},
		{
			name:    "duplicate prefix",
			routes:  []Route{{Prefix: "kz_", Dir: "a"}, {Prefix: "KZ_", Dir: "b"}},
			wantErr: true,
		},
		{name: "empty dir keeps maps in the base directory", routes: []Route{{Prefix: "bhop_"}}},
		{
			name:       "a category may clear the fallback while routes remain",
			routes:     routes(),
			fallback:   "other",
			byCategory: map[int]string{5535: ""},
		},
		{
			name:       "a category clearing the only fallback",
			fallback:   "other",
			byCategory: map[int]string{5535: ""},
			wantErr:    true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			_, err := New(tt.routes, tt.fallback, tt.byCategory)
			if tt.wantErr && err == nil {
				t.Fatal("New: expected an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("New: %v", err)
			}
		})
	}
}

func TestMapRoutesToTheFirstMatch(t *testing.T) {
	m, err := New(routes(), "", nil)
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name    string
		mapName string
		wantDir string
		wantOK  bool
	}{
		{name: "specific prefix wins", mapName: "kz_bhop_arena", wantDir: "kz_bhop", wantOK: true},
		{name: "general prefix", mapName: "kz_arena", wantDir: "kz", wantOK: true},
		{name: "another route", mapName: "bhop_arena", wantDir: "bhop", wantOK: true},
		{name: "case insensitive", mapName: "BHOP_Arena", wantDir: "bhop", wantOK: true},
		{name: "unmatched is dropped without a fallback", mapName: "surf_arena"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, ok := m.Map(0, tt.mapName)
			if ok != tt.wantOK || dir != tt.wantDir {
				t.Errorf("Map(%q) = %q, %v; want %q, %v", tt.mapName, dir, ok, tt.wantDir, tt.wantOK)
			}
		})
	}
}

func TestFallbackTakesTheRest(t *testing.T) {
	m, err := New(routes(), "other", nil)
	if err != nil {
		t.Fatal(err)
	}

	if dir, ok := m.Map(0, "surf_arena"); !ok || dir != "other" {
		t.Errorf("Map(surf_arena) = %q, %v; want other, true", dir, ok)
	}

	if dir, ok := m.Map(0, "bhop_arena"); !ok || dir != "bhop" {
		t.Errorf("Map(bhop_arena) = %q, %v; want bhop, true", dir, ok)
	}
}

func TestFallbackDot(t *testing.T) {
	m, err := New(nil, ".", nil)
	if err != nil {
		t.Fatal(err)
	}

	dir, ok := m.Map(0, "surf_arena")
	if !ok || dir != "" {
		t.Errorf("Map = %q, %v; want \"\", true", dir, ok)
	}

	if got, has := m.Fallback(); !has || got != "" {
		t.Errorf("Fallback() = %q, %v; want \"\", true", got, has)
	}
}

func TestDirIsContained(t *testing.T) {
	m, err := New([]Route{{Prefix: "bhop_", Dir: "../../etc"}}, "", nil)
	if err != nil {
		t.Fatal(err)
	}

	if dir, _ := m.Map(0, "bhop_arena"); dir != "etc" {
		t.Errorf("Map = %q, want the escape stripped to etc", dir)
	}
}

func TestMod(t *testing.T) {
	withRoutes, err := New(routes(), "", nil)
	if err != nil {
		t.Fatal(err)
	}

	if !withRoutes.Mod(0, "New bhop_arena release") {
		t.Error("Mod: expected a title containing a prefix to match")
	}

	if withRoutes.Mod(0, "surf_arena") {
		t.Error("Mod: expected an unrelated title not to match")
	}

	withFallback, err := New(routes(), "other", nil)
	if err != nil {
		t.Fatal(err)
	}

	if !withFallback.Mod(0, "surf_arena") {
		t.Error("Mod: expected everything to match while a fallback is set")
	}
}

func TestCategoryOverridesTheFallback(t *testing.T) {
	const (
		maps = 5535
		bhop = 5568
	)

	m, err := New(routes(), "", map[int]string{bhop: "other"})
	if err != nil {
		t.Fatal(err)
	}

	tests := []struct {
		name     string
		category int
		mapName  string
		wantDir  string
		wantOK   bool
	}{
		{name: "unmatched is dropped in the strict category", category: maps, mapName: "cs_office"},
		{name: "unmatched is kept in the overridden one", category: bhop, mapName: "cs_office", wantDir: "other", wantOK: true},
		{name: "a route still wins there", category: bhop, mapName: "bhop_arena", wantDir: "bhop", wantOK: true},
		{name: "and in the strict one", category: maps, mapName: "bhop_arena", wantDir: "bhop", wantOK: true},
		{name: "a category with no rule uses the default", category: 9999, mapName: "cs_office"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			dir, ok := m.Map(tt.category, tt.mapName)
			if ok != tt.wantOK || dir != tt.wantDir {
				t.Errorf("Map(%d, %q) = %q, %v; want %q, %v", tt.category, tt.mapName, dir, ok, tt.wantDir, tt.wantOK)
			}
		})
	}

	if m.Mod(maps, "cs_office") {
		t.Error("Mod: the strict category should not accept an unrelated title")
	}

	if !m.Mod(bhop, "cs_office") {
		t.Error("Mod: the overridden category should accept everything")
	}
}

func TestCategoryFallbackDot(t *testing.T) {
	m, err := New(routes(), "", map[int]string{5568: "."})
	if err != nil {
		t.Fatal(err)
	}

	dir, ok := m.Map(5568, "cs_office")
	if !ok || dir != "" {
		t.Errorf("Map = %q, %v; want \"\", true", dir, ok)
	}
}

func TestCategoryOverrideIsLocal(t *testing.T) {
	m, err := New(routes(), "other", map[int]string{5535: ""})
	if err != nil {
		t.Fatal(err)
	}

	if _, ok := m.Map(5535, "cs_office"); ok {
		t.Error("the overridden category still keeps unmatched maps")
	}

	if dir, ok := m.Map(5568, "cs_office"); !ok || dir != "other" {
		t.Errorf("Map(5568) = %q, %v; want other, true", dir, ok)
	}
}
