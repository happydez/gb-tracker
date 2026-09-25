package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/happydez/gb-tracker/internal/gb"
	"github.com/happydez/gb-tracker/pkg/bus"
)

type Config struct {
	Log        Log        `yaml:"log"`
	Storage    Storage    `yaml:"storage"`
	NATS       NATS       `yaml:"nats"`
	GameBanana GameBanana `yaml:"gamebanana"`
	Tracker    Tracker    `yaml:"tracker"`
}

// NATS configures the event bus.
type NATS struct {
	URL             string   `yaml:"url"`
	Stream          string   `yaml:"stream"`
	Subjects        []string `yaml:"subjects"`
	MaxAge          Duration `yaml:"max_age"`
	DuplicateWindow Duration `yaml:"duplicate_window"`
	ConnectTimeout  Duration `yaml:"connect_timeout"`
	PublishTimeout  Duration `yaml:"publish_timeout"`
}

// BusConfig converts the YAML-facing config into the bus package's own.
func (n NATS) BusConfig() bus.Config {
	return bus.Config{
		URL:             n.URL,
		Stream:          n.Stream,
		Subjects:        n.Subjects,
		MaxAge:          n.MaxAge.Unwrap(),
		DuplicateWindow: n.DuplicateWindow.Unwrap(),
		ConnectTimeout:  n.ConnectTimeout.Unwrap(),
		PublishTimeout:  n.PublishTimeout.Unwrap(),
	}
}

func (n NATS) validate() error {
	if err := n.BusConfig().Validate(); err != nil {
		return fmt.Errorf("nats: %w", err)
	}

	return nil
}

type Log struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // text | json
}

type Storage struct {
	// Path is the SQLite database file. Its directory must exist.
	Path string `yaml:"path"`
}

// GameBanana configures the API client.
type GameBanana struct {
	ModIndexURL  string `yaml:"mod_index_url"`
	ModURL       string `yaml:"mod_url"`
	UserAgent    string `yaml:"user_agent"`
	MaxBodyBytes int64  `yaml:"max_body_bytes"`
	Retry        Retry  `yaml:"retry"`
}

type Retry struct {
	// Attempts counts retries after the first try: 0 means no retrying.
	Attempts  int      `yaml:"attempts"`
	BaseDelay Duration `yaml:"base_delay"`
	MaxDelay  Duration `yaml:"max_delay"`
}

// ClientConfig converts the YAML-facing config into the client's own.
func (g GameBanana) ClientConfig() gb.Config {
	return gb.Config{
		ModIndexURL:   g.ModIndexURL,
		ModURL:        g.ModURL,
		UserAgent:     g.UserAgent,
		MaxBodyBytes:  g.MaxBodyBytes,
		RetryAttempts: g.Retry.Attempts,
		BaseDelay:     g.Retry.BaseDelay.Unwrap(),
		MaxDelay:      g.Retry.MaxDelay.Unwrap(),
	}
}

type Tracker struct {
	// PollInterval is how often every category is polled.
	PollInterval Duration `yaml:"poll_interval"`

	// TSInit filters every poll, not only the first: a mod whose mdate is not
	// newer than this Unix timestamp is ignored and never stored. Setting it
	// to the moment of deployment is what stops a first run from announcing
	// the whole back catalogue. 0 disables the filter.
	TSInit int64 `yaml:"ts_init"`

	// FetchDetails additionally queries Core/Item/Data for every new mod and
	// puts the result in the published event, so that subscribers do not each
	// have to query it themselves.
	FetchDetails bool `yaml:"fetch_details"`

	// Defaults fills in the per-category paging settings left unset below.
	Defaults CategoryDefaults `yaml:"defaults"`

	Categories []Category `yaml:"categories"`
}

// CategoryDefaults are the paging settings shared by every category.
type CategoryDefaults struct {
	PerPage  int    `yaml:"per_page"`
	MaxPages int    `yaml:"max_pages"`
	Sort     string `yaml:"sort"`
}

// Category is one GameBanana category to watch.
type Category struct {
	ID int `yaml:"id"`

	// Label names the category in logs. It is never sent to the API.
	Label string `yaml:"label"`

	// Zero values here are replaced by Tracker.Defaults on load.
	PerPage  int    `yaml:"per_page"`
	MaxPages int    `yaml:"max_pages"`
	Sort     string `yaml:"sort"`
}

