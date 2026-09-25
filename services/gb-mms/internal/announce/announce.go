// Package announce turns a processed mod into a notification.
package announce

import (
	"context"
	"fmt"
	"strings"

	"github.com/happydez/gb-tracker/pkg/bus"
	"github.com/happydez/gb-tracker/pkg/events"

	"github.com/happydez/gb-mms/internal/pipeline"
)

const (
	colorNew     = "#2ECC71"
	colorUpdated = "#F1C40F"
)

const source = "gb-mms"

// DefaultCoverURL is GameBanana's own stand-in image for a flagged thumbnail.
const DefaultCoverURL = "https://images.gamebanana.com/static/img/DefaultEmbeddables/nsfw.jpg"

type Config struct {
	Channel   string
	CoverNSFW bool
	CoverURL  string
}

func DefaultConfig() Config {
	return Config{
		Channel:   "public",
		CoverNSFW: true,
		CoverURL:  DefaultCoverURL,
	}
}

func (c Config) Validate() error {
	if c.Channel == "" {
		return fmt.Errorf("announce: channel must not be empty")
	}

	if c.CoverNSFW && c.CoverURL == "" {
		return fmt.Errorf("announce: cover_url must be set while cover_nsfw is on")
	}

	return nil
}

type Publisher interface {
	Publish(ctx context.Context, ev events.Event) error
}

type Announcer struct {
	cfg Config
	pub Publisher
}

func New(cfg Config, pub Publisher) *Announcer {
	return &Announcer{cfg: cfg, pub: pub}
}

// Announce publishes the outcome.
func (a *Announcer) Announce(ctx context.Context, r pipeline.Result) error {
	typ, payload := events.TypeNotificationSend, any(Build(a.cfg, r))
	if r.Err != nil {
		typ, payload = events.ErrorType(source), any(BuildError(r))
	}

	ev, err := events.New(typ, source, payload)
	if err != nil {
		return fmt.Errorf("%w: build %s: %w", bus.ErrTerminal, typ, err)
	}

	return a.pub.Publish(ctx, ev)
}

// BuildError composes the report for a mod that failed.
func BuildError(r pipeline.Result) events.ErrorEvent {
	stage := r.Stage
	if stage == "" {
		stage = "process"
	}

	msg := "unknown failure"
	if r.Err != nil {
		msg = r.Err.Error()
	}

	return events.ErrorEvent{
		Service: source,
		Stage:   stage,
		Message: fmt.Sprintf("%s: %s", r.Mod.Name, msg),
		ModID:   r.Mod.ModID,
	}
}

// Build composes the message for a mod that processed cleanly.
func Build(cfg Config, r pipeline.Result) events.Notification {
	m := r.Mod
	n := events.Notification{
		Channel:      cfg.Channel,
		Content:      content(m),
		Title:        inline(m.Name),
		URL:          m.ProfileURL,
		Description:  description(r),
		ImageURL:     m.PreviewURL,
		ThumbnailURL: m.GameIconURL,
		Color:        colorNew,
		Author: events.NotificationAuthor{
			Name:    inline(m.Author),
			URL:     m.AuthorURL,
			IconURL: m.AuthorAvatarURL,
		},
		Footer:    footer(m),
		Timestamp: m.MDate,
	}

	if m.IsUpdate {
		n.Color = colorUpdated
	}

	if m.Studio != "" {
		n.Author = events.NotificationAuthor{Name: inline(m.Studio), URL: m.StudioURL}
	}

	n.Fields = []events.NotificationField{
		{Name: "Mod ID", Value: link(fmt.Sprint(m.ModID), m.ProfileURL), Inline: true},
		{Name: "Status", Value: status(r), Inline: true},
		{Spacer: true, Inline: true},

		{Name: "Game", Value: link(inline(m.GameName), m.GameURL), Inline: true},
		{Name: "Category", Value: categories(m), Inline: true},
		{Spacer: true, Inline: true},
	}

	if cfg.CoverNSFW && m.NSFW {
		n.ImageURL = cfg.CoverURL
	}

	return n
}

