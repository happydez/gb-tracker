package gb

import (
	"encoding/json"
	"path"
	"sort"
	"strconv"
	"strings"
	"time"
)

// ModIndex is the top-level response of the apiv11 Mod/Index endpoint.
type ModIndex struct {
	Metadata Metadata `json:"_aMetadata"`
	Records  []Record `json:"_aRecords"`
}

// Metadata describes the listing as a whole. RecordCount is the total across
// all pages, not the length of Records.
type Metadata struct {
	RecordCount int  `json:"_nRecordCount"`
	IsComplete  bool `json:"_bIsComplete"`
	PerPage     int  `json:"_nPerpage"`
}

// Record is a single mod listing entry from the Mod/Index API.
type Record struct {
	ID            int       `json:"_idRow"`
	ModelName     string    `json:"_sModelName"`
	SingularTitle string    `json:"_sSingularTitle"`
	IconClasses   string    `json:"_sIconClasses"`
	Name          string    `json:"_sName"`
	ProfileURL    string    `json:"_sProfileUrl"`
	DateAdded     Timestamp `json:"_tsDateAdded"`
	DateModified  Timestamp `json:"_tsDateModified"`
	// DateUpdated is present only on mods that published a versioned update,
	// and absent from most records.
	DateUpdated       Timestamp       `json:"_tsDateUpdated"`
	HasFiles          bool            `json:"_bHasFiles"`
	Tags              json.RawMessage `json:"_aTags"`
	PreviewMedia      PreviewMedia    `json:"_aPreviewMedia"`
	Submitter         Submitter       `json:"_aSubmitter"`
	Studio            Studio          `json:"_aStudio"`
	Game              Game            `json:"_aGame"`
	RootCategory      RootCategory    `json:"_aRootCategory"`
	SubCategory       SubCategory     `json:"_aSubCategory"`
	Version           string          `json:"_sVersion"`
	IsObsolete        bool            `json:"_bIsObsolete"`
	InitialVisibility string          `json:"_sInitialVisibility"`
	HasContentRatings bool            `json:"_bHasContentRatings"`
	LikeCount         int             `json:"_nLikeCount"`
	PostCount         int             `json:"_nPostCount"`
	ViewCount         int             `json:"_nViewCount"`
	WasFeatured       bool            `json:"_bWasFeatured"`
	IsOwnedByAccessor bool            `json:"_bIsOwnedByAccessor"`
	// Raw is the record exactly as it arrived, kept so that callers can pass
	// on fields this struct does not model. Not part of the JSON itself.
	Raw json.RawMessage `json:"-"`
}

func (r *Record) UnmarshalJSON(b []byte) error {
	// The alias sheds the methods, this one included, so the nested call does
	// not recurse.
	type alias Record

	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}

	*r = Record(a)
	r.Raw = append(json.RawMessage(nil), b...)

	return nil
}

// FirstImage returns the record's primary screenshot, or the zero value when
// the mod has no preview media.
func (r Record) FirstImage() PreviewImage {
	if len(r.PreviewMedia.Images) == 0 {
		return PreviewImage{}
	}

	return r.PreviewMedia.Images[0]
}

type PreviewMedia struct {
	Images []PreviewImage `json:"_aImages"`
}

// PreviewImage is one screenshot.
type PreviewImage struct {
	Type    string `json:"_sType"`
	BaseURL string `json:"_sBaseUrl"`
	Caption string `json:"_sCaption"`

	File string `json:"_sFile"`

	File220   string `json:"_sFile220"`
	Height220 int    `json:"_hFile220"`
	Width220  int    `json:"_wFile220"`

	File530   string `json:"_sFile530"`
	Height530 int    `json:"_hFile530"`
	Width530  int    `json:"_wFile530"`

	File100   string `json:"_sFile100"`
	Height100 int    `json:"_hFile100"`
	Width100  int    `json:"_wFile100"`
}

// URL returns the full-size image URL. GameBanana splits the address into a
// base and a file name, so neither field is usable on its own.
func (i PreviewImage) URL() string {
	if i.BaseURL == "" || i.File == "" {
		return ""
	}

	return strings.TrimRight(i.BaseURL, "/") + "/" + i.File
}

func (i PreviewImage) ThumbnailURL() string {
	if i.BaseURL == "" {
		return ""
	}

	base := strings.TrimRight(i.BaseURL, "/") + "/"

	switch {
	case i.File530 != "":
		return base + i.File530
	case i.File220 != "":
		return base + i.File220
	case i.File100 != "":
		return base + i.File100
	default:
		return ""
	}
}

type Submitter struct {
	ID          int    `json:"_idRow"`
	Name        string `json:"_sName"`
	IsOnline    bool   `json:"_bIsOnline"`
	HasRipe     bool   `json:"_bHasRipe"`
	ProfileURL  string `json:"_sProfileUrl"`
	AvatarURL   string `json:"_sAvatarUrl"`
	HDAvatarURL string `json:"_sHdAvatarUrl"`
	UpicURL     string `json:"_sUpicUrl"`
}

// Studio is the team a mod was submitted under.
type Studio struct {
	Name       string `json:"_sName"`
	ProfileURL string `json:"_sProfileUrl"`
	BannerURL  string `json:"_sBannerUrl"`
}

type Game struct {
	ID         int    `json:"_idRow"`
	Name       string `json:"_sName"`
	ProfileURL string `json:"_sProfileUrl"`
	IconURL    string `json:"_sIconUrl"`
}

type RootCategory struct {
	Name       string `json:"_sName"`
	ProfileURL string `json:"_sProfileUrl"`
	IconURL    string `json:"_sIconUrl"`
}

