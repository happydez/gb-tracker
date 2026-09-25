// Package notify delivers notifications to chat platforms.
package notify

import (
	"context"
	"fmt"
	"log/slog"
	"sync"

	"github.com/happydez/gb-tracker/pkg/events"
)

// Sender delivers a notification to one destination.
type Sender interface {
	// Name identifies the sender in logs, e.g. "discord:public".
	Name() string

	Send(ctx context.Context, n events.Notification) error
}

// Dispatcher maps a channel name onto the senders configured for it.
type Dispatcher struct {
	channels map[string][]Sender
	log      *slog.Logger
}

func NewDispatcher(channels map[string][]Sender, log *slog.Logger) *Dispatcher {
	return &Dispatcher{channels: channels, log: log}
}

// Channels lists the configured channel names, for logging on startup.
func (d *Dispatcher) Channels() []string {
	out := make([]string, 0, len(d.channels))
	for name := range d.channels {
		out = append(out, name)
	}

	return out
}

// Dispatch sends a notification to every backend of its channel, in parallel.
func (d *Dispatcher) Dispatch(ctx context.Context, n events.Notification) error {
	senders, ok := d.channels[n.Channel]
	if !ok {
		return fmt.Errorf("%w: no channel named %q is configured", ErrPermanent, n.Channel)
	}

	if len(senders) == 0 {
		d.log.Warn("channel has no senders, nothing to do", "channel", n.Channel)
		return nil
	}

	var (
		mu   sync.Mutex
		errs []error
		wg   sync.WaitGroup
	)

	for _, s := range senders {
		wg.Go(func() {
			if err := s.Send(ctx, n); err != nil {
				d.log.Error("sending failed", "sender", s.Name(), "error", err)

				mu.Lock()
				errs = append(errs, fmt.Errorf("%s: %w", s.Name(), err))
				mu.Unlock()

				return
			}

			d.log.Info("notification sent", "sender", s.Name(), "title", n.Title)
		})
	}

	wg.Wait()

	if len(errs) > 0 {
		return fmt.Errorf("dispatch to %q: %w", n.Channel, joinErrors(errs))
	}

	return nil
}

var ErrPermanent = fmt.Errorf("permanent notify failure")

func joinErrors(errs []error) error {
	if len(errs) == 1 {
		return errs[0]
	}

	err := errs[0]
	for _, e := range errs[1:] {
		err = fmt.Errorf("%w; %w", err, e)
	}

	return err
}
