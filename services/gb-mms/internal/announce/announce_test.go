package announce

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"testing"

	"github.com/happydez/gb-tracker/pkg/events"

	"github.com/happydez/gb-mms/internal/pipeline"
)

func sampleResult() pipeline.Result {
	return pipeline.Result{
		Mod: events.ModDiscovered{
			ModID:           718166,
			Name:            "bhop_scopex",
			ProfileURL:      "https://gamebanana.com/mods/718166",
			MDate:           1789673474,
			Author:          "Scopex91",
			AuthorURL:       "https://gamebanana.com/members/4959758",
			GameName:        "Counter-Strike: Source",
			GameURL:         "https://gamebanana.com/games/2",
			GameIconURL:     "https://images.gamebanana.com/css_icon.png",
			RootCategory:    "Maps",
			RootCategoryURL: "https://gamebanana.com/mods/cats/5535",
			SubCategory:     "Bunny Hop",
			SubCategoryURL:  "https://gamebanana.com/mods/cats/5568",
			PreviewURL:      "https://images.gamebanana.com/a.jpg",
		},
		MapCount:      1,
		BZ2Size:       54<<20 + 419430,
		BSPSize:       174 << 20,
		UploadEnabled: true,
		Uploaded:      true,
		RCONEnabled:   true,
		RCONDone:      true,
	}
}

func fieldValue(t *testing.T, n events.Notification, name string) string {
	t.Helper()

	for _, f := range n.Fields {
		if f.Name == name {
			return f.Value
		}
	}

	t.Fatalf("no field named %q", name)

	return ""
}

func TestBuildTotals(t *testing.T) {
	got := Build(DefaultConfig(), sampleResult()).Description

	for _, want := range []string{
		"**Total maps:** 1",
		"**Total (.bsp.bz2) size:** 54.4 MB",
		"**Total (.bsp) size:** 174.0 MB",
		"✅ Uploaded to server storage",
		"✅ RCON Commands",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("description is missing %q:\n%s", want, got)
		}
	}
}

func TestFailureBecomesAnErrorEvent(t *testing.T) {
	r := sampleResult()
	r.Uploaded = false
	r.RCONDone = false
	r.Stage = "rcon"
	r.Err = errors.New("connection refused")

	pub := &capturingPublisher{}

	if err := New(DefaultConfig(), pub).Announce(t.Context(), r); err != nil {
		t.Fatal(err)
	}

	if !events.IsErrorType(pub.ev.Type) {
		t.Fatalf("type = %q, want an errors.* subject", pub.ev.Type)
	}

	got, err := events.DataOf[events.ErrorEvent](pub.ev)
	if err != nil {
		t.Fatal(err)
	}

	if got.Stage != "rcon" {
		t.Errorf("stage = %q, want rcon", got.Stage)
	}

	if got.ModID != r.Mod.ModID {
		t.Errorf("mod_id = %d, want %d", got.ModID, r.Mod.ModID)
	}

	if !strings.Contains(got.Message, "connection refused") {
		t.Errorf("message = %q, want the cause in it", got.Message)
	}
}

func TestSuccessBecomesANotification(t *testing.T) {
	pub := &capturingPublisher{}

	if err := New(DefaultConfig(), pub).Announce(t.Context(), sampleResult()); err != nil {
		t.Fatal(err)
	}

	if pub.ev.Type != events.TypeNotificationSend {
		t.Fatalf("type = %q, want %q", pub.ev.Type, events.TypeNotificationSend)
	}
}

func TestBuildFieldLayout(t *testing.T) {
	n := Build(DefaultConfig(), sampleResult())

	if len(n.Fields) != 6 {
		t.Fatalf("got %d fields, want 6 including both spacers", len(n.Fields))
	}

	for _, i := range []int{2, 5} {
		if !n.Fields[i].Spacer {
			t.Errorf("field %d is not the spacer closing its row", i)
		}
	}

	if v := fieldValue(t, n, "Category"); !strings.Contains(v, "Maps") || !strings.Contains(v, "Bunny Hop") {
		t.Errorf("category = %q", v)
	}
}

func TestBuildNSFWCover(t *testing.T) {
	r := sampleResult()
	r.Mod.NSFW = true

	if got := Build(DefaultConfig(), r).ImageURL; got != DefaultCoverURL {
		t.Errorf("image_url = %q, want the cover", got)
	}

	cfg := DefaultConfig()
	cfg.CoverNSFW = false

	if got := Build(cfg, r).ImageURL; got != r.Mod.PreviewURL {
		t.Errorf("image_url = %q, want the real preview", got)
	}
}

func TestBuildPrefersTheStudio(t *testing.T) {
	r := sampleResult()
	r.Mod.Studio = "*bihop"
	r.Mod.StudioURL = "https://gamebanana.com/studios/35083"

	if got := Build(DefaultConfig(), r).Author.Name; got != "*bihop" {
		t.Errorf("author = %q", got)
	}
}

func TestConfigValidate(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Errorf("the default config is invalid: %v", err)
	}

	if err := (Config{}).Validate(); err == nil {
		t.Error("expected an error for a config with no channel")
	}

	cfg := DefaultConfig()
	cfg.CoverURL = ""

	if err := cfg.Validate(); err == nil {
		t.Error("expected an error for covering with no cover url")
	}
}

