package events

import "encoding/json"

// ModDiscovered is published by the tracker for every mod it has not announced
// before, and again whenever a known mod comes back with a newer MDate.
type ModDiscovered struct {
	ModID int `json:"mod_id"`

	// CategoryID is the watched category the mod was found in, which for a mod
	// in a nested category is whichever listing reached it first.
	CategoryID int `json:"category_id"`

	Name       string `json:"name"`
	ProfileURL string `json:"profile_url"`

	// Unix seconds, straight from the API. MDate is GameBanana's version
	// token: a subscriber deduplicating by mod alone would skip updates, so it
	// belongs in the idempotency key together with ModID.
	DateAdded int64 `json:"date_added"`
	MDate     int64 `json:"mdate"`

	// IsUpdate is false for a mod announced for the first time.
	IsUpdate bool `json:"is_update"`

	Author          string `json:"author"`
	AuthorURL       string `json:"author_url"`
	AuthorAvatarURL string `json:"author_avatar_url"`

	// Studio is empty for a mod submitted by an individual.
	Studio    string `json:"studio"`
	StudioURL string `json:"studio_url"`

	GameName    string `json:"game_name"`
	GameURL     string `json:"game_url"`
	GameIconURL string `json:"game_icon_url"`

	RootCategory    string `json:"root_category"`
	RootCategoryURL string `json:"root_category_url"`
	SubCategory     string `json:"sub_category"`
	SubCategoryURL  string `json:"sub_category_url"`

	PreviewURL string `json:"preview_url"`

	// HasContentRatings is the listing flag saying the mod carries at least one
	// content rating. The API exposes no way to tell which, so it cannot
	// separate a mild warning from an outright block; NSFW is the only rating
	// that arrives as its own field.
	HasContentRatings bool `json:"has_content_ratings"`

	// HasDetail reports whether the detail endpoint was queried and answered.
	// The fields below are empty when it is false, and a subscriber that needs
	// them should skip the event rather than treat them as absent data.
	HasDetail   bool      `json:"has_detail"`
	Description string    `json:"description"`
	NSFW        bool      `json:"nsfw"`
	Version     string    `json:"version"`
	Files       []ModFile `json:"files"`

	// UpdatesCount is how many updates the mod has published in total, which is
	// more than len(Updates): the API only returns the most recent ones.
	UpdatesCount int         `json:"updates_count"`
	Updates      []ModUpdate `json:"updates,omitempty"`

	Record json.RawMessage `json:"record"`
	Detail json.RawMessage `json:"detail,omitempty"`
}

// ModFile is one downloadable archive. Normalised out of the detail response
// so that downloading a mod does not require parsing GameBanana's format.
type ModFile struct {
	ID          string `json:"id"`
	Name        string `json:"name"`
	Size        int64  `json:"size"`
	DownloadURL string `json:"download_url"`
	MD5         string `json:"md5"`
	DateAdded   int64  `json:"date_added"`

	// AvResult is GameBanana's own virus scan verdict, "clean" when it passed.
	AvResult string `json:"av_result"`
}

// ModUpdate is one published revision of a mod, newest first.
type ModUpdate struct {
	Version   string `json:"version,omitempty"`
	Title     string `json:"title,omitempty"`
	Text      string `json:"text,omitempty"`
	DateAdded int64  `json:"date_added,omitempty"`

	Changes []ModChange `json:"changes,omitempty"`
}

// ModChange is one line of a changelog.
type ModChange struct {
	// Category is the author's own label, such as "Bugfix" or "Improvement".
	Category string `json:"category,omitempty"`
	Text     string `json:"text"`
}

// ErrorEvent reports a failure a service could not handle on its own. It is
// informational: the reporting service has already given up or arranged its
// own retry.
type ErrorEvent struct {
	// Service duplicates Event.Source and the errors.<service> type suffix,
	// because a notifier renders payloads through a template that cannot see
	// the envelope. Set it from the same Source* constant.
	Service string `json:"service"`

	// Stage names where it broke: "fetch", "detail", "publish".
	Stage string `json:"stage"`

	Message string `json:"message"`

	// ModID is omitted for failures not tied to a particular mod.
	ModID int `json:"mod_id,omitempty"`
}

// Notification is a message to deliver, in a form no particular platform owns.
//
// A service that wants full control over what people see publishes one of
// these directly. A service that does not can leave the translation to the
// notifier's own templates and never know notifications exist.
type Notification struct {
	// Channel is a name from the notifier's config, such as "public" or
	// "private", not an address. Who actually receives it is the operator's
	// decision, not the sender's.
	Channel string `json:"channel"`

	// Content is plain text shown outside the card, where a backend has such a
	// place: Discord puts it above the embed. Mentions configured on the
	// destination are prepended to it.
	Content string `json:"content,omitempty"`

	Title       string `json:"title"`
	Description string `json:"description,omitempty"`

	// URL makes the title a link.
	URL string `json:"url,omitempty"`

	ImageURL     string `json:"image_url,omitempty"`
	ThumbnailURL string `json:"thumbnail_url,omitempty"`

	// Color is "#RRGGBB". Backends without a notion of colour ignore it.
	Color string `json:"color,omitempty"`

	Author NotificationAuthor `json:"author,omitzero"`

	Fields []NotificationField `json:"fields,omitempty"`

	Footer string `json:"footer,omitempty"`

	// Timestamp is when the thing being announced happened, in unix seconds.
	// Zero means now, which is rarely what a reader wants: an announcement is
	// about an event that already took place.
	Timestamp int64 `json:"timestamp,omitempty"`
}

type NotificationAuthor struct {
	Name    string `json:"name,omitempty"`
	URL     string `json:"url,omitempty"`
	IconURL string `json:"icon_url,omitempty"`
}

type NotificationField struct {
	Name  string `json:"name"`
	Value string `json:"value"`

	// Inline asks for side-by-side layout where the backend supports it.
	Inline bool `json:"inline,omitempty"`

	// Spacer marks a deliberately blank field used to control where a row
	// breaks. A backend that lays fields out in rows renders it as an empty
	// cell; one that does not simply drops it, rather than showing a gap in a
	// list where a gap means nothing.
	Spacer bool `json:"spacer,omitempty"`
}
