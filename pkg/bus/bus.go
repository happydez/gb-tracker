// Package bus carries events over NATS JetStream.
package bus

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"time"

	"github.com/nats-io/nats.go"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/happydez/gb-tracker/pkg/events"
)

type Config struct {
	URL string

	// Stream is the JetStream stream name and Subjects the patterns it
	// captures. Every subject any service publishes on has to be listed.
	Stream   string
	Subjects []string

	// MaxAge is how long an event stays available to a consumer that has not
	// read it yet. It is the length of the outage a subscriber can survive.
	MaxAge time.Duration

	// DuplicateWindow is how long the server remembers message ids in order to
	// drop a repeat. See Publish for what that does and does not cover.
	DuplicateWindow time.Duration

	ConnectTimeout time.Duration
	PublishTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{
		URL:             nats.DefaultURL,
		Stream:          "GBTRACKER",
		Subjects:        []string{"mods.>", "errors.>", "notifications.>"},
		MaxAge:          7 * 24 * time.Hour,
		DuplicateWindow: 2 * time.Hour,
		ConnectTimeout:  10 * time.Second,
		PublishTimeout:  10 * time.Second,
	}
}

func (c Config) Validate() error {
	if c.URL == "" {
		return fmt.Errorf("bus: url must not be empty")
	}
	if c.Stream == "" {
		return fmt.Errorf("bus: stream must not be empty")
	}
	if len(c.Subjects) == 0 {
		return fmt.Errorf("bus: subjects must not be empty")
	}
	if c.MaxAge <= 0 {
		return fmt.Errorf("bus: max_age must be positive")
	}
	if c.DuplicateWindow <= 0 {
		return fmt.Errorf("bus: duplicate_window must be positive")
	}
	if c.DuplicateWindow > c.MaxAge {
		return fmt.Errorf("bus: duplicate_window (%s) must not exceed max_age (%s)", c.DuplicateWindow, c.MaxAge)
	}
	if c.ConnectTimeout <= 0 {
		return fmt.Errorf("bus: connect_timeout must be positive")
	}
	if c.PublishTimeout <= 0 {
		return fmt.Errorf("bus: publish_timeout must be positive")
	}

	return nil
}

type Bus struct {
	cfg Config
	nc  *nats.Conn
	js  jetstream.JetStream
	log *slog.Logger
}

// Connect dials NATS and makes sure the stream exists.
func Connect(ctx context.Context, cfg Config, log *slog.Logger) (*Bus, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	nc, err := nats.Connect(cfg.URL,
		nats.Name("gb-tracker"),
		nats.Timeout(cfg.ConnectTimeout),
		nats.MaxReconnects(-1),
		nats.ReconnectWait(2*time.Second),
		nats.DisconnectErrHandler(func(_ *nats.Conn, err error) {
			log.Warn("nats disconnected", "error", err)
		}),
		nats.ReconnectHandler(func(c *nats.Conn) {
			log.Info("nats reconnected", "url", c.ConnectedUrl())
		}),
	)
	if err != nil {
		return nil, fmt.Errorf("connect to nats at %s: %w", cfg.URL, err)
	}

	js, err := jetstream.New(nc)
	if err != nil {
		nc.Close()

		return nil, fmt.Errorf("init jetstream: %w", err)
	}

	b := &Bus{cfg: cfg, nc: nc, js: js, log: log}

	if err := b.ensureStream(ctx); err != nil {
		nc.Close()

		return nil, err
	}

	log.Info("nats ready", "url", nc.ConnectedUrl(), "stream", cfg.Stream, "subjects", cfg.Subjects)

	return b, nil
}

// ensureStream creates the stream, or updates it when the config changed.
// Running this on every start means the stream is never configured by hand.
func (b *Bus) ensureStream(ctx context.Context) error {
	_, err := b.js.CreateOrUpdateStream(ctx, jetstream.StreamConfig{
		Name:     b.cfg.Stream,
		Subjects: b.cfg.Subjects,

		// Keep events until they age out, regardless of who has read them:
		// the publisher must not depend on subscribers existing at all.
		Retention: jetstream.LimitsPolicy,
		MaxAge:    b.cfg.MaxAge,

		// On disk, so a broker restart does not lose what nobody has read.
		Storage: jetstream.FileStorage,

		Duplicates: b.cfg.DuplicateWindow,
	})
	if err != nil {
		return fmt.Errorf("create stream %s: %w", b.cfg.Stream, err)
	}

	return nil
}

// Publish sends an event on the subject named by its type.
func (b *Bus) Publish(ctx context.Context, ev events.Event) error {
	if err := ev.Validate(); err != nil {
		return err
	}

	body, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event %s: %w", ev.ID, err)
	}

	ctx, cancel := context.WithTimeout(ctx, b.cfg.PublishTimeout)
	defer cancel()

	ack, err := b.js.Publish(ctx, ev.Type, body, jetstream.WithMsgID(ev.ID))
	if err != nil {
		return fmt.Errorf("publish %s: %w", ev.Type, err)
	}

	if ack.Duplicate {
		b.log.Warn("event was a duplicate, the server dropped it", "type", ev.Type, "id", ev.ID)
	}

	return nil
}

// Close flushes anything still buffered before shutting the connection down,
// so events published moments before a signal are not lost.
func (b *Bus) Close() error {
	if err := b.nc.FlushTimeout(b.cfg.PublishTimeout); err != nil {
		b.log.Warn("flushing nats failed", "error", err)
	}

	b.nc.Close()

	return nil
}
