package config

import (
	"fmt"
	"os"
	"time"

	"gopkg.in/yaml.v3"

	"github.com/happydez/gb-tracker/internal/notify"
	"github.com/happydez/gb-tracker/pkg/bus"
)

// NotifierConfig is the notifier service's own file.
type NotifierConfig struct {
	Log      Log      `yaml:"log"`
	NATS     NATS     `yaml:"nats"`
	Notifier Notifier `yaml:"notifier"`
}

type Notifier struct {
	// Durable names the JetStream consumer, and with it the cursor that
	// survives a restart. Two notifiers on one durable would share the stream
	// between them instead of each seeing all of it.
	Durable string `yaml:"durable"`

	// Subjects is what the consumer receives. It has to cover every event type
	// the routes mention, plus notifications.send.
	Subjects []string `yaml:"subjects"`

	MaxDeliver int        `yaml:"max_deliver"`
	BackOff    []Duration `yaml:"backoff"`

	// Channels maps a channel name to the backends that serve it.
	Channels map[string][]Destination `yaml:"channels"`

	// Routes render events the publisher did not shape into notifications.
	Routes []Route `yaml:"routes"`
}

// Destination is one backend of one channel. Exactly one of its fields is set.
type Destination struct {
	Discord *DiscordDestination `yaml:"discord"`
}

type DiscordDestination struct {
	// Name identifies the sender in logs; the channel name is used when empty.
	Name string `yaml:"name"`

	// WebhookURL is a secret and belongs in an environment variable, expanded
	// into the file as ${GB_DISCORD_WEBHOOK_PUBLIC}.
	WebhookURL string `yaml:"webhook_url"`

	Username  string   `yaml:"username"`
	AvatarURL string   `yaml:"avatar_url"`
	Mentions  []string `yaml:"mentions"`
	Timeout   Duration `yaml:"timeout"`
}

// Route is a rendering rule. Every text field is a Go text/template evaluated
// against the event payload, with the envelope available as ._id, ._type,
// ._source and ._occurred_at.
type Route struct {
	EventType    string       `yaml:"event_type"`
	Channel      string       `yaml:"channel"`
	Title        string       `yaml:"title"`
	Description  string       `yaml:"description"`
	URL          string       `yaml:"url"`
	ImageURL     string       `yaml:"image_url"`
	ThumbnailURL string       `yaml:"thumbnail_url"`
	Color        string       `yaml:"color"`
	AuthorName   string       `yaml:"author_name"`
	AuthorURL    string       `yaml:"author_url"`
	AuthorIcon   string       `yaml:"author_icon"`
	Footer       string       `yaml:"footer"`
	Fields       []RouteField `yaml:"fields"`
}

type RouteField struct {
	Name   string `yaml:"name"`
	Value  string `yaml:"value"`
	Inline bool   `yaml:"inline"`
}

// LoadNotifier reads the notifier config, expanding ${VAR} from the
// environment so that webhook urls never sit in the file.
func LoadNotifier(path string) (*NotifierConfig, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, fmt.Errorf("read config: %w", err)
	}

	cfg := DefaultNotifier()
	if err := yaml.Unmarshal([]byte(os.ExpandEnv(string(data))), cfg); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	if err := cfg.Validate(); err != nil {
		return nil, err
	}

	return cfg, nil
}

func (c *NotifierConfig) Validate() error {
	if err := c.Log.validate(); err != nil {
		return err
	}
	if err := c.NATS.validate(); err != nil {
		return err
	}

	return c.Notifier.validate()
}

