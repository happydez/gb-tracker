// Package config loads the service settings.
package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/happydez/gb-tracker/pkg/bus"

	"github.com/happydez/gb-mms/internal/download"
	ftpsvc "github.com/happydez/gb-mms/internal/ftp"
	"github.com/happydez/gb-mms/internal/match"
	rconsvc "github.com/happydez/gb-mms/internal/rcon"
)

type Config struct {
	Log      Log      `yaml:"log"`
	NATS     NATS     `yaml:"nats"`
	Storage  Storage  `yaml:"storage"`
	Work     Work     `yaml:"work"`
	Download Download `yaml:"download"`
	Maps     Maps     `yaml:"maps"`
	FTP      FTP      `yaml:"ftp"`
	RCON     RCON     `yaml:"rcon"`
	Announce Announce `yaml:"announce"`
}

type Log struct {
	Level  string `yaml:"level"`  // debug | info | warn | error
	Format string `yaml:"format"` // text | json
}

type NATS struct {
	URL    string `yaml:"url"`
	Stream string `yaml:"stream"`

	// Durable must differ from every other subscriber's, or they would split
	// the stream between them instead of each seeing all of it.
	Durable  string   `yaml:"durable"`
	Subjects []string `yaml:"subjects"`
}

type Storage struct {
	Path string `yaml:"path"`
}

type Work struct {
	// Dir holds downloads and extracted maps while a mod is being processed.
	Dir string `yaml:"dir"`

	// Keep leaves the working files on disk instead of removing them once the
	// mod is done. Useful while debugging an archive that unpacks oddly.
	Keep bool `yaml:"keep"`
}

type Download struct {
	UserAgent string   `yaml:"user_agent"`
	Timeout   Duration `yaml:"timeout"`
	Attempts  int      `yaml:"attempts"`
}

type Maps struct {
	// Routes send maps to a folder of their own on the FastDL host. Order
	// matters: the first matching prefix wins, so "kz_bhop_" has to sit above
	// "kz_".
	Routes []Route `yaml:"routes"`

	// FallbackDir takes maps that matched no route. Empty drops them, which is
	// what makes the route list a filter as well as a map. "." keeps them in
	// the base directory itself.
	FallbackDir string `yaml:"fallback_dir"`

	// Categories override FallbackDir for one category: a broad listing wants
	// a strict filter, one already about a single kind of map does not.
	Categories []CategoryRule `yaml:"categories"`

	// MaxDepth limits recursion into nested archives. A .tar.gz costs two
	// levels: gzip yields the .tar, the .tar yields its contents.
	MaxDepth int `yaml:"max_depth"`

	// CompressionLevel is the bzip2 level used when a .bsp.bz2 has to be
	// derived from a .bsp the author shipped alone.
	CompressionLevel int `yaml:"compression_level"`
}

type Route struct {
	Prefix string `yaml:"prefix"`

	// Dir is relative to ftp.base_dir. Empty puts the maps straight into it.
	Dir string `yaml:"dir"`
}

// CategoryRule overrides the fallback for the category the tracker found the
// mod in. Listing a category is the override, so an empty dir means "drop what
// did not match" even when the global fallback keeps it.
type CategoryRule struct {
	ID          int    `yaml:"id"`
	FallbackDir string `yaml:"fallback_dir"`
}

type FTP struct {
	// Enabled off leaves the maps on disk and says so in the announcement,
	// rather than claiming an upload that never happened.
	Enabled bool `yaml:"enabled"`

	Addr string `yaml:"addr"`

	// Credentials belong in the environment, expanded here as ${GB_FTP_USER}.
	User     string `yaml:"user"`
	Password string `yaml:"password"`

	// BaseDir is the root every map lands under; the route decides the folder
	// inside it.
	BaseDir string `yaml:"base_dir"`

	// BytesPerSec caps throughput so a big map does not saturate the link.
	BytesPerSec int `yaml:"bytes_per_sec"`

	UseTLS      bool     `yaml:"use_tls"`
	DisableEPSV bool     `yaml:"disable_epsv"`
	DialTimeout Duration `yaml:"dial_timeout"`
}

type RCON struct {
	// Enabled off skips the game servers entirely and says so in the
	// announcement.
	Enabled bool `yaml:"enabled"`

	Servers []RCONServer `yaml:"servers"`

	// Commands run once per map, in order. %s is the map name.
	Commands []string `yaml:"commands"`

	Timeout Duration `yaml:"timeout"`

	// CmdDelay spaces out commands: Source processes RCON on the main thread,
	// and a burst of them stutters the running round.
	CmdDelay Duration `yaml:"cmd_delay"`
}

type RCONServer struct {
	Address string `yaml:"address"`

	// Password belongs in the environment, expanded as ${GB_RCON_PASSWORD}.
	Password string `yaml:"password"`
}

type Announce struct {
	// Channel is a name from the notifier's config, such as "public".
	Channel string `yaml:"channel"`

	// Enabled off means the pipeline runs without telling anyone.
	Enabled bool `yaml:"enabled"`
}