func TestHumanSize(t *testing.T) {
	tests := []struct {
		in   int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1024, "1.0 KB"},
		{57 << 20, "57.0 MB"},
		{5 << 30, "5.0 GB"},
	}

	for _, tt := range tests {
		if got := humanSize(tt.in); got != tt.want {
			t.Errorf("humanSize(%d) = %q, want %q", tt.in, got, tt.want)
		}
	}
}

func TestBuildOmitsDisabledSteps(t *testing.T) {
	r := sampleResult()
	r.UploadEnabled = false
	r.Uploaded = false
	r.RCONEnabled = false
	r.RCONDone = false

	got := Build(DefaultConfig(), r).Description

	for _, unwanted := range []string{"Uploaded to server storage", "RCON Commands", "✅", "❌"} {
		if strings.Contains(got, unwanted) {
			t.Errorf("description mentions %q with the step disabled:\n%s", unwanted, got)
		}
	}

	if !strings.Contains(got, "**Total maps:** 1") {
		t.Errorf("totals missing:\n%s", got)
	}
}

func TestBuildOmitsOnlyTheDisabledStep(t *testing.T) {
	r := sampleResult()
	r.RCONEnabled = false
	r.RCONDone = false

	got := Build(DefaultConfig(), r).Description

	if !strings.Contains(got, "✅ Uploaded to server storage") {
		t.Errorf("upload line missing:\n%s", got)
	}

	if strings.Contains(got, "RCON Commands") {
		t.Errorf("rcon line present with rcon disabled:\n%s", got)
	}
}

type capturingPublisher struct {
	ev events.Event
}

func (p *capturingPublisher) Publish(_ context.Context, ev events.Event) error {
	p.ev = ev

	return nil
}

func TestStatusFollowsTheModsHistory(t *testing.T) {
	tests := []struct {
		name       string
		isUpdate   bool
		updates    int
		wantStatus string
		wantVerb   string
	}{
		{name: "first upload, never revised", wantStatus: "🟢 NEW", wantVerb: "New mod"},
		{
			name:       "first release, revised before it was published",
			updates:    3,
			wantStatus: "🟢 NEW",
			wantVerb:   "New mod",
		},
		{
			name:       "changed after we announced it",
			isUpdate:   true,
			updates:    4,
			wantStatus: "🟡 UPDATED",
			wantVerb:   "Updated mod",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			r := sampleResult()
			r.Mod.IsUpdate = tt.isUpdate
			r.Mod.UpdatesCount = tt.updates

			n := Build(DefaultConfig(), r)

			if got := fieldValue(t, n, "Status"); got != tt.wantStatus {
				t.Errorf("status = %q, want %q", got, tt.wantStatus)
			}

			if !strings.HasPrefix(n.Content, tt.wantVerb) {
				t.Errorf("content = %q, want it to start with %q", n.Content, tt.wantVerb)
			}
		})
	}
}

func TestBuildRendersTheChangelog(t *testing.T) {
	r := sampleResult()
	r.Mod.UpdatesCount = 3
	r.Mod.Updates = []events.ModUpdate{{
		Version: "v_fix",
		Title:   "_fix",
		Changes: []events.ModChange{
			{Category: "Removal", Text: "removed doubleboost in long white stage"},
			{Category: "Bugfix", Text: "2 small skips fixed"},
			{Text: "no category here"},
		},
	}}

	got := Build(DefaultConfig(), r).Description

	for _, want := range []string{
		"**Updates (3)** - v_fix: _fix",
		"• [Removal] removed doubleboost in long white stage",
		"• [Bugfix] 2 small skips fixed",
		"• no category here",
	} {
		if !strings.Contains(got, want) {
			t.Errorf("description is missing %q:\n%s", want, got)
		}
	}
}

func TestBuildCapsTheChangelog(t *testing.T) {
	r := sampleResult()
	r.Mod.UpdatesCount = 1
	r.Mod.Updates = []events.ModUpdate{{Version: "1.1"}}

	for i := range maxChanges + 4 {
		r.Mod.Updates[0].Changes = append(r.Mod.Updates[0].Changes, events.ModChange{Text: fmt.Sprint("change ", i)})
	}

	got := Build(DefaultConfig(), r).Description

	if strings.Count(got, "• ") != maxChanges+1 {
		t.Errorf("got %d bullets, want %d plus the overflow line:\n%s", strings.Count(got, "• "), maxChanges, got)
	}

	if !strings.Contains(got, "and 4 more") {
		t.Errorf("the overflow is not reported:\n%s", got)
	}
}

func TestBuildChangelogFallsBackToText(t *testing.T) {
	r := sampleResult()
	r.Mod.UpdatesCount = 1
	r.Mod.Updates = []events.ModUpdate{{Version: "1.1", Text: "rebuilt lighting"}}

	got := Build(DefaultConfig(), r).Description

	if !strings.Contains(got, "**Updates (1)** - 1.1") || !strings.Contains(got, "rebuilt lighting") {
		t.Errorf("description:\n%s", got)
	}
}

func TestBuildOmitsTheChangelogWhenThereIsNone(t *testing.T) {
	if got := Build(DefaultConfig(), sampleResult()).Description; strings.Contains(got, "Updates") {
		t.Errorf("description mentions updates with none present:\n%s", got)
	}
}