func (n Notifier) validate() error {
	if len(n.Channels) == 0 {
		return fmt.Errorf("notifier.channels must not be empty")
	}

	for name, dests := range n.Channels {
		for i, d := range dests {
			if d.Discord == nil {
				return fmt.Errorf("notifier.channels.%s[%d]: no backend configured", name, i)
			}
			if d.Discord.WebhookURL == "" {
				return fmt.Errorf("notifier.channels.%s[%d]: discord.webhook_url must not be empty", name, i)
			}
		}
	}

	for i, r := range n.Routes {
		if _, ok := n.Channels[r.Channel]; !ok {
			return fmt.Errorf("notifier.routes[%d] (%s): no channel named %q", i, r.EventType, r.Channel)
		}
	}

	if err := n.ConsumerConfig().Validate(); err != nil {
		return fmt.Errorf("notifier: %w", err)
	}

	if _, err := notify.NewRouter(n.RouterRoutes()); err != nil {
		return fmt.Errorf("notifier.routes: %w", err)
	}

	return nil
}

func (n Notifier) ConsumerConfig() bus.ConsumerConfig {
	backoff := make([]time.Duration, 0, len(n.BackOff))
	for _, d := range n.BackOff {
		backoff = append(backoff, d.Unwrap())
	}

	return bus.ConsumerConfig{
		Durable:    n.Durable,
		Subjects:   n.Subjects,
		MaxDeliver: n.MaxDeliver,
		BackOff:    backoff,
	}
}

func (n Notifier) RouterRoutes() []notify.Route {
	out := make([]notify.Route, 0, len(n.Routes))

	for _, r := range n.Routes {
		route := notify.Route{
			EventType:    r.EventType,
			Channel:      r.Channel,
			Title:        r.Title,
			Description:  r.Description,
			URL:          r.URL,
			ImageURL:     r.ImageURL,
			ThumbnailURL: r.ThumbnailURL,
			Color:        r.Color,
			AuthorName:   r.AuthorName,
			AuthorURL:    r.AuthorURL,
			AuthorIcon:   r.AuthorIcon,
			Footer:       r.Footer,
		}

		for _, f := range r.Fields {
			route.Fields = append(route.Fields, notify.RouteField{Name: f.Name, Value: f.Value, Inline: f.Inline})
		}

		out = append(out, route)
	}

	return out
}

// Senders builds the dispatch table.
func (n Notifier) Senders() (map[string][]notify.Sender, error) {
	channels := make(map[string][]notify.Sender, len(n.Channels))

	for name, dests := range n.Channels {
		for _, d := range dests {
			senderName := d.Discord.Name
			if senderName == "" {
				senderName = name
			}

			s, err := notify.NewDiscordSender(notify.DiscordConfig{
				Name:       senderName,
				WebhookURL: d.Discord.WebhookURL,
				Username:   d.Discord.Username,
				AvatarURL:  d.Discord.AvatarURL,
				Mentions:   d.Discord.Mentions,
				Timeout:    d.Discord.Timeout.Unwrap(),
			})
			if err != nil {
				return nil, err
			}

			channels[name] = append(channels[name], s)
		}
	}

	return channels, nil
}

func DefaultNotifier() *NotifierConfig {
	broker := bus.DefaultConfig()
	consumer := bus.DefaultConsumerConfig("notifier", "mods.>", "errors.>", "notifications.>")

	backoff := make([]Duration, 0, len(consumer.BackOff))
	for _, d := range consumer.BackOff {
		backoff = append(backoff, Duration(d))
	}

	return &NotifierConfig{
		Log: Log{Level: "info", Format: "text"},
		NATS: NATS{
			URL:             broker.URL,
			Stream:          broker.Stream,
			Subjects:        broker.Subjects,
			MaxAge:          Duration(broker.MaxAge),
			DuplicateWindow: Duration(broker.DuplicateWindow),
			ConnectTimeout:  Duration(broker.ConnectTimeout),
			PublishTimeout:  Duration(broker.PublishTimeout),
		},
		Notifier: Notifier{
			Durable:    consumer.Durable,
			Subjects:   consumer.Subjects,
			MaxDeliver: consumer.MaxDeliver,
			BackOff:    backoff,
		},
	}
}
