package notify

import (
	"bytes"
	"encoding/json"
	"fmt"
	"strings"
	"text/template"

	"github.com/happydez/gb-tracker/pkg/events"
)

// Route turns events of one type into a notification. It is how the notifier
// announces things it knows nothing about: the operator writes the templates,
// so teaching it about a new event type is a config change, not a release.
type Route struct {
	// EventType matches an event type, either exactly or as a NATS-style
	// prefix wildcard such as "errors.>".
	EventType string

	Channel string

	Title        string
	Description  string
	URL          string
	ImageURL     string
	ThumbnailURL string
	Color        string
	AuthorName   string
	AuthorURL    string
	AuthorIcon   string
	Footer       string

	Fields []RouteField
}

type RouteField struct {
	Name   string
	Value  string
	Inline bool
}

// Router renders events into notifications using the first matching route.
type Router struct {
	routes []compiledRoute
}

type compiledRoute struct {
	eventType string
	channel   string
	tmpl      *template.Template

	// names lists every template defined above, so rendering can look each one
	// up without reflecting over the Route struct again.
	names  []string
	fields []compiledField
}

type compiledField struct {
	name   string
	inline bool
}

func NewRouter(routes []Route) (*Router, error) {
	out := make([]compiledRoute, 0, len(routes))

	for i, r := range routes {
		if r.EventType == "" {
			return nil, fmt.Errorf("route %d: event_type must not be empty", i)
		}
		if r.Channel == "" {
			return nil, fmt.Errorf("route %d (%s): channel must not be empty", i, r.EventType)
		}

		cr, err := compile(r)
		if err != nil {
			return nil, fmt.Errorf("route %d (%s): %w", i, r.EventType, err)
		}

		out = append(out, cr)
	}

	return &Router{routes: out}, nil
}

func compile(r Route) (compiledRoute, error) {
	cr := compiledRoute{eventType: r.EventType, channel: r.Channel}
	cr.tmpl = template.New(r.EventType)

	parts := map[string]string{
		"title":         r.Title,
		"description":   r.Description,
		"url":           r.URL,
		"image_url":     r.ImageURL,
		"thumbnail_url": r.ThumbnailURL,
		"color":         r.Color,
		"author_name":   r.AuthorName,
		"author_url":    r.AuthorURL,
		"author_icon":   r.AuthorIcon,
		"footer":        r.Footer,
	}

	for name, body := range parts {
		if body == "" {
			continue
		}

		if _, err := cr.tmpl.New(name).Parse(body); err != nil {
			return cr, fmt.Errorf("%s: %w", name, err)
		}

		cr.names = append(cr.names, name)
	}

	for i, f := range r.Fields {
		if f.Name == "" {
			return cr, fmt.Errorf("field %d: name must not be empty", i)
		}

		nameKey := fmt.Sprintf("field_%d_name", i)
		valueKey := fmt.Sprintf("field_%d_value", i)

		if _, err := cr.tmpl.New(nameKey).Parse(f.Name); err != nil {
			return cr, fmt.Errorf("field %d name: %w", i, err)
		}
		if _, err := cr.tmpl.New(valueKey).Parse(f.Value); err != nil {
			return cr, fmt.Errorf("field %d value: %w", i, err)
		}

		cr.fields = append(cr.fields, compiledField{name: nameKey, inline: f.Inline})
	}

	return cr, nil
}

// Render produces a notification for ev, or ok=false when no route matches.
func (r *Router) Render(ev events.Event) (events.Notification, bool, error) {
	route, ok := r.match(ev.Type)
	if !ok {
		return events.Notification{}, false, nil
	}

	var data map[string]any
	if err := json.Unmarshal(ev.Data, &data); err != nil {
		return events.Notification{}, false, fmt.Errorf("%w: decode payload of %s: %w", ErrPermanent, ev.Type, err)
	}

	data["_id"] = ev.ID
	data["_type"] = ev.Type
	data["_source"] = ev.Source
	data["_occurred_at"] = ev.OccurredAt

	rendered := make(map[string]string, len(route.names))
	for _, name := range route.names {
		s, err := route.render(name, data)
		if err != nil {
			return events.Notification{}, false, err
		}

		rendered[name] = s
	}

	n := events.Notification{
		Channel:      route.channel,
		Title:        rendered["title"],
		Description:  rendered["description"],
		URL:          rendered["url"],
		ImageURL:     rendered["image_url"],
		ThumbnailURL: rendered["thumbnail_url"],
		Color:        rendered["color"],
		Footer:       rendered["footer"],
		Author: events.NotificationAuthor{
			Name:    rendered["author_name"],
			URL:     rendered["author_url"],
			IconURL: rendered["author_icon"],
		},
	}

	for i, f := range route.fields {
		name, err := route.render(f.name, data)
		if err != nil {
			return events.Notification{}, false, err
		}

		value, err := route.render(fmt.Sprintf("field_%d_value", i), data)
		if err != nil {
			return events.Notification{}, false, err
		}

		if value == "" {
			continue
		}

		n.Fields = append(n.Fields, events.NotificationField{Name: name, Value: value, Inline: f.inline})
	}

	return n, true, nil
}

func (c compiledRoute) render(name string, data map[string]any) (string, error) {
	var buf bytes.Buffer

	if err := c.tmpl.ExecuteTemplate(&buf, name, data); err != nil {
		return "", fmt.Errorf("%w: render %s of %s: %w", ErrPermanent, name, c.eventType, err)
	}

	return strings.TrimSpace(buf.String()), nil
}

// match finds the first route for a type. Order matters: a specific route has
// to be listed above a wildcard that would also catch it.
func (r *Router) match(eventType string) (compiledRoute, bool) {
	for _, route := range r.routes {
		if matchSubject(route.eventType, eventType) {
			return route, true
		}
	}

	return compiledRoute{}, false
}

// matchSubject supports the NATS wildcards, so routing rules can be written
// the same way stream subjects are: "errors.>" covers every service's errors,
// "mods.*" covers one level.
func matchSubject(pattern, subject string) bool {
	if pattern == subject {
		return true
	}

	pTokens := strings.Split(pattern, ".")
	sTokens := strings.Split(subject, ".")

	for i, p := range pTokens {
		if p == ">" {
			// ">" matches the rest, but only if there is a rest to match.
			return i < len(sTokens)
		}

		if i >= len(sTokens) {
			return false
		}

		if p != "*" && p != sTokens[i] {
			return false
		}
	}

	return len(pTokens) == len(sTokens)
}
