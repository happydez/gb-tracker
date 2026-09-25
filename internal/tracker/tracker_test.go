package tracker

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"log/slog"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/happydez/gb-tracker/internal/config"
	"github.com/happydez/gb-tracker/internal/gb"
	"github.com/happydez/gb-tracker/internal/storage"
	"github.com/happydez/gb-tracker/pkg/events"
)

type stubAPI struct {
	pages map[int][]gb.Record
	mods  map[int]*gb.Mod

	recordsErr error
	modErr     error

	mu        sync.Mutex
	pageCalls []int
	modCalls  []int
}

func (a *stubAPI) FetchRecords(_ context.Context, params gb.RecordParams) ([]gb.Record, error) {
	a.mu.Lock()
	a.pageCalls = append(a.pageCalls, params.Page)
	a.mu.Unlock()

	if a.recordsErr != nil {
		return nil, a.recordsErr
	}

	return a.pages[params.Page], nil
}

func (a *stubAPI) FetchMod(_ context.Context, modID int) (*gb.Mod, error) {
	a.mu.Lock()
	a.modCalls = append(a.modCalls, modID)
	a.mu.Unlock()

	if a.modErr != nil {
		return nil, a.modErr
	}

	mod, ok := a.mods[modID]
	if !ok {
		return nil, fmt.Errorf("mod %d not found", modID)
	}

	return mod, nil
}

type stubStore struct {
	mu    sync.Mutex
	saved map[int]storage.Mod

	saveErr error
}

func newStubStore() *stubStore {
	return &stubStore{saved: map[int]storage.Mod{}}
}

func (s *stubStore) Seen(_ context.Context, modID int) (int64, bool, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	m, ok := s.saved[modID]
	if !ok {
		return 0, false, nil
	}

	return m.MDate, true, nil
}

