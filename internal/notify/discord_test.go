package notify

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"log/slog"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"

	"github.com/happydez/gb-tracker/pkg/events"
)

func testLogger() *slog.Logger {
	return slog.New(slog.NewTextHandler(io.Discard, nil))
}

func sampleNotification() events.Notification {
	return events.Notification{
		Channel:     "public",
		Title:       "bhop_ln_portal",
		Description: "a bhop map",
		URL:         "https://gamebanana.com/mods/1",
		ImageURL:    "https://images.gamebanana.com/a.jpg",
		Color:       "#2ECC71",
		Author:      events.NotificationAuthor{Name: "qwertybox", URL: "https://gamebanana.com/members/1"},
		Fields:      []events.NotificationField{{Name: "Status", Value: "New", Inline: true}},
		Footer:      "Counter-Strike: Source",
	}
}

type discordServer struct {
	*httptest.Server

	mu      sync.Mutex
	bodies  []string
	status  int
	headers map[string]string
}

func newDiscordServer(t *testing.T) *discordServer {
	t.Helper()

	s := &discordServer{status: http.StatusNoContent, headers: map[string]string{}}
	s.Server = httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		body, _ := io.ReadAll(r.Body)

		s.mu.Lock()
		s.bodies = append(s.bodies, string(body))
		status := s.status
		for k, v := range s.headers {
			w.Header().Set(k, v)
		}
		s.mu.Unlock()

		w.WriteHeader(status)
	}))

	t.Cleanup(s.Close)

	return s
}

func (s *discordServer) lastEmbed(t *testing.T) map[string]any {
	t.Helper()

	embeds, ok := s.lastBody(t)["embeds"].([]any)
	if !ok || len(embeds) == 0 {
		t.Fatal("the posted body carries no embeds")
	}

	embed, ok := embeds[0].(map[string]any)
	if !ok {
		t.Fatalf("embed is %T, want an object", embeds[0])
	}

	return embed
}

func (s *discordServer) lastBody(t *testing.T) map[string]any {
	t.Helper()

	s.mu.Lock()
	defer s.mu.Unlock()

	if len(s.bodies) == 0 {
		t.Fatal("nothing was posted")
	}

	var out map[string]any
	if err := json.Unmarshal([]byte(s.bodies[len(s.bodies)-1]), &out); err != nil {
		t.Fatalf("unmarshal posted body: %v", err)
	}

	return out
}

func newSender(t *testing.T, url string) *DiscordSender {
	t.Helper()

	s, err := NewDiscordSender(DiscordConfig{Name: "public", WebhookURL: url, Username: "GameBanana"})
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}

	return s
}

func TestDiscordSendBuildsEmbed(t *testing.T) {
	srv := newDiscordServer(t)
	s := newSender(t, srv.URL)

	if err := s.Send(t.Context(), sampleNotification()); err != nil {
		t.Fatalf("send: %v", err)
	}

	body := srv.lastBody(t)

	if got := body["username"]; got != "GameBanana" {
		t.Errorf("username = %v, want GameBanana", got)
	}

	embeds, ok := body["embeds"].([]any)
	if !ok || len(embeds) != 1 {
		t.Fatalf("embeds = %v, want exactly one", body["embeds"])
	}

	embed, ok := embeds[0].(map[string]any)
	if !ok {
		t.Fatalf("embed is %T, want an object", embeds[0])
	}

	if embed["title"] != "bhop_ln_portal" {
		t.Errorf("title = %v", embed["title"])
	}

	if got, want := embed["color"], float64(0x2ECC71); got != want {
		t.Errorf("color = %v, want %v", got, want)
	}

	if _, ok := embed["image"]; !ok {
		t.Error("image missing from the embed")
	}
}

func TestDiscordOmitsEmptyParts(t *testing.T) {
	srv := newDiscordServer(t)
	s := newSender(t, srv.URL)

	if err := s.Send(t.Context(), events.Notification{Title: "bare"}); err != nil {
		t.Fatalf("send: %v", err)
	}

	embed := srv.lastEmbed(t)

	for _, key := range []string{"author", "image", "thumbnail", "footer", "fields", "description", "url"} {
		if _, present := embed[key]; present {
			t.Errorf("%q is present in the embed, want it omitted", key)
		}
	}
}

func TestDiscordTruncatesLongText(t *testing.T) {
	srv := newDiscordServer(t)
	s := newSender(t, srv.URL)

	long := strings.Repeat("я", discordMaxTitle+50)

	if err := s.Send(t.Context(), events.Notification{Title: long}); err != nil {
		t.Fatalf("send: %v", err)
	}

	embed := srv.lastEmbed(t)

	title, _ := embed["title"].(string)
	if n := len([]rune(title)); n != discordMaxTitle {
		t.Errorf("title is %d runes, want %d", n, discordMaxTitle)
	}

	if !strings.HasSuffix(title, "...") {
		t.Error("a truncated title should end with an ellipsis")
	}
}

