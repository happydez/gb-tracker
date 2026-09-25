// Command notifier delivers events to chat platforms.
package main

import (
	"context"
	"errors"
	"flag"
	"fmt"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/happydez/gb-tracker/internal/config"
	"github.com/happydez/gb-tracker/internal/notify"
	"github.com/happydez/gb-tracker/pkg/bus"
	"github.com/happydez/gb-tracker/pkg/events"
)

var configPath = flag.String("config", "notifier.yaml", "path to the config file")

func main() {
	if err := run(); err != nil {
		fmt.Fprintln(os.Stderr, "fatal:", err)
		os.Exit(1)
	}
}

func run() error {
	flag.Parse()

	cfg, err := config.LoadNotifier(*configPath)
	if err != nil {
		return err
	}

	log := newLogger(cfg.Log)

	router, err := notify.NewRouter(cfg.Notifier.RouterRoutes())
	if err != nil {
		return err
	}

	senders, err := cfg.Notifier.Senders()
	if err != nil {
		return err
	}

	dispatcher := notify.NewDispatcher(senders, log)

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	b, err := bus.Connect(ctx, cfg.NATS.BusConfig(), log)
	if err != nil {
		return err
	}
	defer func() {
		if closeErr := b.Close(); closeErr != nil {
			log.Error("closing nats failed", "error", closeErr)
		}
	}()

	handler := newHandler(router, dispatcher, log)

	sub, err := b.Subscribe(ctx, cfg.Notifier.ConsumerConfig(), handler)
	if err != nil {
		return err
	}

	log.Info("notifier started",
		"channels", dispatcher.Channels(),
		"routes", len(cfg.Notifier.Routes),
		"subjects", cfg.Notifier.Subjects,
	)

	<-ctx.Done()

	sub.Stop()
	log.Info("notifier stopped")

	return nil
}

// newHandler decides how an event becomes a notification, and translates a
// permanent failure into the bus's terminal error so the message is dropped
// rather than redelivered until it expires.
func newHandler(router *notify.Router, dispatcher *notify.Dispatcher, log *slog.Logger) bus.Handler {
	return func(ctx context.Context, ev events.Event) error {
		n, ok, err := build(router, ev)
		if err != nil {
			return terminalIfPermanent(err)
		}

		if !ok {
			log.Debug("no route for event, skipping", "type", ev.Type, "id", ev.ID)
			return nil
		}

		if err := dispatcher.Dispatch(ctx, n); err != nil {
			return terminalIfPermanent(err)
		}

		return nil
	}
}

func build(router *notify.Router, ev events.Event) (events.Notification, bool, error) {
	if ev.Type != events.TypeNotificationSend {
		return router.Render(ev)
	}

	n, err := events.DataOf[events.Notification](ev)
	if err != nil {
		return events.Notification{}, false, fmt.Errorf("%w: %w", notify.ErrPermanent, err)
	}

	if n.Channel == "" {
		return events.Notification{}, false, fmt.Errorf("%w: notification has no channel", notify.ErrPermanent)
	}

	return n, true, nil
}

func terminalIfPermanent(err error) error {
	if errors.Is(err, notify.ErrPermanent) {
		return fmt.Errorf("%w: %w", bus.ErrTerminal, err)
	}

	return err
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
