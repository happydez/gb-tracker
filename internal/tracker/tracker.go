// Package tracker polls the configured GameBanana categories and publishes an
// event for every mod it has not announced before.
package tracker

import (
	"context"
	"encoding/json"
	"fmt"
	"log/slog"
	"runtime/debug"
	"sync"
	"sync/atomic"
	"time"

	"github.com/happydez/gb-tracker/internal/config"
	"github.com/happydez/gb-tracker/internal/gb"
	"github.com/happydez/gb-tracker/internal/storage"
	"github.com/happydez/gb-tracker/pkg/events"
)

// API is the slice of the GameBanana client this package uses. Declaring it
// here rather than in gb is what lets a test swap in a stub.
type API interface {
	FetchRecords(ctx context.Context, params gb.RecordParams) ([]gb.Record, error)
	FetchMod(ctx context.Context, modID int) (*gb.Mod, error)
}

// Store is the slice of storage this package uses.
type Store interface {
	Seen(ctx context.Context, modID int) (int64, bool, error)
	Save(ctx context.Context, m storage.Mod) error
}

// Publisher hands a finished event to whatever carries it. The tracker never
// learns which broker that is, so swapping NATS for anything else changes
// nothing here.
type Publisher interface {
	Publish(ctx context.Context, ev events.Event) error
}

type Tracker struct {
	cfg   config.Tracker
	api   API
	store Store
	pub   Publisher
	log   *slog.Logger

	// announcing serialises the check-publish-save sequence across categories.
	// Nested categories list the same mod, and without this both goroutines
	// would see it as unknown before either had saved it and announce it
	// twice. The primary key keeps the database clean but cannot take back a
	// published event.
	announcing sync.Mutex
}

func New(cfg config.Tracker, api API, store Store, pub Publisher, log *slog.Logger) *Tracker {
	return &Tracker{cfg: cfg, api: api, store: store, pub: pub, log: log}
}

// Run polls until ctx is cancelled. It never returns on an error: a failed
// tick is logged, and the next one proceeds normally.
func (t *Tracker) Run(ctx context.Context) {
	t.log.Info("tracker started",
		"categories", len(t.cfg.Categories),
		"poll_interval", t.cfg.PollInterval.Unwrap(),
		"fetch_details", t.cfg.FetchDetails,
	)

	var running atomic.Bool
	tick := func() {
		// A tick that outlasts the interval must not be joined by the next
		// one, or two goroutines would poll the same category at once.
		if !running.CompareAndSwap(false, true) {
			t.log.Warn("skipping tick, the previous one is still running")

			return
		}
		defer running.Store(false)

		t.tick(ctx)
	}

	tick()

	ticker := time.NewTicker(t.cfg.PollInterval.Unwrap())
	defer ticker.Stop()

	for {
		select {
		case <-ctx.Done():
			t.log.Info("tracker stopped")

			return
		case <-ticker.C:
			tick()
		}
	}
}

// tick polls every category concurrently. Categories share nothing but the
// database, which deduplicates the overlap between a parent and its child.
func (t *Tracker) tick(ctx context.Context) {
	var wg sync.WaitGroup

	for _, cat := range t.cfg.Categories {
		wg.Go(func() {
			defer func() {
				if r := recover(); r != nil {
					t.log.Error("category panicked", "category_id", cat.ID, "panic", r, "stack", string(debug.Stack()))
					t.report(ctx, "panic", 0, fmt.Errorf("category %d panicked: %v", cat.ID, r))
				}
			}()

			if err := t.pollCategory(ctx, cat); err != nil && ctx.Err() == nil {
				t.log.Error("category poll failed", "category_id", cat.ID, "label", cat.Label, "error", err)
				t.report(ctx, "poll", 0, fmt.Errorf("category %d (%s): %w", cat.ID, cat.Label, err))
			}
		})
	}

	wg.Wait()
}

