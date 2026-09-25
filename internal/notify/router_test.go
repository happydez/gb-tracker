package notify

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/google/go-cmp/cmp"

	"github.com/happydez/gb-tracker/pkg/events"
)

func modEvent(t *testing.T, payload events.ModDiscovered) events.Event {
	t.Helper()

	ev, err := events.New(events.TypeModDiscovered, events.SourceTracker, payload)
	if err != nil {
		t.Fatalf("new event: %v", err)
	}

	return ev
}

func modRoute() Route {
	return Route{
		EventType:   events.TypeModDiscovered,
		Channel:     "public",
		Title:       "{{ .name }}",
		URL:         "{{ .profile_url }}",
		Description: "{{ .description }}",
		Color:       "{{ if .is_update }}#F1C40F{{ else }}#2ECC71{{ end }}",
		AuthorName:  "{{ .author }}",
		Fields: []RouteField{
			{Name: "Status", Value: "{{ if .is_update }}Updated{{ else }}New{{ end }}", Inline: true},
			{Name: "Files", Value: "{{ range .files }}{{ .name }} {{ end }}"},
		},
	}
}

func TestRenderNewMod(t *testing.T) {
	r, err := NewRouter([]Route{modRoute()})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	ev := modEvent(t, events.ModDiscovered{
		Name:        "bhop_ln_portal",
		ProfileURL:  "https://gamebanana.com/mods/1",
		Description: "a bhop map",
		Author:      "qwertybox",
		Files:       []events.ModFile{{Name: "a.zip"}, {Name: "b.zip"}},
	})

	got, ok, err := r.Render(ev)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !ok {
		t.Fatal("no route matched mods.discovered")
	}

	want := events.Notification{
		Channel:     "public",
		Title:       "bhop_ln_portal",
		Description: "a bhop map",
		URL:         "https://gamebanana.com/mods/1",
		Color:       "#2ECC71",
		Author:      events.NotificationAuthor{Name: "qwertybox"},
		Fields: []events.NotificationField{
			{Name: "Status", Value: "New", Inline: true},
			{Name: "Files", Value: "a.zip b.zip"},
		},
	}

	if diff := cmp.Diff(want, got); diff != "" {
		t.Errorf("notification mismatch (-want +got):\n%s", diff)
	}
}

func TestRenderUpdatedModChangesColor(t *testing.T) {
	r, err := NewRouter([]Route{modRoute()})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	got, _, err := r.Render(modEvent(t, events.ModDiscovered{Name: "a", IsUpdate: true}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if got.Color != "#F1C40F" {
		t.Errorf("color = %q, want the updated colour", got.Color)
	}

	if got.Fields[0].Value != "Updated" {
		t.Errorf("status = %q, want Updated", got.Fields[0].Value)
	}
}

func TestRenderDropsEmptyFields(t *testing.T) {
	r, err := NewRouter([]Route{{
		EventType: events.TypeModDiscovered,
		Channel:   "public",
		Title:     "{{ .name }}",
		Fields: []RouteField{
			{Name: "Files", Value: "{{ range .files }}{{ .name }}{{ end }}"},
		},
	}})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	got, _, err := r.Render(modEvent(t, events.ModDiscovered{Name: "a"}))
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if len(got.Fields) != 0 {
		t.Errorf("fields = %+v, want none", got.Fields)
	}
}

func TestRenderReachesUnmodelledFields(t *testing.T) {
	r, err := NewRouter([]Route{{
		EventType: "custom.thing",
		Channel:   "public",
		Title:     "{{ .whatever }} from {{ ._source }}",
	}})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	ev := events.Event{
		ID:         "01JBQX7K9M2NPVWXYZ3ABCDEFG",
		Type:       "custom.thing",
		Source:     "some-service",
		OccurredAt: events.Event{}.OccurredAt,
		Data:       json.RawMessage(`{"whatever":"hello"}`),
	}

	got, ok, err := r.Render(ev)
	if err != nil {
		t.Fatalf("render: %v", err)
	}
	if !ok {
		t.Fatal("no route matched")
	}

	if want := "hello from some-service"; got.Title != want {
		t.Errorf("title = %q, want %q", got.Title, want)
	}
}

func TestRenderNoMatch(t *testing.T) {
	r, err := NewRouter([]Route{modRoute()})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	_, ok, err := r.Render(events.Event{Type: "something.else", Data: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if ok {
		t.Error("a route matched an unrelated event type")
	}
}

func TestRenderFirstRouteWins(t *testing.T) {
	r, err := NewRouter([]Route{
		{EventType: "errors.tracker", Channel: "private", Title: "specific"},
		{EventType: "errors.>", Channel: "private", Title: "wildcard"},
	})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	got, _, err := r.Render(events.Event{Type: "errors.tracker", Data: json.RawMessage(`{}`)})
	if err != nil {
		t.Fatalf("render: %v", err)
	}

	if got.Title != "specific" {
		t.Errorf("title = %q, want the specific route to win", got.Title)
	}
}

func TestMatchSubject(t *testing.T) {
	tests := []struct {
		pattern string
		subject string
		want    bool
	}{
		{"mods.discovered", "mods.discovered", true},
		{"mods.discovered", "mods.updated", false},
		{"errors.>", "errors.tracker", true},
		{"errors.>", "errors.tracker.detail", true},
		{"errors.>", "errors", false},
		{"errors.>", "mods.discovered", false},
		{"mods.*", "mods.discovered", true},
		{"mods.*", "mods.a.b", false},
		{">", "anything.at.all", true},
	}

	for _, tt := range tests {
		if got := matchSubject(tt.pattern, tt.subject); got != tt.want {
			t.Errorf("matchSubject(%q, %q) = %v, want %v", tt.pattern, tt.subject, got, tt.want)
		}
	}
}

func TestNewRouterRejectsBadRoutes(t *testing.T) {
	tests := []struct {
		name  string
		route Route
	}{
		{name: "no event type", route: Route{Channel: "public"}},
		{name: "no channel", route: Route{EventType: "mods.discovered"}},
		{
			name:  "broken template",
			route: Route{EventType: "mods.discovered", Channel: "public", Title: "{{ .name"},
		},
		{
			name:  "field without a name",
			route: Route{EventType: "mods.discovered", Channel: "public", Fields: []RouteField{{Value: "x"}}},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, err := NewRouter([]Route{tt.route}); err == nil {
				t.Fatal("new router: expected an error")
			}
		})
	}
}

func TestRenderBadPayloadIsPermanent(t *testing.T) {
	r, err := NewRouter([]Route{modRoute()})
	if err != nil {
		t.Fatalf("new router: %v", err)
	}

	ev := events.Event{Type: events.TypeModDiscovered, Data: json.RawMessage(`"not an object"`)}

	if _, _, err := r.Render(ev); !errors.Is(err, ErrPermanent) {
		t.Fatalf("error = %v, want it to match ErrPermanent", err)
	}
}