func (s *stubStore) Save(_ context.Context, m storage.Mod) error {
	if s.saveErr != nil {
		return s.saveErr
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.saved[m.ModID] = m

	return nil
}

type stubPublisher struct {
	mu        sync.Mutex
	published []events.Event

	err error
}

func (p *stubPublisher) Publish(_ context.Context, ev events.Event) error {
	if p.err != nil {
		return p.err
	}

	p.mu.Lock()
	defer p.mu.Unlock()
	p.published = append(p.published, ev)

	return nil
}

func (p *stubPublisher) payloads(t *testing.T) []events.ModDiscovered {
	t.Helper()

	p.mu.Lock()
	defer p.mu.Unlock()

	out := make([]events.ModDiscovered, 0, len(p.published))
	for _, ev := range p.published {
		got, err := events.DataOf[events.ModDiscovered](ev)
		if err != nil {
			t.Fatalf("decode payload: %v", err)
		}

		out = append(out, got)
	}

	return out
}

func record(id int, name string, added, modified int64) gb.Record {
	rec := gb.Record{
		ID:           id,
		Name:         name,
		ProfileURL:   fmt.Sprintf("https://gamebanana.com/mods/%d", id),
		DateAdded:    gb.Timestamp(time.Unix(added, 0).UTC()),
		DateModified: gb.Timestamp(time.Unix(modified, 0).UTC()),
		Submitter:    gb.Submitter{Name: "qwertybox", ProfileURL: "https://gamebanana.com/members/1"},
		Game:         gb.Game{Name: "Counter-Strike: Source"},
		RootCategory: gb.RootCategory{Name: "Maps"},
		SubCategory:  gb.SubCategory{Name: "Bunny Hop"},
		Raw:          json.RawMessage(fmt.Sprintf(`{"_idRow":%d,"_sName":%q}`, id, name)),
	}

	return rec
}

func newTracker(t *testing.T, cfg config.Tracker, api API, store Store, pub Publisher) *Tracker {
	t.Helper()

	return New(cfg, api, store, pub, slog.New(slog.NewTextHandler(io.Discard, nil)))
}

func testConfig(cats ...config.Category) config.Tracker {
	return config.Tracker{
		PollInterval: config.Duration(time.Minute),
		Categories:   cats,
	}
}

func category(id int) config.Category {
	return config.Category{ID: id, Label: "test", PerPage: 5, MaxPages: 3, Sort: "Generic_LatestModified"}
}

func TestAnnouncesNewMods(t *testing.T) {
	api := &stubAPI{pages: map[int][]gb.Record{
		1: {record(2, "b", 200, 200), record(1, "a", 100, 100)},
	}}
	store := newStubStore()
	pub := &stubPublisher{}

	tr := newTracker(t, testConfig(category(5568)), api, store, pub)
	if err := tr.pollCategory(t.Context(), category(5568)); err != nil {
		t.Fatalf("poll: %v", err)
	}

	got := pub.payloads(t)
	if len(got) != 2 {
		t.Fatalf("published %d events, want 2", len(got))
	}

	if got[0].ModID != 1 || got[1].ModID != 2 {
		t.Errorf("announced order = %d, %d; want 1, 2", got[0].ModID, got[1].ModID)
	}

	if got[0].IsUpdate {
		t.Error("is_update = true for a mod seen for the first time")
	}

	if len(store.saved) != 2 {
		t.Errorf("saved %d mods, want 2", len(store.saved))
	}
}

func TestSkipsKnownMods(t *testing.T) {
	api := &stubAPI{pages: map[int][]gb.Record{
		1: {record(1, "a", 100, 100)},
	}}
	store := newStubStore()
	pub := &stubPublisher{}

	tr := newTracker(t, testConfig(category(5568)), api, store, pub)

	for range 2 {
		if err := tr.pollCategory(t.Context(), category(5568)); err != nil {
			t.Fatalf("poll: %v", err)
		}
	}

	if n := len(pub.published); n != 1 {
		t.Errorf("published %d events over two polls, want 1", n)
	}
}

func TestAnnouncesUpdatedMod(t *testing.T) {
	api := &stubAPI{pages: map[int][]gb.Record{
		1: {record(1, "a", 100, 100)},
	}}
	store := newStubStore()
	pub := &stubPublisher{}

	tr := newTracker(t, testConfig(category(5568)), api, store, pub)
	if err := tr.pollCategory(t.Context(), category(5568)); err != nil {
		t.Fatalf("poll: %v", err)
	}

	api.pages[1] = []gb.Record{record(1, "a", 100, 500)}

	if err := tr.pollCategory(t.Context(), category(5568)); err != nil {
		t.Fatalf("second poll: %v", err)
	}

	got := pub.payloads(t)
	if len(got) != 2 {
		t.Fatalf("published %d events, want 2", len(got))
	}

	if got[1].MDate != 500 {
		t.Errorf("mdate = %d, want 500", got[1].MDate)
	}

	if !got[1].IsUpdate {
		t.Error("is_update = false for a mod announced before")
	}

	if pub.published[0].ID == pub.published[1].ID {
		t.Error("both events share an id, want a new one per announcement")
	}
}

func TestTSInitFiltersOldMods(t *testing.T) {
	api := &stubAPI{pages: map[int][]gb.Record{
		1: {record(2, "new", 300, 300), record(1, "old", 100, 100)},
	}}
	store := newStubStore()
	pub := &stubPublisher{}

	cfg := testConfig(category(5568))
	cfg.TSInit = 200

	tr := newTracker(t, cfg, api, store, pub)
	if err := tr.pollCategory(t.Context(), category(5568)); err != nil {
		t.Fatalf("poll: %v", err)
	}

	got := pub.payloads(t)
	if len(got) != 1 {
		t.Fatalf("published %d events, want 1", len(got))
	}

	if got[0].ModID != 2 {
		t.Errorf("announced mod %d, want 2", got[0].ModID)
	}
}

func TestStopsPagingOnAKnownPage(t *testing.T) {
	api := &stubAPI{pages: map[int][]gb.Record{
		1: {record(1, "a", 100, 100)},
		2: {record(2, "b", 90, 90)},
		3: {record(3, "c", 80, 80)},
	}}
	store := newStubStore()
	pub := &stubPublisher{}

	tr := newTracker(t, testConfig(category(5568)), api, store, pub)

	if err := tr.pollCategory(t.Context(), category(5568)); err != nil {
		t.Fatalf("poll: %v", err)
	}

	if diff := cmp.Diff([]int{1, 2, 3}, api.pageCalls); diff != "" {
		t.Errorf("first poll pages (-want +got):\n%s", diff)
	}

	api.pageCalls = nil

	if err := tr.pollCategory(t.Context(), category(5568)); err != nil {
		t.Fatalf("second poll: %v", err)
	}

	if diff := cmp.Diff([]int{1}, api.pageCalls); diff != "" {
		t.Errorf("second poll pages (-want +got):\n%s", diff)
	}
}

func TestDetailsFillThePayload(t *testing.T) {
	api := &stubAPI{
		pages: map[int][]gb.Record{1: {record(1, "a", 100, 100)}},
		mods: map[int]*gb.Mod{1: {
			Description: "a bhop map",
			IsNSFW:      true,
			LatestUpdates: []gb.UpdateLog{
				{Version: "1.1.0"},
			},
			Files: map[string]gb.File{
				"77": {
					ID: "77", File: "a.zip", FileSize: 1024,
					DownloadURL: "https://gamebanana.com/dl/77",
					MD5Checksum: "abc", AvResult: "clean",
					DateAdded: gb.Timestamp(time.Unix(150, 0).UTC()),
				},
			},
			Raw: json.RawMessage(`{"name":"a"}`),
		}},
	}
	store := newStubStore()
	pub := &stubPublisher{}

	cfg := testConfig(category(5568))
	cfg.FetchDetails = true

	tr := newTracker(t, cfg, api, store, pub)
	if err := tr.pollCategory(t.Context(), category(5568)); err != nil {
		t.Fatalf("poll: %v", err)
	}

	got := pub.payloads(t)[0]

	if !got.HasDetail {
		t.Fatal("has_detail = false, want true")
	}

	want := []events.ModFile{{
		ID: "77", Name: "a.zip", Size: 1024,
		DownloadURL: "https://gamebanana.com/dl/77",
		MD5:         "abc", DateAdded: 150, AvResult: "clean",
	}}

	if diff := cmp.Diff(want, got.Files); diff != "" {
		t.Errorf("files (-want +got):\n%s", diff)
	}

	if got.Description != "a bhop map" || !got.NSFW || got.Version != "1.1.0" {
		t.Errorf("detail fields not carried over: %+v", got)
	}

	if len(store.saved[1].DetailJSON) == 0 {
		t.Error("detail_json not stored")
	}
}

func TestDetailFailureStillAnnounces(t *testing.T) {
	api := &stubAPI{
		pages:  map[int][]gb.Record{1: {record(1, "a", 100, 100)}},
		modErr: errors.New("404 not found"),
	}
	store := newStubStore()
	pub := &stubPublisher{}

	cfg := testConfig(category(5568))
	cfg.FetchDetails = true

	tr := newTracker(t, cfg, api, store, pub)
	if err := tr.pollCategory(t.Context(), category(5568)); err != nil {
		t.Fatalf("poll: %v", err)
	}

	got := pub.payloads(t)
	if len(got) != 1 {
		t.Fatalf("published %d events, want 1", len(got))
	}

	if got[0].HasDetail {
		t.Error("has_detail = true after the detail request failed")
	}

	if _, ok := store.saved[1]; !ok {
		t.Error("mod not saved, want it recorded as announced")
	}
}

func TestPublishFailureLeavesModUnsaved(t *testing.T) {
	api := &stubAPI{pages: map[int][]gb.Record{1: {record(1, "a", 100, 100)}}}
	store := newStubStore()
	pub := &stubPublisher{err: errors.New("nats is down")}

	tr := newTracker(t, testConfig(category(5568)), api, store, pub)
	if err := tr.pollCategory(t.Context(), category(5568)); err != nil {
		t.Fatalf("poll: %v", err)
	}

	if len(store.saved) != 0 {
		t.Fatal("mod saved even though publishing failed")
	}

	pub.err = nil

	if err := tr.pollCategory(t.Context(), category(5568)); err != nil {
		t.Fatalf("second poll: %v", err)
	}

	if len(pub.published) != 1 {
		t.Errorf("published %d events, want the retry to succeed once", len(pub.published))
	}
}

func TestFetchErrorFailsTheCategory(t *testing.T) {
	api := &stubAPI{recordsErr: errors.New("503")}

	tr := newTracker(t, testConfig(category(5568)), api, newStubStore(), &stubPublisher{})

	if err := tr.pollCategory(t.Context(), category(5568)); err == nil {
		t.Fatal("poll: expected an error")
	}
}

func TestOverlappingCategoriesAnnounceOnce(t *testing.T) {
	api := &stubAPI{pages: map[int][]gb.Record{1: {record(1, "a", 100, 100)}}}
	store := newStubStore()
	pub := &stubPublisher{}

	cfg := testConfig(category(5535), category(5568))

	tr := newTracker(t, cfg, api, store, pub)
	tr.tick(t.Context())

	if n := len(pub.published); n != 1 {
		t.Errorf("published %d events for one mod in two categories, want 1", n)
	}
}

func TestRunStopsOnContextCancel(t *testing.T) {
	api := &stubAPI{pages: map[int][]gb.Record{1: {record(1, "a", 100, 100)}}}

	cfg := testConfig(category(5568))
	cfg.PollInterval = config.Duration(time.Hour)

	tr := newTracker(t, cfg, api, newStubStore(), &stubPublisher{})

	ctx, cancel := context.WithCancel(t.Context())
	done := make(chan struct{})

	go func() {
		tr.Run(ctx)
		close(done)
	}()

	cancel()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("Run did not return after the context was cancelled")
	}
}

