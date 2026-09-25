// Command tracker polls GameBanana categories and publishes an event for
// every newly discovered or updated mod.
package main

import (
	"context"
	"flag"
	"log"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/happydez/gb-tracker/internal/config"
	"github.com/happydez/gb-tracker/internal/gb"
	"github.com/happydez/gb-tracker/internal/storage"
	"github.com/happydez/gb-tracker/internal/tracker"
	"github.com/happydez/gb-tracker/pkg/bus"
)

var configPath = flag.String("config", "config.yaml", "path to the config file")

func main() {
	if err := run(); err != nil {
		log.Fatal(err)
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

	known, err := store.Count(ctx)
	if err != nil {
		return err
	}

	log.Info("storage ready", "path", cfg.Storage.Path, "known_mods", known)

	pub, err := bus.Connect(ctx, cfg.NATS.BusConfig(), log)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := pub.Close(); closeErr != nil {
			log.Error("closing nats failed", "error", closeErr)
		}
	}()

	client := gb.NewClient(cfg.GameBanana.ClientConfig())

	tracker.New(cfg.Tracker, client, store, pub, log).Run(ctx)

	return nil
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
