package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"
)

func writeConfig(t *testing.T, body string) string {
	t.Helper()

	path := filepath.Join(t.TempDir(), "config.yaml")
	if err := os.WriteFile(path, []byte(body), 0o600); err != nil {
		t.Fatalf("write config: %v", err)
	}

	return path
}

const minimalConfig = `
tracker:
  categories:
    - id: 5568
      label: "CS:S Bunny Hop"
`

func TestLoadAppliesDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimalConfig))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got, want := cfg.Log.Level, "info"; got != want {
		t.Errorf("log.level = %q, want %q", got, want)
	}

	if got, want := cfg.Tracker.PollInterval.Unwrap(), 5*time.Minute; got != want {
		t.Errorf("poll_interval = %s, want %s", got, want)
	}

	if got, want := cfg.GameBanana.ModIndexURL, "https://gamebanana.com/apiv11/Mod/Index"; got != want {
		t.Errorf("mod_index_url = %q, want %q", got, want)
	}

	c := cfg.Tracker.Categories[0]
	if c.PerPage != 15 || c.MaxPages != 5 || c.Sort != "Generic_LatestModified" {
		t.Errorf("category defaults not applied: %+v", c)
	}
}

func TestLoadOverridesDefaults(t *testing.T) {
	cfg, err := Load(writeConfig(t, `
tracker:
  poll_interval: 30s
  fetch_details: false
  defaults:
    per_page: 20
    max_pages: 3
    sort: Generic_LatestModified
  categories:
    - id: 5568
    - id: 5535
      max_pages: 10
`))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	if got, want := cfg.Tracker.PollInterval.Unwrap(), 30*time.Second; got != want {
		t.Errorf("poll_interval = %s, want %s", got, want)
	}

	if cfg.Tracker.FetchDetails {
		t.Error("fetch_details = true, want the file's false to win over the default")
	}

	first, second := cfg.Tracker.Categories[0], cfg.Tracker.Categories[1]

	if first.MaxPages != 3 {
		t.Errorf("category %d max_pages = %d, want the default 3", first.ID, first.MaxPages)
	}

	if second.MaxPages != 10 {
		t.Errorf("category %d max_pages = %d, want its own 10", second.ID, second.MaxPages)
	}

	if second.PerPage != 20 {
		t.Errorf("category %d per_page = %d, want the default 20", second.ID, second.PerPage)
	}
}

func TestLoadRejectsInvalid(t *testing.T) {
	tests := []struct {
		name string
		body string
	}{
		{
			name: "no categories",
			body: "tracker:\n  poll_interval: 5m\n",
		},
		{
			name: "duplicate category",
			body: "tracker:\n  categories:\n    - id: 5568\n    - id: 5568\n",
		},
		{
			name: "per_page above the api cap",
			body: "tracker:\n  categories:\n    - id: 5568\n      per_page: 100\n",
		},
		{
			name: "negative ts_init",
			body: "tracker:\n  ts_init: -1\n  categories:\n    - id: 5568\n",
		},
		{
			name: "unknown log level",
			body: "log:\n  level: verbose\ntracker:\n  categories:\n    - id: 5568\n",
		},
		{
			name: "empty storage path",
			body: "storage:\n  path: \"\"\ntracker:\n  categories:\n    - id: 5568\n",
		},
		{
			name: "max delay below base delay",
			body: "gamebanana:\n  retry:\n    base_delay: 10s\n    max_delay: 1s\ntracker:\n  categories:\n    - id: 5568\n",
		},
		{
			name: "malformed yaml",
			body: "tracker: [",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := Load(writeConfig(t, tt.body)); err == nil {
				t.Fatal("load: expected an error")
			}
		})
	}
}

func TestLoadMissingFile(t *testing.T) {
	if _, err := Load(filepath.Join(t.TempDir(), "absent.yaml")); err == nil {
		t.Fatal("load: expected an error for a missing file")
	}
}

func TestClientConfig(t *testing.T) {
	cfg, err := Load(writeConfig(t, minimalConfig))
	if err != nil {
		t.Fatalf("load: %v", err)
	}

	client := cfg.GameBanana.ClientConfig()
	if err := client.Validate(); err != nil {
		t.Fatalf("the client rejected a config we consider valid: %v", err)
	}

	if got, want := client.BaseDelay, 2*time.Second; got != want {
		t.Errorf("base delay = %s, want %s", got, want)
	}

	if got, want := client.RetryAttempts, 3; got != want {
		t.Errorf("retry attempts = %d, want %d", got, want)
	}
}
