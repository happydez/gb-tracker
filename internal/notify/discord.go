package notify

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/happydez/gb-tracker/pkg/events"
)

// Discord's own limits. Exceeding any of them is a 400, so text is cut to fit
// rather than sent and rejected.
const (
	discordMaxContent     = 2000
	discordMaxTitle       = 256
	discordMaxDescription = 4096
	discordMaxFieldName   = 256
	discordMaxFieldValue  = 1024
	discordMaxFooter      = 2048
	discordMaxFields      = 25
	discordDefaultColor   = 0x5865F2
)

// ellipsis marks text that was cut to fit.
const ellipsis = "..."

// zeroWidthSpace fills a field Discord would otherwise reject for being empty.
const zeroWidthSpace = "\u200b"

// DiscordSender posts an embed to a webhook.
type DiscordSender struct {
	name       string
	webhookURL string
	username   string
	avatarURL  string
	mentions   []string
	client     *http.Client
}

type DiscordConfig struct {
	// Name identifies the sender in logs.
	Name       string
	WebhookURL string

	// Username and AvatarURL override the webhook's own identity.
	Username  string
	AvatarURL string

	// Mentions are raw Discord ids to ping, as "<@123>" or "<@&456>".
	Mentions []string

	Timeout time.Duration
}

func NewDiscordSender(cfg DiscordConfig) (*DiscordSender, error) {
	if cfg.WebhookURL == "" {
		return nil, fmt.Errorf("discord %s: webhook url must not be empty", cfg.Name)
	}

	timeout := cfg.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}

	return &DiscordSender{
		name:       "discord:" + cfg.Name,
		webhookURL: cfg.WebhookURL,
		username:   cfg.Username,
		avatarURL:  cfg.AvatarURL,
		mentions:   cfg.Mentions,
		client:     &http.Client{Timeout: timeout},
	}, nil
}

func (s *DiscordSender) Name() string {
	return s.name
}

func (s *DiscordSender) Send(ctx context.Context, n events.Notification) error {
	body, err := json.Marshal(s.payload(n))
	if err != nil {
		return fmt.Errorf("%w: marshal payload: %w", ErrPermanent, err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, s.webhookURL, bytes.NewReader(body))
	if err != nil {
		return fmt.Errorf("%w: build request: %w", ErrPermanent, err)
	}

	req.Header.Set("Content-Type", "application/json")

	resp, err := s.client.Do(req)
	if err != nil {
		return fmt.Errorf("post webhook: %w", err)
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	switch {
	case resp.StatusCode >= 200 && resp.StatusCode < 300:
		return nil
	case resp.StatusCode == http.StatusTooManyRequests:
		return fmt.Errorf("rate limited, retry after %s", retryAfter(resp))
	case resp.StatusCode >= 500:
		return fmt.Errorf("discord returned %d", resp.StatusCode)
	default:
		snippet, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return fmt.Errorf("%w: discord returned %d: %s", ErrPermanent, resp.StatusCode, bytes.TrimSpace(snippet))
	}
}

func retryAfter(resp *http.Response) time.Duration {
	v := resp.Header.Get("Retry-After")
	if v == "" {
		return 0
	}

	secs, err := strconv.ParseFloat(v, 64)
	if err != nil {
		return 0
	}

	return time.Duration(secs * float64(time.Second))
}

func (s *DiscordSender) payload(n events.Notification) discordPayload {
	// The timestamp belongs to the thing being announced, not to the moment the
	// message was sent.
	stamp := time.Now().UTC()
	if n.Timestamp > 0 {
		stamp = time.Unix(n.Timestamp, 0).UTC()
	}

	embed := discordEmbed{
		Title:       truncate(n.Title, discordMaxTitle),
		Description: truncate(n.Description, discordMaxDescription),
		URL:         n.URL,
		Color:       parseColor(n.Color),
		Timestamp:   stamp.Format(time.RFC3339),
	}

	if n.ImageURL != "" {
		embed.Image = &discordImage{URL: n.ImageURL}
	}

	if n.ThumbnailURL != "" {
		embed.Thumbnail = &discordImage{URL: n.ThumbnailURL}
	}

	if n.Author.Name != "" {
		embed.Author = &discordAuthor{
			Name:    truncate(n.Author.Name, discordMaxTitle),
			URL:     n.Author.URL,
			IconURL: n.Author.IconURL,
		}
	}

	if n.Footer != "" {
		embed.Footer = &discordFooter{Text: truncate(n.Footer, discordMaxFooter)}
	}

	for i, f := range n.Fields {
		if i == discordMaxFields {
			break
		}

		// Discord rejects a field with an empty name or value, so a spacer is
		// rendered as a zero-width space: an occupied cell that shows nothing.
		if f.Spacer {
			embed.Fields = append(embed.Fields, discordField{Name: zeroWidthSpace, Value: zeroWidthSpace, Inline: true})

			continue
		}

		embed.Fields = append(embed.Fields, discordField{
			Name:   truncate(f.Name, discordMaxFieldName),
			Value:  truncate(f.Value, discordMaxFieldValue),
			Inline: f.Inline,
		})
	}

	content := strings.TrimSpace(strings.Join(append(slices.Clone(s.mentions), n.Content), " "))

	return discordPayload{
		Username:  s.username,
		AvatarURL: s.avatarURL,
		Content:   truncate(content, discordMaxContent),
		Embeds:    []discordEmbed{embed},
	}
}

// parseColor turns "#RRGGBB" into the integer Discord expects.
func parseColor(s string) int {
	v, err := strconv.ParseInt(strings.TrimPrefix(s, "#"), 16, 32)
	if err != nil {
		return discordDefaultColor
	}

	return int(v)
}

func truncate(s string, n int) string {
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

type discordPayload struct {
	Username  string         `json:"username,omitempty"`
	AvatarURL string         `json:"avatar_url,omitempty"`
	Content   string         `json:"content,omitempty"`
	Embeds    []discordEmbed `json:"embeds,omitempty"`
}

type discordEmbed struct {
	Title       string         `json:"title,omitempty"`
	Description string         `json:"description,omitempty"`
	URL         string         `json:"url,omitempty"`
	Color       int            `json:"color,omitempty"`
	Timestamp   string         `json:"timestamp,omitempty"`
	Author      *discordAuthor `json:"author,omitempty"`
	Image       *discordImage  `json:"image,omitempty"`
	Thumbnail   *discordImage  `json:"thumbnail,omitempty"`
	Footer      *discordFooter `json:"footer,omitempty"`
	Fields      []discordField `json:"fields,omitempty"`
}

type discordAuthor struct {
	Name    string `json:"name"`
	URL     string `json:"url,omitempty"`
	IconURL string `json:"icon_url,omitempty"`
}

type discordImage struct {
	URL string `json:"url"`
}

type discordFooter struct {
	Text string `json:"text"`
}

type discordField struct {
	Name   string `json:"name"`
	Value  string `json:"value"`
	Inline bool   `json:"inline,omitempty"`
}