func (t *Tracker) pollCategory(ctx context.Context, cat config.Category) error {
	pending, err := t.collect(ctx, cat)
	if err != nil {
		return err
	}

	if len(pending) == 0 {
		return nil
	}

	t.log.Info("found mods to announce", "category_id", cat.ID, "label", cat.Label, "count", len(pending))

	for _, p := range pending {
		if err := ctx.Err(); err != nil {
			return err
		}

		if err := t.announce(ctx, cat, p); err != nil {
			t.log.Error("announce failed",
				"mod_id", p.record.ID, "name", p.record.Name, "error", err)
			t.report(ctx, "announce", p.record.ID, err)
		}
	}

	return nil
}

// candidate is a record that has to be announced and whether the mod was
// already known under an older mdate.
type candidate struct {
	record     gb.Record
	seenBefore bool
}

// collect pages through the listing and returns the mods due for an
// announcement, oldest first.
func (t *Tracker) collect(ctx context.Context, cat config.Category) ([]candidate, error) {
	var out []candidate

	for page := 1; page <= cat.MaxPages; page++ {
		records, err := t.api.FetchRecords(ctx, gb.RecordParams{
			PerPage:  cat.PerPage,
			Sort:     cat.Sort,
			Category: cat.ID,
			Page:     page,
		})
		if err != nil {
			return nil, fmt.Errorf("fetch page %d: %w", page, err)
		}

		if len(records) == 0 {
			break
		}

		fresh := 0

		for _, rec := range records {
			seen, due, err := t.due(ctx, rec)
			if err != nil {
				return nil, err
			}

			if !due {
				continue
			}

			out = append(out, candidate{record: rec, seenBefore: seen})
			fresh++
		}

		// The listing is sorted newest first, so a page with nothing new means
		// every deeper page is older still.
		if fresh == 0 {
			break
		}
	}

	// Announce oldest first, so that a crash midway leaves a prefix of the
	// history published rather than a scattering of it.
	for i, j := 0, len(out)-1; i < j; i, j = i+1, j-1 {
		out[i], out[j] = out[j], out[i]
	}

	return out, nil
}

// due reports whether a record should be announced and whether it was already
// known under an older mdate.
func (t *Tracker) due(ctx context.Context, rec gb.Record) (seenBefore, due bool, err error) {
	mdate := rec.DateModified.Time().Unix()

	if t.cfg.TSInit > 0 && mdate <= t.cfg.TSInit {
		return false, false, nil
	}

	stored, ok, err := t.store.Seen(ctx, rec.ID)
	if err != nil {
		return false, false, err
	}

	// Equal mdate means the same version of the same mod, which is what most
	// of a listing is on every poll after the first.
	if ok && stored >= mdate {
		return true, false, nil
	}

	return ok, true, nil
}

// announce publishes the event and only then records the mod. Failing between
// the two announces it twice next tick, which beats losing it.
func (t *Tracker) announce(ctx context.Context, cat config.Category, c candidate) error {
	t.announcing.Lock()
	defer t.announcing.Unlock()

	seenBefore, due, err := t.due(ctx, c.record)
	if err != nil {
		return err
	}

	if !due {
		return nil
	}

	c.seenBefore = seenBefore

	var detail *gb.Mod

	if t.cfg.FetchDetails {
		mod, detailErr := t.api.FetchMod(ctx, c.record.ID)
		if detailErr != nil {
			t.log.Warn("fetching mod detail failed, announcing without it", "mod_id", c.record.ID, "error", detailErr)
		} else {
			detail = mod
		}
	}

	payload := buildPayload(cat, c, detail)

	ev, err := events.New(events.TypeModDiscovered, events.SourceTracker, payload)
	if err != nil {
		return fmt.Errorf("build event: %w", err)
	}

	if err := t.pub.Publish(ctx, ev); err != nil {
		return fmt.Errorf("publish: %w", err)
	}

	row := storage.Mod{
		ModID:      c.record.ID,
		CategoryID: cat.ID,
		Name:       c.record.Name,
		ProfileURL: c.record.ProfileURL,
		DateAdded:  payload.DateAdded,
		MDate:      payload.MDate,
		EventID:    ev.ID,
		RecordJSON: c.record.Raw,
	}
	if detail != nil {
		row.DetailJSON = detail.Raw
	}

	if err := t.store.Save(ctx, row); err != nil {
		return fmt.Errorf("save: %w", err)
	}

	t.log.Info("mod announced", "mod_id", row.ModID, "name", row.Name, "is_update", payload.IsUpdate, "event_id", ev.ID)

	return nil
}