func Load(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := Default()
	if err := yaml.Unmarshal([]byte(os.ExpandEnv(string(data))), cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func Default() *Config {
	broker := bus.DefaultConfig()
	consumer := bus.DefaultConsumerConfig("gb-mms", "mods.discovered")
	dl := download.DefaultConfig()

	return &Config{
		Log: Log{Level: "info", Format: "text"},
		NATS: NATS{
			URL:      broker.URL,
			Stream:   broker.Stream,
			Durable:  consumer.Durable,
			Subjects: consumer.Subjects,
		},
		Storage: Storage{Path: "/data/gbmms.db"},
		Work:    Work{Dir: "/data/work"},
		Download: Download{
			UserAgent: dl.UserAgent,
			Timeout:   Duration(dl.Timeout),
			Attempts:  dl.Attempts,
		},
		Maps: Maps{
			MaxDepth:         8,
			CompressionLevel: 9,
		},
		FTP: FTP{DialTimeout: Duration(ftpsvc.DefaultConfig().DialTimeout)},
		RCON: RCON{
			Commands: rconsvc.DefaultConfig().Commands,
			Timeout:  Duration(rconsvc.DefaultConfig().Timeout),
			CmdDelay: Duration(rconsvc.DefaultConfig().CmdDelay),
		},
		Announce: Announce{Channel: "public", Enabled: true},
	}
}

func (c *Config) Validate() error {
	switch c.Log.Level {
	case "debug", "info", "warn", "error":
	default:
		return fmt.Errorf("log.level must be one of debug, info, warn, error, got %q", c.Log.Level)
	}

	switch c.Log.Format {
	case "text", "json":
	default:
		return fmt.Errorf("log.format must be text or json, got %q", c.Log.Format)
	}

	if c.Storage.Path == "" {
		return fmt.Errorf("storage.path must not be empty")
	}

	if c.Work.Dir == "" {
		return fmt.Errorf("work.dir must not be empty")
	}

	if c.Maps.MaxDepth < 1 {
		return fmt.Errorf("maps.max_depth must be at least 1, got %d", c.Maps.MaxDepth)
	}

	if c.Maps.CompressionLevel < 1 || c.Maps.CompressionLevel > 9 {
		return fmt.Errorf("maps.compression_level must be between 1 and 9, got %d", c.Maps.CompressionLevel)
	}

	if c.Announce.Enabled && c.Announce.Channel == "" {
		return fmt.Errorf("announce.channel must not be empty while announcing is enabled")
	}

	if _, err := c.Matcher(); err != nil {
		return err
	}

	if err := c.BusConfig().Validate(); err != nil {
		return err
	}

	if err := c.ConsumerConfig().Validate(); err != nil {
		return err
	}

	if c.FTP.Enabled {
		if err := c.FTPConfig().Validate(); err != nil {
			return err
		}
	}

	if c.RCON.Enabled {
		if err := c.RCONConfig().Validate(); err != nil {
			return err
		}
	}

	return c.DownloadConfig().Validate()
}

func (c *Config) BusConfig() bus.Config {
	out := bus.DefaultConfig()
	out.URL = c.NATS.URL
	out.Stream = c.NATS.Stream

	return out
}

func (c *Config) ConsumerConfig() bus.ConsumerConfig {
	return bus.DefaultConsumerConfig(c.NATS.Durable, c.NATS.Subjects...)
}

func (c *Config) DownloadConfig() download.Config {
	out := download.DefaultConfig()
	out.UserAgent = c.Download.UserAgent
	out.Attempts = c.Download.Attempts

	if d := c.Download.Timeout.Unwrap(); d > 0 {
		out.Timeout = d
	}

	return out
}

func (c *Config) Matcher() (*match.Matcher, error) {
	routes := make([]match.Route, 0, len(c.Maps.Routes))
	for _, r := range c.Maps.Routes {
		routes = append(routes, match.Route{Prefix: r.Prefix, Dir: r.Dir})
	}

	byCategory := make(map[int]string, len(c.Maps.Categories))
	for _, rule := range c.Maps.Categories {
		if _, dup := byCategory[rule.ID]; dup {
			return nil, fmt.Errorf("maps.categories: id %d is listed twice", rule.ID)
		}

		byCategory[rule.ID] = rule.FallbackDir
	}

	return match.New(routes, c.Maps.FallbackDir, byCategory)
}

// Duration reads "10s" and "5m" from YAML.
type Duration time.Duration

func (d Duration) Unwrap() time.Duration {
	return time.Duration(d)
}

func (d Duration) MarshalYAML() (any, error) {
	return time.Duration(d).String(), nil
}

func (d *Duration) UnmarshalYAML(node *yaml.Node) error {
	v, err := time.ParseDuration(node.Value)
	if err != nil {
		return err
	}

	*d = Duration(v)

	return nil
}

func (c *Config) FTPConfig() ftpsvc.Config {
	out := ftpsvc.DefaultConfig()
	out.Addr = c.FTP.Addr
	out.User = c.FTP.User
	out.Password = c.FTP.Password
	out.BaseDir = c.FTP.BaseDir
	out.BytesPerSec = c.FTP.BytesPerSec
	out.UseTLS = c.FTP.UseTLS
	out.DisableEPSV = c.FTP.DisableEPSV

	if d := c.FTP.DialTimeout.Unwrap(); d > 0 {
		out.DialTimeout = d
	}

	return out
}

func (c *Config) RCONConfig() rconsvc.Config {
	out := rconsvc.DefaultConfig()

	out.Servers = make([]rconsvc.Server, 0, len(c.RCON.Servers))
	for _, s := range c.RCON.Servers {
		out.Servers = append(out.Servers, rconsvc.Server{Address: s.Address, Password: s.Password})
	}

	if len(c.RCON.Commands) > 0 {
		out.Commands = c.RCON.Commands
	}

	if d := c.RCON.Timeout.Unwrap(); d > 0 {
		out.Timeout = d
	}

	if d := c.RCON.CmdDelay.Unwrap(); d > 0 {
		out.CmdDelay = d
	}

	return out
}
