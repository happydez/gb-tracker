package events

import (
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/oklog/ulid/v2"
)

// ErrInvalidEvent marks a malformed envelope. Subscribers match it with
// errors.Is to tell a permanently broken message (msg.Term) from a transient
// handler failure (msg.Nak).
var ErrInvalidEvent = errors.New("invalid event")

// Event is the envelope every service publishes and consumes.
type Event struct {
	ID         string          `json:"id"`
	Type       string          `json:"type"`
	OccurredAt time.Time       `json:"occurred_at"`
	Source     string          `json:"source"`
	Data       json.RawMessage `json:"data"`
}

// New builds an event with a freshly generated ULID and the current UTC time,
// marshalling data into the Data field.
func New(typ string, source string, data any) (Event, error) {
	raw, err := json.Marshal(data)
	if err != nil {
		return Event{}, fmt.Errorf("marshal %s payload: %w", typ, err)
	}

	return Event{
		ID:         ulid.Make().String(),
		Type:       typ,
		OccurredAt: time.Now().UTC(),
		Source:     source,
		Data:       raw,
	}, nil
}

func (e Event) Validate() error {
	switch {
	case e.ID == "":
		return fmt.Errorf("%w: empty id", ErrInvalidEvent)
	case e.Type == "":
		return fmt.Errorf("%w: empty type", ErrInvalidEvent)
	case e.Source == "":
		return fmt.Errorf("%w: empty source", ErrInvalidEvent)
	case e.OccurredAt.IsZero():
		return fmt.Errorf("%w: zero occurred_at", ErrInvalidEvent)
	case len(e.Data) == 0:
		return fmt.Errorf("%w: empty data", ErrInvalidEvent)
	}

	return nil
}

// DataOf decodes the event payload into T. It does not check that T matches
// e.Type: decoding a payload into the wrong type yields zero fields rather
// than an error, so callers are expected to switch on Type first.
func DataOf[T any](e Event) (T, error) {
	var out T
	if err := json.Unmarshal(e.Data, &out); err != nil {
		var zeroValue T
		return zeroValue, fmt.Errorf("decode %s payload: %w", e.Type, err)
	}

	return out, nil
}