func buildPayload(cat config.Category, c candidate, detail *gb.Mod) events.ModDiscovered {
	rec := c.record

	p := events.ModDiscovered{
		ModID:      rec.ID,
		CategoryID: cat.ID,
		Name:       rec.Name,
		ProfileURL: rec.ProfileURL,
		DateAdded:  rec.DateAdded.Time().Unix(),
		MDate:      rec.DateModified.Time().Unix(),

		// IsUpdate answers "have we announced this mod before", not "has the
		// mod ever been edited".
		IsUpdate: c.seenBefore,

		Author:          rec.Submitter.Name,
		AuthorURL:       rec.Submitter.ProfileURL,
		AuthorAvatarURL: rec.Submitter.AvatarURL,
		Studio:          rec.Studio.Name,
		StudioURL:       rec.Studio.ProfileURL,

		GameName:    rec.Game.Name,
		GameURL:     rec.Game.ProfileURL,
		GameIconURL: rec.Game.IconURL,

		RootCategory:    rec.RootCategory.Name,
		RootCategoryURL: rec.RootCategory.ProfileURL,
		SubCategory:     rec.SubCategory.Name,
		SubCategoryURL:  rec.SubCategory.ProfileURL,

		PreviewURL:        rec.FirstImage().URL(),
		HasContentRatings: rec.HasContentRatings,

		Record: rec.Raw,
	}

	if detail == nil {
		return p
	}

	p.HasDetail = true
	p.Description = detail.Description
	p.NSFW = detail.IsNSFW
	p.Version = detail.LatestVersion()
	p.UpdatesCount = detail.UpdatesCount
	p.Detail = detail.Raw

	for _, u := range detail.LatestUpdates {
		update := events.ModUpdate{
			Version:   u.Version,
			Title:     u.Title,
			Text:      u.Text,
			DateAdded: u.DateAdded.Time().Unix(),
		}

		for _, c := range u.ChangeLog {
			update.Changes = append(update.Changes, events.ModChange{Category: c.Category, Text: c.Text})
		}

		p.Updates = append(p.Updates, update)
	}

	for _, f := range detail.SortedFiles() {
		p.Files = append(p.Files, events.ModFile{
			ID:          f.ID,
			Name:        f.File,
			Size:        f.FileSize,
			DownloadURL: f.DownloadURL,
			MD5:         f.MD5Checksum,
			DateAdded:   f.DateAdded.Time().Unix(),
			AvResult:    f.AvResult,
		})
	}

	return p
}

// LogPublisher writes events to the log instead of a broker. It makes the
// tracker runnable end to end before any messaging exists.
type LogPublisher struct {
	Log *slog.Logger
}

func (p LogPublisher) Publish(_ context.Context, ev events.Event) error {
	body, err := json.Marshal(ev)
	if err != nil {
		return fmt.Errorf("marshal event: %w", err)
	}

	p.Log.Info("event published", "type", ev.Type, "id", ev.ID, "event", string(body))

	return nil
}

// Close satisfies the shutdown half of a publisher. There is nothing to flush.
func (p LogPublisher) Close() error { return nil }

// report publishes an error event so that a failure reaches an admin channel
// instead of only a log file nobody is watching.
func (t *Tracker) report(ctx context.Context, stage string, modID int, cause error) {
	ev, err := events.New(events.ErrorType(events.SourceTracker), events.SourceTracker, events.ErrorEvent{
		Service: events.SourceTracker,
		Stage:   stage,
		Message: cause.Error(),
		ModID:   modID,
	})
	if err != nil {
		t.log.Error("building an error event failed", "stage", stage, "error", err)

		return
	}

	if err := t.pub.Publish(ctx, ev); err != nil {
		t.log.Warn("publishing an error event failed", "stage", stage, "error", err)
	}
}
