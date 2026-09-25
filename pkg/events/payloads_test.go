package events

import (
	"encoding/json"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func loadData(t *testing.T, name string) []byte {
	t.Helper()

	data, err := os.ReadFile(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("read data %s: %v", name, err)
	}

	return data
}

func TestModDiscovered(t *testing.T) {
	var ev Event
	if err := json.Unmarshal(loadData(t, "mod_discovered.json"), &ev); err != nil {
		t.Fatalf("unmarshal envelope: %v", err)
	}

	if err := ev.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	if ev.Type != TypeModDiscovered {
		t.Fatalf("type = %q, want %q", ev.Type, TypeModDiscovered)
	}

	if ev.Source != SourceTracker {
		t.Errorf("source = %q, want %q", ev.Source, SourceTracker)
	}

	wantOccurredAt := time.Date(2026, 9, 16, 12, 34, 56, 789_000_000, time.UTC)
	if !ev.OccurredAt.Equal(wantOccurredAt) {
		t.Errorf("occurred_at = %s, want %s", ev.OccurredAt, wantOccurredAt)
	}

	got, err := DataOf[ModDiscovered](ev)
	if err != nil {
		t.Fatalf("data of: %v", err)
	}

	want := ModDiscovered{
		ModID:           699260,
		CategoryID:      5568,
		Name:            "bhop_ln_portal",
		ProfileURL:      "https://gamebanana.com/mods/699260",
		DateAdded:       1785528621,
		MDate:           1785614900,
		IsUpdate:        true,
		Author:          "qwertybox",
		AuthorURL:       "https://gamebanana.com/members/4609031",
		AuthorAvatarURL: "https://images.gamebanana.com/static/img/defaults/avatar.gif",
		GameName:        "Counter-Strike: Source",
		GameURL:         "https://gamebanana.com/games/2",
		GameIconURL:     "https://images.gamebanana.com/img/ico/games/css_icon.png",
		RootCategory:    "Maps",
		RootCategoryURL: "https://gamebanana.com/mods/cats/5535",
		SubCategory:     "Bunny Hop",
		SubCategoryURL:  "https://gamebanana.com/mods/cats/5568",
		PreviewURL:      "https://images.gamebanana.com/img/ss/mods/6a6d00f093f49.jpg",
		HasDetail:       true,
		Description:     "Remake of the classic portal-themed bhop map.",
		NSFW:            false,
		Version:         "1.1.0",
		UpdatesCount:    2,
		Updates: []ModUpdate{
			{
				Version:   "1.1.0",
				DateAdded: 1785614880,
				Changes: []ModChange{
					{Category: "Bugfix", Text: "fixed a skip on the last stage"},
					{Category: "Improvement", Text: "better lighting in the tunnel"},
				},
			},
		},
		Files: []ModFile{
			{
				ID:          "1160715",
				Name:        "bhop_ln_portal_110.zip",
				Size:        258320957,
				DownloadURL: "https://gamebanana.com/dl/1160715",
				MD5:         "f07cbbdf512843cd9d8bba3e97664e84",
				DateAdded:   1785614880,
				AvResult:    "clean",
			},
		},
	}

	rawRecord, rawDetail := got.Record, got.Detail
	got.Record, got.Detail = nil, nil

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("payload mismatch (-want +got):\n%s", diff)
	}

	var record struct {
		ID int `json:"_idRow"`
	}
	if err := json.Unmarshal(rawRecord, &record); err != nil {
		t.Fatalf("unmarshal raw record: %v", err)
	}
	if record.ID != want.ModID {
		t.Errorf("raw record id = %d, want %d", record.ID, want.ModID)
	}

	if len(rawDetail) == 0 {
		t.Error("raw detail is empty")
	}
}

func TestModDiscoveredWithoutDetail(t *testing.T) {
	payload := ModDiscovered{
		ModID:      1,
		CategoryID: 5568,
		Name:       "bhop_ln_portal",
		MDate:      100,
		Record:     json.RawMessage(`{"_idRow":1}`),
	}

	ev, err := New(TypeModDiscovered, SourceTracker, payload)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	if err = ev.Validate(); err != nil {
		t.Fatalf("validate: %v", err)
	}

	var fields map[string]any
	if err = json.Unmarshal(ev.Data, &fields); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}

	if _, ok := fields["detail"]; ok {
		t.Error("detail is present, want it omitted when there is none")
	}

	got, err := DataOf[ModDiscovered](ev)
	if err != nil {
		t.Fatalf("data of: %v", err)
	}

	if got.HasDetail {
		t.Error("has_detail = true, want false")
	}

	if diff := cmp.Diff(payload, got); diff != "" {
		t.Errorf("payload mismatch (-want +got):\n%s", diff)
	}
}

func TestErrorEventRoundTrip(t *testing.T) {
	payload := ErrorEvent{
		Service: SourceTracker,
		Stage:   "fetch",
		Message: "unexpected status 503",
	}

	ev, err := New(ErrorType(SourceTracker), SourceTracker, payload)
	if err != nil {
		t.Fatalf("new: %v", err)
	}

	var fields map[string]any
	if err = json.Unmarshal(ev.Data, &fields); err != nil {
		t.Fatalf("unmarshal data: %v", err)
	}

	if _, ok := fields["mod_id"]; ok {
		t.Error("mod_id is present, want it omitted when unset")
	}

	got, err := DataOf[ErrorEvent](ev)
	if err != nil {
		t.Fatalf("data of: %v", err)
	}

	if diff := cmp.Diff(payload, got); diff != "" {
		t.Errorf("payload mismatch (-want +got):\n%s", diff)
	}
}