func content(m events.ModDiscovered) string {
	verb := "New mod"
	if m.IsUpdate {
		verb = "Updated mod"
	}

	return fmt.Sprintf("%s **%d** (%s)", verb, m.ModID, inline(m.Name))
}

func status(r pipeline.Result) string {
	if r.Mod.IsUpdate {
		return "🟡 UPDATED"
	}

	return "🟢 NEW"
}

func description(r pipeline.Result) string {
	var lines []string

	if d := inline(r.Mod.Description); d != "" {
		lines = append(lines, "**Description:** "+clip(d, 200), "")
	}

	lines = append(lines,
		fmt.Sprintf("**Total maps:** %d", r.MapCount),
		fmt.Sprintf("**Total (.bsp.bz2) size:** %s", humanSize(r.BZ2Size)),
		fmt.Sprintf("**Total (.bsp) size:** %s", humanSize(r.BSPSize)),
	)

	lines = append(lines, updates(r.Mod)...)

	var steps []string

	if r.UploadEnabled {
		steps = append(steps, check(r.Uploaded)+" Uploaded to server storage")
	}

	if r.RCONEnabled {
		steps = append(steps, check(r.RCONDone)+" RCON Commands")
	}

	if len(steps) > 0 {
		lines = append(lines, "")
		lines = append(lines, steps...)
	}

	return strings.Join(lines, "\n")
}

const maxChanges = 6

func updates(m events.ModDiscovered) []string {
	if len(m.Updates) == 0 {
		return nil
	}

	u := m.Updates[0]

	head := fmt.Sprintf("**Updates (%d)**", max(m.UpdatesCount, len(m.Updates)))
	if label := label(u); label != "" {
		head += " - " + label
	}

	lines := []string{"", head}

	if len(u.Changes) == 0 {
		if text := plain(u.Text); text != "" {
			lines = append(lines, clip(text, 300))
		}

		return lines
	}

	for i, c := range u.Changes {
		if i == maxChanges {
			lines = append(lines, fmt.Sprintf("• ...and %d more", len(u.Changes)-maxChanges))

			break
		}

		text := clip(inline(c.Text), 160)
		if c.Category != "" {
			text = fmt.Sprintf("[%s] %s", inline(c.Category), text)
		}

		lines = append(lines, "• "+text)
	}

	return lines
}

// label names a revision, which may carry a version, a title, both or neither.
func label(u events.ModUpdate) string {
	parts := make([]string, 0, 2)
	for _, p := range []string{u.Version, u.Title} {
		if p = inline(p); p != "" {
			parts = append(parts, p)
		}
	}

	return strings.Join(parts, ": ")
}

func check(ok bool) string {
	if ok {
		return "✅"
	}

	return "❌"
}

func categories(m events.ModDiscovered) string {
	parts := make([]string, 0, 2)

	if m.RootCategory != "" {
		parts = append(parts, link(inline(m.RootCategory), m.RootCategoryURL))
	}

	if m.SubCategory != "" {
		parts = append(parts, link(inline(m.SubCategory), m.SubCategoryURL))
	}

	return strings.Join(parts, " / ")
}

func footer(m events.ModDiscovered) string {
	parts := make([]string, 0, 2)
	for _, p := range []string{"GameBanana", inline(m.SubCategory)} {
		if p != "" {
			parts = append(parts, p)
		}
	}

	return strings.Join(parts, " • ")
}

func link(text, url string) string {
	if text == "" {
		return ""
	}

	if url == "" {
		return text
	}

	return fmt.Sprintf("[%s](%s)", text, url)
}

const ellipsis = "..."

func clip(s string, n int) string {
	runes := []rune(s)
	if len(runes) <= n {
		return s
	}

	marker := []rune(ellipsis)
	if n <= len(marker) {
		return string(runes[:n])
	}

	return string(runes[:n-len(marker)]) + ellipsis
}

func humanSize(n int64) string {
	const unit = 1024

	if n < unit {
		return fmt.Sprintf("%d B", n)
	}

	div, exp := int64(unit), 0
	for size := n / unit; size >= unit; size /= unit {
		div *= unit
		exp++
	}

	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