func TestDiscordCapsFieldCount(t *testing.T) {
	srv := newDiscordServer(t)
	s := newSender(t, srv.URL)

	n := events.Notification{Title: "many"}
	for range discordMaxFields + 10 {
		n.Fields = append(n.Fields, events.NotificationField{Name: "n", Value: "v"})
	}

	if err := s.Send(t.Context(), n); err != nil {
		t.Fatalf("send: %v", err)
	}

	embed := srv.lastEmbed(t)

	fields, ok := embed["fields"].([]any)
	if !ok {
		t.Fatalf("fields is %T, want a list", embed["fields"])
	}

	if got := len(fields); got != discordMaxFields {
		t.Errorf("fields = %d, want the cap of %d", got, discordMaxFields)
	}
}

func TestDiscordRateLimitIsRetryable(t *testing.T) {
	srv := newDiscordServer(t)
	srv.status = http.StatusTooManyRequests
	srv.headers["Retry-After"] = "1.5"

	s := newSender(t, srv.URL)

	err := s.Send(t.Context(), sampleNotification())
	if err == nil {
		t.Fatal("send: expected an error")
	}

	if errors.Is(err, ErrPermanent) {
		t.Errorf("error %v is permanent, want it retryable", err)
	}
}

func TestDiscordClientErrorIsPermanent(t *testing.T) {
	srv := newDiscordServer(t)
	srv.status = http.StatusNotFound

	s := newSender(t, srv.URL)

	err := s.Send(t.Context(), sampleNotification())
	if !errors.Is(err, ErrPermanent) {
		t.Errorf("error = %v, want it to match ErrPermanent: a deleted webhook will not reappear", err)
	}
}

func TestDiscordServerErrorIsRetryable(t *testing.T) {
	srv := newDiscordServer(t)
	srv.status = http.StatusBadGateway

	s := newSender(t, srv.URL)

	err := s.Send(t.Context(), sampleNotification())
	if err == nil {
		t.Fatal("send: expected an error")
	}

	if errors.Is(err, ErrPermanent) {
		t.Errorf("error %v is permanent, want a 5xx to be retryable", err)
	}
}

func TestNewDiscordSenderRequiresWebhook(t *testing.T) {
	if _, err := NewDiscordSender(DiscordConfig{Name: "public"}); err == nil {
		t.Fatal("expected an error without a webhook url")
	}
}

func TestParseColor(t *testing.T) {
	tests := []struct {
		in   string
		want int
	}{
		{"#2ECC71", 0x2ECC71},
		{"2ECC71", 0x2ECC71},
		{"", discordDefaultColor},
		{"not a colour", discordDefaultColor},
	}

	for _, tt := range tests {
		if got := parseColor(tt.in); got != tt.want {
			t.Errorf("parseColor(%q) = %d, want %d", tt.in, got, tt.want)
		}
	}
}

type stubSender struct {
	name string
	err  error

	mu   sync.Mutex
	sent []events.Notification
}

func (s *stubSender) Name() string { return s.name }

