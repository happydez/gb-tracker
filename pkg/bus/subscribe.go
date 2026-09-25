package bus

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/nats-io/nats.go/jetstream"

	"github.com/happydez/gb-tracker/pkg/events"
)

var ErrTerminal = errors.New("terminal failure")

// Handler processes one event.
type Handler func(ctx context.Context, ev events.Event) error

type ConsumerConfig struct {
	// Durable is the consumer name, and with it the cursor that survives a
	// restart. Two services must not share one, or they would split the
	// stream between themselves instead of each seeing all of it.
	Durable string

	// Subjects filters what this consumer receives, e.g. "mods.>".
	Subjects []string

	// MaxDeliver caps redeliveries of a message that keeps failing. Past it
	// the message is dropped, so one poisonous event cannot occupy the
	// consumer forever.
	MaxDeliver int

	// BackOff is the delay before each redelivery. A message redelivered more
	// times than there are entries reuses the last one.
	BackOff []time.Duration
}

// DefaultAckWait is how long a handler may take before the server assumes it
// died. Deliveries to a chat platform take seconds, not milliseconds.
const DefaultAckWait = 30 * time.Second

func DefaultConsumerConfig(durable string, subjects ...string) ConsumerConfig {
	return ConsumerConfig{
		Durable:    durable,
		Subjects:   subjects,
		MaxDeliver: 5,
		BackOff:    []time.Duration{DefaultAckWait, time.Minute, 5 * time.Minute, 15 * time.Minute},
	}
}

func (c ConsumerConfig) Validate() error {
	if c.Durable == "" {
		return fmt.Errorf("bus: consumer durable must not be empty")
	}
	if len(c.Subjects) == 0 {
		return fmt.Errorf("bus: consumer subjects must not be empty")
	}
	if c.MaxDeliver < 1 {
		return fmt.Errorf("bus: consumer max_deliver must be at least 1, got %d", c.MaxDeliver)
	}
	if len(c.BackOff) == 0 {
		return fmt.Errorf("bus: consumer backoff must not be empty")
	}

	// The first step is the ack deadline, so a value measured in milliseconds
	// redelivers every message a slow handler is still working on.
	if c.BackOff[0] < time.Second {
		return fmt.Errorf("bus: consumer backoff[0] is the ack deadline and must be at least a second, got %s", c.BackOff[0])
	}

	return nil
}

// Subscription is a running consumer. Stop waits for the handler to finish
// with the message it is on.
type Subscription struct {
	ctx jetstream.ConsumeContext
}

func (s *Subscription) Stop() {
	s.ctx.Drain()
	<-s.ctx.Closed()
}

// Subscribe starts delivering matching events to h and returns once the
// consumer is running. Messages arrive concurrently with the caller.
func (b *Bus) Subscribe(ctx context.Context, cfg ConsumerConfig, h Handler) (*Subscription, error) {
	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	cons, err := b.js.CreateOrUpdateConsumer(ctx, b.cfg.Stream, jetstream.ConsumerConfig{
		Durable:        cfg.Durable,
		FilterSubjects: cfg.Subjects,
		AckPolicy:      jetstream.AckExplicitPolicy,
		MaxDeliver:     cfg.MaxDeliver,
		BackOff:        cfg.BackOff,
		AckWait:        ackWait(cfg),
	})
	if err != nil {
		return nil, fmt.Errorf("create consumer %s: %w", cfg.Durable, err)
	}

	consumeCtx, err := cons.Consume(func(msg jetstream.Msg) {
		b.handle(ctx, h, msg)
	})
	if err != nil {
		return nil, fmt.Errorf("consume %s: %w", cfg.Durable, err)
	}

	b.log.Info("subscribed", "durable", cfg.Durable, "subjects", cfg.Subjects)

	return &Subscription{ctx: consumeCtx}, nil
}

// handle turns the outcome of one delivery into one of the three answers
// JetStream understands, which is the whole of a consumer's error handling:
//
//	Ack  - done, move the cursor on
//	Nak  - temporary, send it again after the backoff
//	Term - hopeless, drop it and move on
func (b *Bus) handle(ctx context.Context, h Handler, msg jetstream.Msg) {
	var ev events.Event

	if err := json.Unmarshal(msg.Data(), &ev); err != nil {
		b.log.Error("dropping unparsable message", "subject", msg.Subject(), "error", err)
		b.term(msg)

		return
	}

	if err := ev.Validate(); err != nil {
		b.log.Error("dropping malformed event", "subject", msg.Subject(), "error", err)
		b.term(msg)

		return
	}

	err := h(ctx, ev)
	switch {
	case err == nil:
		if ackErr := msg.Ack(); ackErr != nil {
			b.log.Error("ack failed", "id", ev.ID, "error", ackErr)
		}
	case errors.Is(err, ErrTerminal):
		b.log.Error("dropping event, retrying cannot help", "type", ev.Type, "id", ev.ID, "error", err)
		b.term(msg)
	default:
		b.log.Warn("handler failed, will retry", "type", ev.Type, "id", ev.ID, "error", err)

		if nakErr := msg.Nak(); nakErr != nil {
			b.log.Error("nak failed", "id", ev.ID, "error", nakErr)
		}
	}
}

func (b *Bus) term(msg jetstream.Msg) {
	if err := msg.Term(); err != nil {
		b.log.Error("term failed", "subject", msg.Subject(), "error", err)
	}
}

// ackWait returns the deadline the server will really apply.
func ackWait(cfg ConsumerConfig) time.Duration {
	if len(cfg.BackOff) > 0 {
		return cfg.BackOff[0]
	}

	return DefaultAckWait
}