// CategoryID extracts the numeric category ID from ProfileURL.
// GameBanana category URLs look like "https://gamebanana.com/mods/cats/5535",
// with the numeric id as the last path segment. Returns 0 if the URL is empty
// or not in the expected format.
func (c RootCategory) CategoryID() int {
	return parseCategoryIDFromURL(c.ProfileURL)
}

type SubCategory struct {
	Name       string `json:"_sName"`
	ProfileURL string `json:"_sProfileUrl"`
	IconURL    string `json:"_sIconUrl"`
}

// CategoryID extracts the numeric category ID from ProfileURL.
// See RootCategory.CategoryID for format details. Returns 0 when the mod has
// no subcategory: GameBanana omits _aSubCategory entirely for root-only mods.
func (c SubCategory) CategoryID() int {
	return parseCategoryIDFromURL(c.ProfileURL)
}

func parseCategoryIDFromURL(profileURL string) int {
	id, err := strconv.Atoi(path.Base(strings.TrimRight(profileURL, "/")))
	if err != nil {
		return 0
	}

	return id
}

// Mod is the detailed mod payload from the Core/Item/Data API.
type Mod struct {
	Name        string `json:"name"`
	Description string `json:"description"`

	LatestUpdates []UpdateLog `json:"Updates().aGetLatestUpdates()"`
	UpdatesCount  int         `json:"Updates().nUpdatesCount()"`

	IsNSFW    bool   `json:"Nsfw().bIsNsfw()"`
	OwnerName string `json:"Owner().name"`

	// UDate is the last published update, MDate the last modification of the
	// submission itself. A mod that never published an update has no UDate.
	UDate Timestamp `json:"udate"`
	MDate Timestamp `json:"mdate"`

	GameName         string `json:"Game().name"`
	RootCategoryName string `json:"RootCategory().name"`
	CategoryName     string `json:"Category().name"`

	// Files is keyed by file id, the same value as File.ID.
	Files map[string]File `json:"Files().aFiles()"`

	ProfileURL string `json:"Url().sProfileUrl()"`
	PreviewURL string `json:"Preview().sStructuredDataFullsizeUrl()"`

	// Raw is the response exactly as it arrived. See Record.Raw.
	Raw json.RawMessage `json:"-"`
}

func (m *Mod) UnmarshalJSON(b []byte) error {
	type alias Mod

	var a alias
	if err := json.Unmarshal(b, &a); err != nil {
		return err
	}

	*m = Mod(a)
	m.Raw = append(json.RawMessage(nil), b...)

	return nil
}

// SortedFiles returns the mod's files oldest first. Files is a map, and Go
// randomises map iteration order on purpose, so ranging over it directly makes
// behaviour differ between runs.
func (m Mod) SortedFiles() []File {
	files := make([]File, 0, len(m.Files))
	for _, f := range m.Files {
		files = append(files, f)
	}

	sort.Slice(files, func(i, j int) bool {
		if files[i].DateAdded.Time().Equal(files[j].DateAdded.Time()) {
			return files[i].ID < files[j].ID
		}

		return files[i].DateAdded.Time().Before(files[j].DateAdded.Time())
	})

	return files
}

// LatestVersion returns the version string of the most recent update, or an
// empty string for a mod that has never published one.
func (m Mod) LatestVersion() string {
	if len(m.LatestUpdates) == 0 {
		return ""
	}

	return m.LatestUpdates[0].Version
}

type UpdateLog struct {
	Title     string            `json:"_sTitle"`
	ChangeLog []UpdateChangeLog `json:"_aChangeLog"`
	Text      string            `json:"_sText"`
	DateAdded Timestamp         `json:"_tsDateAdded"`
	Version   string            `json:"_sVersion"`
}

// UpdateChangeLog is one line of a changelog. Its keys are bare, without the
// underscore prefix the rest of the API uses.
type UpdateChangeLog struct {
	Text     string `json:"text"`
	Category string `json:"cat"`
}

// File is one downloadable archive attached to a mod. ID arrives as a string
// here, unlike the numeric _idRow of the listing API.
type File struct {
	ID          string    `json:"_idRow"`
	File        string    `json:"_sFile"`
	FileSize    int64     `json:"_nFilesize"`
	DateAdded   Timestamp `json:"_tsDateAdded"`
	DownloadURL string    `json:"_sDownloadUrl"`
	MD5Checksum string    `json:"_sMd5Checksum"`

	DownloadCount int `json:"_nDownloadCount"`

	// Analysis and Av fields report the scanning GameBanana runs on an upload.
	// A file whose AvResult is not "clean" should not be handed to users.
	AnalysisState         string `json:"_sAnalysisState"`
	AnalysisResult        string `json:"_sAnalysisResult"`
	AnalysisResultVerbose string `json:"_sAnalysisResultVerbose"`
	AvState               string `json:"_sAvState"`
	AvResult              string `json:"_sAvResult"`

	IsArchived  bool `json:"_bIsArchived"`
	HasContents bool `json:"_bHasContents"`
}

// Timestamp wraps time.Time for the Unix-seconds format GameBanana uses.
type Timestamp time.Time

// Time returns the timestamp as a standard time.Time.
func (t Timestamp) Time() time.Time {
	return time.Time(t)
}

func (t *Timestamp) UnmarshalJSON(b []byte) error {
	if string(b) == "null" {
		return nil
	}

	var s int64
	if err := json.Unmarshal(b, &s); err != nil {
		return err
	}

	*t = Timestamp(time.Unix(s, 0).UTC())

	return nil
}
