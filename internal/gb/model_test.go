package gb

import (
	"encoding/json"
	"io"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/google/go-cmp/cmp"
)

func loadData(t *testing.T, name string) []byte {
	t.Helper()

	in, err := os.Open(filepath.Join("testdata", name))
	if err != nil {
		t.Fatalf("open file %s: %v", name, err)
	}
	defer func() {
		_ = in.Close()
	}()

	data, err := io.ReadAll(in)
	if err != nil {
		t.Fatalf("read data %s: %v", name, err)
	}

	return data
}

func TestModIndexUnmarshal(t *testing.T) {
	var got ModIndex
	if err := json.Unmarshal(loadData(t, "mod_index.json"), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if len(got.Records) == 0 {
		t.Fatal("no records parsed")
	}

	want := Record{
		ID:            699260,
		ModelName:     "Mod",
		SingularTitle: "Mod",
		IconClasses:   "SubmissionType Mod",
		Name:          "bhop_vse_krivoe",
		ProfileURL:    "https://gamebanana.com/mods/699260",
		DateAdded:     Timestamp(time.Unix(1785528621, 0).UTC()),
		DateModified:  Timestamp(time.Unix(1785528621, 0).UTC()),
		HasFiles:      true,
		Tags:          json.RawMessage(`[]`),
		PreviewMedia: PreviewMedia{
			Images: []PreviewImage{
				{
					Type:      "screenshot",
					BaseURL:   "https://images.gamebanana.com/img/ss/mods",
					File:      "6a6d00f093f49.jpg",
					File220:   "220-90_6a6d00f093f49.jpg",
					Height220: 124,
					Width220:  220,
					File530:   "530-90_6a6d00f093f49.jpg",
					Height530: 298,
					Width530:  530,
					File100:   "100-90_6a6d00f093f49.jpg",
					Height100: 56,
					Width100:  100,
				},
				{
					Type:      "screenshot",
					BaseURL:   "https://images.gamebanana.com/img/ss/mods",
					File:      "6a6d00f0c489b.jpg",
					File100:   "100-90_6a6d00f0c489b.jpg",
					Height100: 56,
					Width100:  100,
				},
				{
					Type:      "screenshot",
					BaseURL:   "https://images.gamebanana.com/img/ss/mods",
					File:      "6a6d00f0d0f52.jpg",
					File100:   "100-90_6a6d00f0d0f52.jpg",
					Height100: 56,
					Width100:  100,
				},
			},
		},
		Submitter: Submitter{
			ID:         4609031,
			Name:       "qwertybox",
			ProfileURL: "https://gamebanana.com/members/4609031",
			AvatarURL:  "https://images.gamebanana.com/static/img/defaults/avatar.gif",
		},
		Game: Game{
			ID:         2,
			Name:       "Counter-Strike: Source",
			ProfileURL: "https://gamebanana.com/games/2",
			IconURL:    "https://images.gamebanana.com/img/ico/games/css_icon.png",
		},
		RootCategory: RootCategory{
			Name:       "Maps",
			ProfileURL: "https://gamebanana.com/mods/cats/5535",
			IconURL:    "https://images.gamebanana.com/img/ico/ModCategory/60995467e5344.gif",
		},
		SubCategory: SubCategory{
			Name:       "Bunny Hop",
			ProfileURL: "https://gamebanana.com/mods/cats/5568",
			IconURL:    "https://images.gamebanana.com/img/ico/ModCategory/5c57694f3fb8c.png",
		},
		InitialVisibility: "show",
		LikeCount:         2,
		PostCount:         2,
		ViewCount:         267,
	}

	var timestampComparer = cmp.Comparer(func(a, b Timestamp) bool {
		return a.Time().Equal(b.Time())
	})

	gotRecord := got.Records[0]

	// Raw is the record verbatim, so it is asserted on its own rather than
	// duplicated into want.
	if len(gotRecord.Raw) == 0 {
		t.Error("Raw is empty, want the record as received")
	}
	gotRecord.Raw = nil

	if diff := cmp.Diff(want, gotRecord, timestampComparer); diff != "" {
		t.Errorf("record mismatch (-want +got):\n%s", diff)
	}
}

func TestRawIsPreserved(t *testing.T) {
	var index ModIndex
	if err := json.Unmarshal(loadData(t, "mod_index.json"), &index); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	// Raw must carry fields the struct does not model, which is the whole
	// reason for keeping it: subscribers get an escape hatch.
	var extra struct {
		Studio struct {
			BannerURL string `json:"_sBannerUrl"`
		} `json:"_aStudio"`
	}

	for _, r := range index.Records {
		if len(r.Raw) == 0 {
			t.Fatalf("record %d: Raw is empty", r.ID)
		}

		if err := json.Unmarshal(r.Raw, &extra); err != nil {
			t.Fatalf("record %d: Raw is not valid json: %v", r.ID, err)
		}
	}

	var mod Mod
	if err := json.Unmarshal(loadData(t, "mod_data.json"), &mod); err != nil {
		t.Fatalf("unmarshal mod: %v", err)
	}

	if len(mod.Raw) == 0 {
		t.Error("mod Raw is empty")
	}
}

func TestCategoryID(t *testing.T) {
	tests := []struct {
		name string
		url  string
		want int
	}{
		{"root category", "https://gamebanana.com/mods/cats/5535", 5535},
		{"sub category", "https://gamebanana.com/mods/cats/5568", 5568},
		{"trailing slash", "https://gamebanana.com/mods/cats/5568/", 5568},
		{"empty", "", 0},
		{"no numeric id", "https://gamebanana.com/mods/cats/abc", 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := (RootCategory{ProfileURL: tt.url}).CategoryID(); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestTimestampUnmarshal(t *testing.T) {
	tests := []struct {
		name  string
		input string
		want  int64
	}{
		{"unix seconds", `{"_tsDateAdded":1785528621}`, 1785528621},
		{"zero", `{"_tsDateAdded":0}`, 0},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			var r Record
			if err := json.Unmarshal([]byte(tt.input), &r); err != nil {
				t.Fatalf("unmarshal: %v", err)
			}
			if got := r.DateAdded.Time().Unix(); got != tt.want {
				t.Errorf("got %d, want %d", got, tt.want)
			}
		})
	}
}

func TestAllRecordsHaveRequiredFields(t *testing.T) {
	var idx ModIndex
	if err := json.Unmarshal(loadData(t, "mod_index.json"), &idx); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	for i, r := range idx.Records {
		if r.ID == 0 {
			t.Errorf("record %d: empty ID", i)
		}
		if r.Name == "" {
			t.Errorf("record %d: empty Name", i)
		}
		if r.DateAdded.Time().IsZero() {
			t.Errorf("record %d (%s): zero DateAdded", i, r.Name)
		}
		if r.SubCategory.CategoryID() == 0 {
			t.Errorf("record %d (%s): cannot parse subcategory id", i, r.Name)
		}
	}
}

func TestModUnmarshal(t *testing.T) {
	var got Mod
	if err := json.Unmarshal(loadData(t, "mod_data.json"), &got); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	if got.Name != "kz_bhop_genkai" {
		t.Errorf("name: got %q", got.Name)
	}
	if got.OwnerName != "Flexlolo" {
		t.Errorf("owner: got %q", got.OwnerName)
	}
	if got.UpdatesCount != 2 {
		t.Errorf("updates count: got %d", got.UpdatesCount)
	}
	if len(got.LatestUpdates) != 2 {
		t.Fatalf("latest updates: got %d, want 2", len(got.LatestUpdates))
	}
	if v := got.LatestUpdates[0].Version; v != "1.1.0" {
		t.Errorf("version: got %q", v)
	}
	if n := len(got.LatestUpdates[0].ChangeLog); n != 23 {
		t.Errorf("changelog entries: got %d, want 23", n)
	}

	f, ok := got.Files["1160715"]
	if !ok {
		t.Fatal("file 1160715 not found")
	}
	if f.FileSize != 258320957 {
		t.Errorf("filesize: got %d", f.FileSize)
	}
	if f.MD5Checksum != "f07cbbdf512843cd9d8bba3e97664e84" {
		t.Errorf("md5: got %q", f.MD5Checksum)
	}
	if !f.HasContents {
		t.Error("expected HasContents to be true")
	}
}

func TestPreviewImageURLs(t *testing.T) {
	tests := []struct {
		name          string
		image         PreviewImage
		wantURL       string
		wantThumbnail string
	}{
		{
			name: "all variants",
			image: PreviewImage{
				BaseURL: "https://images.gamebanana.com/img/ss/mods",
				File:    "a.jpg",
				File220: "220-90_a.jpg",
				File530: "530-90_a.jpg",
				File100: "100-90_a.jpg",
			},
			wantURL:       "https://images.gamebanana.com/img/ss/mods/a.jpg",
			wantThumbnail: "https://images.gamebanana.com/img/ss/mods/530-90_a.jpg",
		},
		{
			// Every image past the first one comes with the 100px variant only.
			name: "thumbnail falls back to 100",
			image: PreviewImage{
				BaseURL: "https://images.gamebanana.com/img/ss/mods",
				File:    "b.jpg",
				File100: "100-90_b.jpg",
			},
			wantURL:       "https://images.gamebanana.com/img/ss/mods/b.jpg",
			wantThumbnail: "https://images.gamebanana.com/img/ss/mods/100-90_b.jpg",
		},
		{
			name:          "trailing slash in base",
			image:         PreviewImage{BaseURL: "https://images.gamebanana.com/img/ss/mods/", File: "c.jpg"},
			wantURL:       "https://images.gamebanana.com/img/ss/mods/c.jpg",
			wantThumbnail: "",
		},
		{
			name:          "empty image",
			image:         PreviewImage{},
			wantURL:       "",
			wantThumbnail: "",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := tt.image.URL(); got != tt.wantURL {
				t.Errorf("URL() = %q, want %q", got, tt.wantURL)
			}

			if got := tt.image.ThumbnailURL(); got != tt.wantThumbnail {
				t.Errorf("ThumbnailURL() = %q, want %q", got, tt.wantThumbnail)
			}
		})
	}
}

func TestFirstImage(t *testing.T) {
	var index ModIndex
	if err := json.Unmarshal(loadData(t, "mod_index.json"), &index); err != nil {
		t.Fatalf("unmarshal: %v", err)
	}

	got := index.Records[0].FirstImage()
	if want := "6a6d00f093f49.jpg"; got.File != want {
		t.Errorf("first image = %q, want %q", got.File, want)
	}

	if got := (Record{}).FirstImage(); got != (PreviewImage{}) {
		t.Errorf("first image of a record without media = %+v, want the zero value", got)
	}
}

func TestSortedFiles(t *testing.T) {
	mod := Mod{
		Files: map[string]File{
			"3":  {ID: "3", DateAdded: Timestamp(time.Unix(300, 0).UTC())},
			"1":  {ID: "1", DateAdded: Timestamp(time.Unix(100, 0).UTC())},
			"2":  {ID: "2", DateAdded: Timestamp(time.Unix(200, 0).UTC())},
			"2b": {ID: "2b", DateAdded: Timestamp(time.Unix(200, 0).UTC())},
		},
	}

	want := []string{"1", "2", "2b", "3"}
	for range 10 {
		var got []string
		for _, f := range mod.SortedFiles() {
			got = append(got, f.ID)
		}

		if diff := cmp.Diff(want, got); diff != "" {
			t.Fatalf("file order mismatch (-want +got):\n%s", diff)
		}
	}

	if got := (Mod{}).SortedFiles(); len(got) != 0 {
		t.Errorf("sorted files of an empty mod = %v, want none", got)
	}
}

func TestLatestVersion(t *testing.T) {
	mod := Mod{LatestUpdates: []UpdateLog{{Version: "1.1.0"}, {Version: "1.0.1"}}}
	if got, want := mod.LatestVersion(), "1.1.0"; got != want {
		t.Errorf("latest version = %q, want %q", got, want)
	}

	if got := (Mod{}).LatestVersion(); got != "" {
		t.Errorf("latest version of a mod without updates = %q, want empty", got)
	}
}