func TestLogPublisher(t *testing.T) {
	ev, err := events.New(events.TypeModDiscovered, events.SourceTracker, events.ModDiscovered{ModID: 1})
	if err != nil {
		t.Fatalf("new event: %v", err)
	}

	p := LogPublisher{Log: slog.New(slog.NewTextHandler(io.Discard, nil))}
	if err := p.Publish(t.Context(), ev); err != nil {
		t.Fatalf("publish: %v", err)
	}
}

func (p *stubPublisher) errorEvents(t *testing.T) []events.ErrorEvent {
	t.Helper()

	p.mu.Lock()
	defer p.mu.Unlock()

	var out []events.ErrorEvent
	for _, ev := range p.published {
		if !events.IsErrorType(ev.Type) {
			continue
		}

		got, err := events.DataOf[events.ErrorEvent](ev)
		if err != nil {
			t.Fatalf("decode error event: %v", err)
		}

		out = append(out, got)
	}

	return out
}

func TestPollFailurePublishesAnError(t *testing.T) {
	api := &stubAPI{recordsErr: errors.New("503 from gamebanana")}
	pub := &stubPublisher{}

	tr := newTracker(t, testConfig(category(5568)), api, newStubStore(), pub)
	tr.tick(t.Context())

	got := pub.errorEvents(t)
	if len(got) != 1 {
		t.Fatalf("published %d error events, want 1", len(got))
	}

	if got[0].Stage != "poll" {
		t.Errorf("stage = %q, want poll", got[0].Stage)
	}

	if got[0].Service != events.SourceTracker {
		t.Errorf("service = %q, want %q", got[0].Service, events.SourceTracker)
	}

	if !strings.Contains(got[0].Message, "503 from gamebanana") {
		t.Errorf("message = %q, want it to carry the cause", got[0].Message)
	}

	if want := events.ErrorType(events.SourceTracker); pub.published[0].Type != want {
		t.Errorf("type = %q, want %q", pub.published[0].Type, want)
	}
}

