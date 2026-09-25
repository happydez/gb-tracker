// Command gbmms downloads maps announced by gb-tracker, puts them on FastDL
// and tells the game servers about them.
package main

import (
	"context"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/happydez/gb-tracker/pkg/bus"
	"github.com/happydez/gb-tracker/pkg/events"

	"github.com/happydez/gb-mms/internal/announce"
	"github.com/happydez/gb-mms/internal/config"
	"github.com/happydez/gb-mms/internal/download"
	"github.com/happydez/gb-mms/internal/ftp"
	"github.com/happydez/gb-mms/internal/match"
	"github.com/happydez/gb-mms/internal/pipeline"
	"github.com/happydez/gb-mms/internal/rcon"
	"github.com/happydez/gb-mms/internal/storage"
)

var configPath = flag.String("config", "gbmms.yaml", "path to the config file")

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	flag.Parse()

	cfg, err := config.Load(*configPath)
	if err != nil {
		return err
	}

	log := newLogger(cfg.Log)

	matcher, err := cfg.Matcher()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	store, err := storage.Open(ctx, cfg.Storage.Path)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := store.Close(); closeErr != nil {
			log.Error("closing the database failed", "error", closeErr)
		}
	}()

	known, err := store.CountMods(ctx)
	if err != nil {
		return err
	}

	b, err := bus.Connect(ctx, cfg.BusConfig(), log)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := b.Close(); closeErr != nil {
			log.Error("closing nats failed", "error", closeErr)
		}
	}()

	var announcer pipeline.Announcer
	if cfg.Announce.Enabled {
		acfg := announce.DefaultConfig()
		acfg.Channel = cfg.Announce.Channel

		if err = acfg.Validate(); err != nil {
			return err
		}

		announcer = announce.New(acfg, b)
	}

	var uploader pipeline.Uploader
	if cfg.FTP.Enabled {
		up := ftp.New(cfg.FTPConfig())
		uploader = up

		log.Info("ftp enabled", "addr", cfg.FTP.Addr, "dir", up.RemotePath(""))
	}

	var commander pipeline.Commander
	if cfg.RCON.Enabled {
		commander = rcon.New(cfg.RCONConfig())
		log.Info("rcon enabled", "servers", len(cfg.RCON.Servers), "commands", cfg.RCONConfig().Commands)
	}

	pipe := pipeline.New(
		pipeline.Config{
			WorkDir:          cfg.Work.Dir,
			KeepWork:         cfg.Work.Keep,
			MaxDepth:         cfg.Maps.MaxDepth,
			CompressionLevel: cfg.Maps.CompressionLevel,
		},
		matcher,
		download.New(cfg.DownloadConfig()),
		store,
		uploader,
		commander,
		announcer,
		log,
	)

	sub, err := b.Subscribe(ctx, cfg.ConsumerConfig(), handler(pipe, log))
	if err != nil {
		return err
	}

	log.Info("gb-mms started",
		"known_mods", known,
		"work_dir", cfg.Work.Dir,
		"prefixes", matcher.Prefixes(),
		"fallback_dir", fallbackDir(matcher.Fallback()),
		"category_fallbacks", categoryFallbacks(matcher),
		"subjects", cfg.NATS.Subjects,
	)

	<-ctx.Done()

	sub.Stop()
	log.Info("gb-mms stopped")

	return nil
}

func handler(pipe *pipeline.Pipeline, log *slog.Logger) bus.Handler {
	return func(ctx context.Context, ev events.Event) error {
		if ev.Type != events.TypeModDiscovered {
			log.Debug("ignoring event", "type", ev.Type, "id", ev.ID)

			return nil
		}

		mod, err := events.DataOf[events.ModDiscovered](ev)
		if err != nil {
			return fmt.Errorf("%w: decode mod: %w", bus.ErrTerminal, err)
		}

		if err := pipe.Process(ctx, mod); err != nil {
			return fmt.Errorf("process mod %d: %w", mod.ModID, err)
		}

		return nil
	}
}

func newLogger(cfg config.Log) *slog.Logger {
	var level slog.Level
	switch cfg.Level {
	case "debug":
		level = slog.LevelDebug
	case "warn":
		level = slog.LevelWarn
	case "error":
		level = slog.LevelError
	default:
		level = slog.LevelInfo
	}

	opts := &slog.HandlerOptions{Level: level}

	var handler slog.Handler
	if cfg.Format == "json" {
		handler = slog.NewJSONHandler(os.Stdout, opts)
	} else {
		handler = slog.NewTextHandler(os.Stdout, opts)
	}

	return slog.New(handler)
}

// fallbackDir names the folder unmatched maps go to, or says there is none.
func fallbackDir(dir string, ok bool) string {
	switch {
	case !ok:
		return "(none, unmatched maps are dropped)"
	case dir == "":
		return "(base dir)"
	default:
		return dir
	}
}

// categoryFallbacks renders the per-category overrides as "5568=other".
func categoryFallbacks(m *match.Matcher) []string {
	ids := m.CategoryIDs()
	out := make([]string, 0, len(ids))

	for _, id := range ids {
		out = append(out, fmt.Sprintf("%d=%s", id, fallbackDir(m.FallbackFor(id))))
	}

	return out
}