// Load reads the config file over the defaults and validates the result.
func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := Default()
	if err := yaml.Unmarshal(data, cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	cfg.Tracker.applyDefaults()

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

// applyDefaults fills the paging settings a category left unset. It cannot run
// as part of Default, because the categories themselves only exist once the
// YAML has been read.
func (t *Tracker) applyDefaults() {
	for i := range t.Categories {
		c := &t.Categories[i]

		if c.PerPage == 0 {
			c.PerPage = t.Defaults.PerPage
		}
		if c.MaxPages == 0 {
			c.MaxPages = t.Defaults.MaxPages
		}
		if c.Sort == "" {
			c.Sort = t.Defaults.Sort
		}
	}
}

func (c *Config) Validate() error {
	if err := c.Log.validate(); err != nil {
		return err
	}
	if err := c.Storage.validate(); err != nil {
		return err
	}
	if err := c.NATS.validate(); err != nil {
		return err
	}
	if err := c.GameBanana.validate(); err != nil {
		return err
	}

	return c.Tracker.validate()
}

func (l Log) validate() error {
	switch l.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log.level must be one of debug, info, warn, error, got %q", l.Level)
	}

	switch l.Format {
	case "text", "json":
	default:
		return fmt.Errorf("log.format must be text or json, got %q", l.Format)
	}

	return nil
}

func (s Storage) validate() error {
	if s.Path == "" {
		return fmt.Errorf("storage.path must not be empty")
	}

	return nil
}

func (g GameBanana) validate() error {
	if err := g.ClientConfig().Validate(); err != nil {
		return fmt.Errorf("gamebanana: %w", err)
	}

	return nil
}

func (t Tracker) validate() error {
	if t.PollInterval <= 0 {
		return fmt.Errorf("tracker.poll_interval must be positive")
	}
	if t.TSInit < 0 {
		return fmt.Errorf("tracker.ts_init must not be negative, got %d", t.TSInit)
	}
	if len(t.Categories) == 0 {
		return fmt.Errorf("tracker.categories must not be empty")
	}

	seen := make(map[int]struct{}, len(t.Categories))
	for i, c := range t.Categories {
		if c.ID <= 0 {
			return fmt.Errorf("tracker.categories[%d].id must be positive, got %d", i, c.ID)
		}

		if _, dup := seen[c.ID]; dup {
			return fmt.Errorf("tracker.categories: id %d is listed more than once", c.ID)
		}
		seen[c.ID] = struct{}{}

		// PerPage is capped by the API itself, which silently clamps anything
		// larger and would leave the tracker expecting records it never gets.
		if c.PerPage < 1 || c.PerPage > 50 {
			return fmt.Errorf("tracker.categories[%d] (%d): per_page must be between 1 and 50, got %d", i, c.ID, c.PerPage)
		}
		if c.MaxPages < 1 {
			return fmt.Errorf("tracker.categories[%d] (%d): max_pages must be at least 1, got %d", i, c.ID, c.MaxPages)
		}
		if c.Sort == "" {
			return fmt.Errorf("tracker.categories[%d] (%d): sort must not be empty", i, c.ID)
		}
	}

	return nil
}

// Default returns the configuration the YAML file is layered onto.
func Default() *Config {
	client := gb.DefaultConfig()
	broker := bus.DefaultConfig()

	return &Config{
		Log: Log{
			Level:  "info",
			Format: "text",
		},
		Storage: Storage{
			Path: "/data/gbtracker.db",
		},
		NATS: NATS{
			URL:             broker.URL,
			Stream:          broker.Stream,
			Subjects:        broker.Subjects,
			MaxAge:          Duration(broker.MaxAge),
			DuplicateWindow: Duration(broker.DuplicateWindow),
			ConnectTimeout:  Duration(broker.ConnectTimeout),
			PublishTimeout:  Duration(broker.PublishTimeout),
		},
		GameBanana: GameBanana{
			ModIndexURL:  client.ModIndexURL,
			ModURL:       client.ModURL,
			UserAgent:    "GB-Tracker/1.0",
			MaxBodyBytes: client.MaxBodyBytes,
			Retry: Retry{
				Attempts:  client.RetryAttempts,
				BaseDelay: Duration(client.BaseDelay),
				MaxDelay:  Duration(client.MaxDelay),
			},
		},
		Tracker: Tracker{
			PollInterval: Duration(5 * time.Minute),
			TSInit:       0,
			FetchDetails: true,
			Defaults: CategoryDefaults{
				PerPage:  15,
				MaxPages: 5,
				Sort:     "Generic_LatestModified",
			},
		},
	}
}