func TestAnnounceFailurePublishesAnError(t *testing.T) {
	api := &stubAPI{pages: map[int][]gb.Record{1: {record(42, "a", 100, 100)}}}
	store := newStubStore()
	store.saveErr = errors.New("disk is full")
	pub := &stubPublisher{}

	tr := newTracker(t, testConfig(category(5568)), api, store, pub)
	if err := tr.pollCategory(t.Context(), category(5568)); err != nil {
		t.Fatalf("poll: %v", err)
	}

	got := pub.errorEvents(t)
	if len(got) != 1 {
		t.Fatalf("published %d error events, want 1", len(got))
	}

	if got[0].Stage != "announce" {
		t.Errorf("stage = %q, want announce", got[0].Stage)
	}

	if got[0].ModID != 42 {
		t.Errorf("mod_id = %d, want 42", got[0].ModID)
	}
}

func TestPublisherFailureDoesNotLoop(t *testing.T) {
	api := &stubAPI{pages: map[int][]gb.Record{1: {record(1, "a", 100, 100)}}}
	pub := &stubPublisher{err: errors.New("nats is down")}

	tr := newTracker(t, testConfig(category(5568)), api, newStubStore(), pub)

	done := make(chan struct{})
	go func() {
		_ = tr.pollCategory(t.Context(), category(5568))
		close(done)
	}()

	select {
	case <-done:
	case <-time.After(2 * time.Second):
		t.Fatal("pollCategory did not return: reporting a publish failure is looping")
	}
}

func TestSuccessPublishesNoErrors(t *testing.T) {
	api := &stubAPI{pages: map[int][]gb.Record{1: {record(1, "a", 100, 100)}}}
	pub := &stubPublisher{}

	tr := newTracker(t, testConfig(category(5568)), api, newStubStore(), pub)
	if err := tr.pollCategory(t.Context(), category(5568)); err != nil {
		t.Fatalf("poll: %v", err)
	}

	if got := pub.errorEvents(t); len(got) != 0 {
		t.Errorf("published %d error events on a clean run, want none", len(got))
	}
}