func (s *stubSender) Send(_ context.Context, n events.Notification) error {
	if s.err != nil {
		return s.err
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	s.sent = append(s.sent, n)

	return nil
}

func TestDispatchFansOut(t *testing.T) {
	a := &stubSender{name: "a"}
	b := &stubSender{name: "b"}

	d := NewDispatcher(map[string][]Sender{"public": {a, b}}, testLogger())

	n := sampleNotification()
	if err := d.Dispatch(t.Context(), n); err != nil {
		t.Fatalf("dispatch: %v", err)
	}

	for _, s := range []*stubSender{a, b} {
		if diff := cmp.Diff([]events.Notification{n}, s.sent); diff != "" {
			t.Errorf("sender %s (-want +got):\n%s", s.name, diff)
		}
	}
}

func TestDispatchUnknownChannelIsPermanent(t *testing.T) {
	d := NewDispatcher(map[string][]Sender{"public": {&stubSender{name: "a"}}}, testLogger())

	n := sampleNotification()
	n.Channel = "nowhere"

	err := d.Dispatch(t.Context(), n)
	if !errors.Is(err, ErrPermanent) {
		t.Errorf("error = %v, want ErrPermanent: a missing channel is a config mistake", err)
	}
}

func TestDispatchReportsPartialFailure(t *testing.T) {
	ok := &stubSender{name: "ok"}
	bad := &stubSender{name: "bad", err: errors.New("boom")}

	d := NewDispatcher(map[string][]Sender{"public": {ok, bad}}, testLogger())

	if err := d.Dispatch(t.Context(), sampleNotification()); err == nil {
		t.Fatal("dispatch: expected an error")
	}

	if len(ok.sent) != 1 {
		t.Errorf("the working sender got %d notifications, want 1", len(ok.sent))
	}
}

func TestDispatchEmptyChannel(t *testing.T) {
	d := NewDispatcher(map[string][]Sender{"public": {}}, testLogger())

	if err := d.Dispatch(t.Context(), sampleNotification()); err != nil {
		t.Errorf("dispatch: %v, want a channel with no senders to be a no-op", err)
	}
}

func TestDiscordSendsContentAboveTheEmbed(t *testing.T) {
	srv := newDiscordServer(t)
	s := newSender(t, srv.URL)

	n := sampleNotification()
	n.Content = "New mod **718166** (bhop_scopex)"

	if err := s.Send(t.Context(), n); err != nil {
		t.Fatalf("send: %v", err)
	}

	if got := srv.lastBody(t)["content"]; got != n.Content {
		t.Errorf("content = %v, want %q", got, n.Content)
	}
}

func TestDiscordPrependsMentions(t *testing.T) {
	srv := newDiscordServer(t)

	s, err := NewDiscordSender(DiscordConfig{
		Name:       "public",
		WebhookURL: srv.URL,
		Mentions:   []string{"<@111>", "<@&222>"},
	})
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}

	n := sampleNotification()
	n.Content = "New mod **1** (a)"

	if err := s.Send(t.Context(), n); err != nil {
		t.Fatalf("send: %v", err)
	}

	if want := "<@111> <@&222> New mod **1** (a)"; srv.lastBody(t)["content"] != want {
		t.Errorf("content = %v, want %q", srv.lastBody(t)["content"], want)
	}
}

func TestDiscordMentionsDoNotAccumulate(t *testing.T) {
	srv := newDiscordServer(t)

	s, err := NewDiscordSender(DiscordConfig{
		Name:       "public",
		WebhookURL: srv.URL,
		Mentions:   []string{"<@111>"},
	})
	if err != nil {
		t.Fatalf("new sender: %v", err)
	}

	n := sampleNotification()
	n.Content = "hello"

	for range 2 {
		if err := s.Send(t.Context(), n); err != nil {
			t.Fatalf("send: %v", err)
		}
	}

	if want := "<@111> hello"; srv.lastBody(t)["content"] != want {
		t.Errorf("content = %v, want %q", srv.lastBody(t)["content"], want)
	}
}

func TestDiscordUsesTheNotificationTimestamp(t *testing.T) {
	srv := newDiscordServer(t)
	s := newSender(t, srv.URL)

	n := sampleNotification()
	n.Timestamp = 1789673474

	if err := s.Send(t.Context(), n); err != nil {
		t.Fatalf("send: %v", err)
	}

	want := time.Unix(n.Timestamp, 0).UTC().Format(time.RFC3339)
	if got := srv.lastEmbed(t)["timestamp"]; got != want {
		t.Errorf("timestamp = %v, want %q", got, want)
	}
}

func TestDiscordFallsBackToNow(t *testing.T) {
	srv := newDiscordServer(t)
	s := newSender(t, srv.URL)

	if err := s.Send(t.Context(), sampleNotification()); err != nil {
		t.Fatalf("send: %v", err)
	}

	stamp, _ := srv.lastEmbed(t)["timestamp"].(string)

	got, err := time.Parse(time.RFC3339, stamp)
	if err != nil {
		t.Fatalf("parse timestamp %q: %v", stamp, err)
	}

	if time.Since(got) > time.Minute {
		t.Errorf("timestamp = %s, want roughly now when the notification carries none", got)
	}
}

func TestDiscordRendersSpacerFields(t *testing.T) {
	srv := newDiscordServer(t)
	s := newSender(t, srv.URL)

	n := sampleNotification()
	n.Fields = []events.NotificationField{
		{Name: "Mod ID", Value: "1", Inline: true},
		{Spacer: true, Inline: true},
		{Name: "Game", Value: "CS:S", Inline: true},
	}

	if err := s.Send(t.Context(), n); err != nil {
		t.Fatalf("send: %v", err)
	}

	fields, ok := srv.lastEmbed(t)["fields"].([]any)
	if !ok || len(fields) != 3 {
		t.Fatalf("fields = %v, want three", srv.lastEmbed(t)["fields"])
	}

	spacer, ok := fields[1].(map[string]any)
	if !ok {
		t.Fatalf("spacer field is %T, want an object", fields[1])
	}

	for _, key := range []string{"name", "value"} {
		if spacer[key] == "" {
			t.Errorf("spacer %s is empty, which discord rejects", key)
		}
	}

	if spacer["inline"] != true {
		t.Error("the spacer is not inline, so it does not occupy a slot in the row")
	}
}
