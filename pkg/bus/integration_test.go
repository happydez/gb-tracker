package bus

import (
	"context"
	"encoding/json"
	"io"
	"log/slog"
	"os"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
	"github.com/nats-io/nats.go/jetstream"

	"github.com/happydez/gb-tracker/pkg/events"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

// liveBus connects to the broker named by GB_TRACKER_TEST_NATS, skipping the
// test when it is unset.
func liveBus(t *testing.T) *Bus {
	t.Helper()

	url := os.Getenv("GB_TRACKER_TEST_NATS")
	if url == "" {
		t.Skip("set GB_TRACKER_TEST_NATS to a nats url to run the bus integration tests")
	}

	cfg := DefaultConfig()
	cfg.URL = url
	cfg.Stream = "GBTRACKER_TEST"
	cfg.Subjects = []string{"test.mods.>"}
	cfg.MaxAge = time.Hour
	cfg.DuplicateWindow = time.Minute

	b, err := Connect(t.Context(), cfg, testLogger())
	if err != nil {
		t.Fatalf("connect: %v", err)
	}

	if err := b.js.DeleteStream(t.Context(), cfg.Stream); err != nil {
		t.Logf("no leftover stream to delete: %v", err)
	}

	if err := b.ensureStream(t.Context()); err != nil {
		t.Fatalf("recreate stream: %v", err)
	}

	t.Cleanup(func() {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		if err := b.js.DeleteStream(ctx, cfg.Stream); err != nil {
			t.Logf("deleting the test stream failed: %v", err)
		}

		_ = b.Close()
	})

	return b
}

func testEvent(t *testing.T) events.Event {
	t.Helper()

	ev, err := events.New("test.mods.discovered", events.SourceTracker, events.ModDiscovered{
		ModID: 699260,
		Name:  "bhop_ln_portal",
		MDate: 1785528621,
	})
	if err != nil {
		t.Fatalf("new event: %v", err)
	}

	return ev
}

func TestPublishAndConsume(t *testing.T) {
	b := liveBus(t)
	ev := testEvent(t)

	if err := b.Publish(t.Context(), ev); err != nil {
		t.Fatalf("publish: %v", err)
	}

	cons, err := b.js.CreateOrUpdateConsumer(t.Context(), "GBTRACKER_TEST", jetstream.ConsumerConfig{
		Durable:       "test-consumer",
		FilterSubject: "test.mods.>",
		AckPolicy:     jetstream.AckExplicitPolicy,
	})
	if err != nil {
		t.Fatalf("create consumer: %v", err)
	}

	msg, err := cons.Next(jetstream.FetchMaxWait(5 * time.Second))
	if err != nil {
		t.Fatalf("fetch: %v", err)
	}

	if err = msg.Ack(); err != nil {
		t.Fatalf("ack: %v", err)
	}

	var got events.Event
	if err = json.Unmarshal(msg.Data(), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.ID != ev.ID {
		t.Errorf("id = %q, want %q", got.ID, ev.ID)
	}

	if msg.Subject() != ev.Type {
		t.Errorf("subject = %q, want %q", msg.Subject(), ev.Type)
	}

	payload, err := events.DataOf[events.ModDiscovered](got)
	if err != nil {
		t.Fatalf("data of: %v", err)
	}

	want, err := events.DataOf[events.ModDiscovered](ev)
	if err != nil {
		t.Fatalf("data of: %v", err)
	}

	if diff := cmp.Diff(want, payload); diff != "" {
		t.Errorf("payload mismatch (-want +got):\n%s", diff)
	}
}

func TestPublishDeduplicates(t *testing.T) {
	b := liveBus(t)
	ev := testEvent(t)

	for range 2 {
		if err := b.Publish(t.Context(), ev); err != nil {
			t.Fatalf("publish: %v", err)
		}
	}

	stream, err := b.js.Stream(t.Context(), "GBTRACKER_TEST")
	if err != nil {
		t.Fatalf("stream: %v", err)
	}

	info, err := stream.Info(t.Context())
	if err != nil {
		t.Fatalf("stream info: %v", err)
	}

	if info.State.Msgs != 1 {
		t.Errorf("stream holds %d messages, want 1 after publishing the same event twice", info.State.Msgs)
	}
}

func TestPublishRejectsInvalidEvent(t *testing.T) {
	b := liveBus(t)

	ev := testEvent(t)
	ev.ID = ""

	if err := b.Publish(t.Context(), ev); err == nil {
		t.Fatal("publish: expected an error for an event with no id")
	}
}
