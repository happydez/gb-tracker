package events

import (
	"encoding/json"
	"errors"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func samplePayload() ModDiscovered {
	return ModDiscovered{
		ModID:           1,
		CategoryID:      5568,
		Name:            "bhop_ln_portal",
		ProfileURL:      "https://gamebanana.com/mods/1",
		DateAdded:       1785528621,
		MDate:           1785614900,
		IsUpdate:        true,
		Author:          "qwertybox",
		AuthorURL:       "https://gamebanana.com/members/1",
		AuthorAvatarURL: "https://images.gamebanana.com/static/img/defaults/avatar.gif",
		GameName:        "Counter-Strike: Source",
		RootCategory:    "Maps",
		SubCategory:     "Bunny Hop",
		PreviewURL:      "https://images.gamebanana.com/img/ss/mods/1.jpg",
		HasDetail:       true,
		Description:     "test",
		Version:         "1.1.0",
		Files: []ModFile{
			{ID: "1", Name: "bhop_ln_portal.zip", Size: 100, DownloadURL: "https://gamebanana.com/dl/1", MD5: "d41d8cd98f00b204e9800998ecf8427e", AvResult: "clean"},
		},
		Record: json.RawMessage(`{"_idRow":1}`),
		Detail: json.RawMessage(`{"name":"bhop_ln_portal"}`),
	}
}

func TestNew(t *testing.T) {
	ev, err := New(TypeModDiscovered, SourceTracker, samplePayload())
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if err := ev.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	if len(ev.ID) != 26 {
		t.Errorf("id = %q, want a 26-character ULID", ev.ID)
	}

	if ev.Type != TypeModDiscovered {
		t.Errorf("type = %q, want %q", ev.Type, TypeModDiscovered)
	}

	if ev.Source != SourceTracker {
		t.Errorf("source = %q, want %q", ev.Source, SourceTracker)
	}

	if _, offset := ev.OccurredAt.Zone(); offset != 0 {
		t.Errorf("occurred_at zone offset = %d, want 0 (UTC)", offset)
	}
}

func TestNewGeneratesUniqueIDs(t *testing.T) {
	first, err := New(TypeModDiscovered, SourceTracker, samplePayload())
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	second, err := New(TypeModDiscovered, SourceTracker, samplePayload())
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if first.ID == second.ID {
		t.Fatalf("both events got id %q, want distinct ids", first.ID)
	}

	if first.ID > second.ID {
		t.Errorf("id %q sorts after %q, want chronological order", first.ID, second.ID)
	}
}

func TestNewRejectsUnmarshalablePayload(t *testing.T) {
	if _, err := New(TypeModDiscovered, SourceTracker, make(chan int)); err == nil {
		t.Fatal("new: expected an error for an unmarshalable payload")
	}
}

func TestEventRoundTrip(t *testing.T) {
	want := samplePayload()

	ev, err := New(TypeModDiscovered, SourceTracker, want)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	raw, err := json.Marshal(ev)
	if err != nil {
		t.Fatalf("marshal: %v", err)
	}

	var decoded Event
	if err = json.Unmarshal(raw, &decoded); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if diff := cmp.Diff(ev, decoded); diff != "" {
		t.Errorf("envelope mismatch (-want +got):\n%s", diff)
	}

	got, err := DataOf[ModDiscovered](decoded)
	if err != nil {
		t.Fatalf("data of: %v", err)
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("payload mismatch (-want +got):\n%s", diff)
	}
}

func TestEventValidate(t *testing.T) {
	valid := func() Event {
		ev, err := New(TypeModDiscovered, SourceTracker, samplePayload())
		if err != nil {
			t.Fatalf("new: %v", err)
		}

		return ev
	}

	tests := []struct {
		name    string
		mutate  func(*Event)
		wantErr bool
	}{
		{name: "valid", mutate: func(*Event) {}},
		{name: "empty id", mutate: func(e *Event) { e.ID = "" }, wantErr: true},
		{name: "empty type", mutate: func(e *Event) { e.Type = "" }, wantErr: true},
		{name: "empty source", mutate: func(e *Event) { e.Source = "" }, wantErr: true},
		{name: "zero occurred at", mutate: func(e *Event) { e.OccurredAt = time.Time{} }, wantErr: true},
		{name: "empty data", mutate: func(e *Event) { e.Data = nil }, wantErr: true},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			ev := valid()
			tt.mutate(&ev)

			err := ev.Validate()
			if tt.wantErr {
				if err == nil {
					t.Fatal("validate: expected an error")
				}

				if !errors.Is(err, ErrInvalidEvent) {
					t.Errorf("validate: error %v does not match ErrInvalidEvent", err)
				}

				return
			}

			if err != nil {
				t.Fatalf("validate: %v", err)
			}
		})
	}
}

func TestUnknownEventTypeDecodes(t *testing.T) {
	raw := []byte(`{
		"id": "01JBQX7K9M2NPVWXYZ3ABCDEFG",
		"type": "mods.rehosted",
		"occurred_at": "2026-09-16T12:34:56.789Z",
		"source": "some-future-service",
		"data": {"whatever": [1, 2, 3]}
	}`)

	var ev Event
	if err := json.Unmarshal(raw, &ev); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if err := ev.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	if ev.Type != "mods.rehosted" {
		t.Errorf("type = %q, want %q", ev.Type, "mods.rehosted")
	}
}

func TestDataOfRejectsMismatchedPayload(t *testing.T) {
	ev, err := New(TypeModDiscovered, SourceTracker, samplePayload())
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	ev.Data = json.RawMessage(`"a string, not an object"`)

	got, err := DataOf[ModDiscovered](ev)
	if err == nil {
		t.Fatal("data of: expected an error")
	}

	if diff := cmp.Diff(ModDiscovered{}, got); diff != "" {
		t.Errorf("got a non-zero value on error (-want +got):\n%s", diff)
	}
}
