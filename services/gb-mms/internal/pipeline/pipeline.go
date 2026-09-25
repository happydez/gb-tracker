// Package pipeline turns a discovered mod into maps ready for the game servers.
package pipeline

import (
	"context"
	"fmt"
	"log/slog"
	"os"
	"path"
	"path/filepath"
	"strings"

	"github.com/happydez/gb-tracker/pkg/events"

	"github.com/happydez/gb-mms/internal/archive"
	"github.com/happydez/gb-mms/internal/bsp"
	"github.com/happydez/gb-mms/internal/download"
	"github.com/happydez/gb-mms/internal/match"
	"github.com/happydez/gb-mms/internal/storage"
)

// Uploader puts a map on the FastDL host.
type Uploader interface {
	Upload(ctx context.Context, local, remote string) error
}

// Commander runs the post-upload commands on the game servers.
type Commander interface {
	AddMaps(ctx context.Context, names []string) error
}

// Announcer publishes the result. It is called for skipped mods too, so the
// decision of what is worth reporting stays in one place.
type Announcer interface {
	Announce(ctx context.Context, r Result) error
}

type Config struct {
	WorkDir          string
	KeepWork         bool
	MaxDepth         int
	CompressionLevel int
}

// Result is what came out of processing one mod.
type Result struct {
	Mod      events.ModDiscovered
	Maps     []bsp.MapFile
	MapCount int
	BSPSize  int64
	BZ2Size  int64

	// Stage names the step that failed, for the error event.
	Stage string

	UploadEnabled bool
	Uploaded      bool
	RCONEnabled   bool
	RCONDone      bool
	Skipped       bool
	Err           error
}

type Pipeline struct {
	cfg       Config
	matcher   *match.Matcher
	dl        *download.Downloader
	store     *storage.Storage
	uploader  Uploader
	commander Commander
	announcer Announcer
	log       *slog.Logger
}

func New(
	cfg Config,
	matcher *match.Matcher,
	dl *download.Downloader,
	store *storage.Storage,
	uploader Uploader,
	commander Commander,
	announcer Announcer,
	log *slog.Logger,
) *Pipeline {
	return &Pipeline{
		cfg:       cfg,
		matcher:   matcher,
		dl:        dl,
		store:     store,
		uploader:  uploader,
		commander: commander,
		announcer: announcer,
		log:       log,
	}
}

// Process handles one mod end to end. It returns an error only for failures
// worth retrying; anything decided on purpose (no matching maps, already done)
// comes back as a nil error.
func (p *Pipeline) Process(ctx context.Context, mod events.ModDiscovered) error {
	done, err := p.alreadyDone(ctx, mod)
	if err != nil {
		return err
	}
	if done {
		p.log.Debug("mod already processed", "mod_id", mod.ModID, "name", mod.Name)

		return nil
	}

	if !p.matcher.Mod(mod.CategoryID, mod.Name) {
		return p.finishSkipped(ctx, mod, "mod title does not match any prefix")
	}

	p.log.Info("processing mod", "mod_id", mod.ModID, "name", mod.Name, "files", len(mod.Files))

	dir := filepath.Join(p.cfg.WorkDir, fmt.Sprint(mod.ModID))
	if !p.cfg.KeepWork {
		defer func() {
			if rmErr := os.RemoveAll(dir); rmErr != nil {
				p.log.Warn("cleaning the work dir failed", "dir", dir, "error", rmErr)
			}
		}()
	}

	maps, err := p.collect(ctx, mod, dir)
	if err != nil {
		return err
	}

	if len(maps) == 0 {
		return p.finishSkipped(ctx, mod, "no maps matched the prefixes")
	}

	res := Result{
		Mod:           mod,
		Maps:          maps,
		MapCount:      countNames(maps),
		UploadEnabled: p.uploader != nil,
		RCONEnabled:   p.commander != nil,
	}

	for _, m := range maps {
		if m.Compressed {
			res.BZ2Size += m.Size

			continue
		}

		res.BSPSize += m.Size
	}

	// RCON only runs once the maps are actually downloadable, so a server is
	// never told about a map the players cannot fetch.
	if res.UploadEnabled {
		if err := p.upload(ctx, &res); err != nil {
			res.Err = err
			res.Stage = "upload"
			p.log.Error("upload failed", "mod_id", mod.ModID, "error", err)
		} else {
			res.Uploaded = true
		}
	}

	if res.RCONEnabled && (res.Uploaded || !res.UploadEnabled) {
		if err := p.command(ctx, &res); err != nil {
			res.Err = err
			res.Stage = "rcon"
			p.log.Error("rcon failed", "mod_id", mod.ModID, "error", err)
		} else {
			res.RCONDone = true
		}
	}

	p.log.Info("mod processed",
		"mod_id", mod.ModID, "name", mod.Name,
		"maps", res.MapCount, "bsp_size", res.BSPSize, "bz2_size", res.BZ2Size,
		"uploaded", res.Uploaded, "rcon", res.RCONDone)

	return p.finish(ctx, res)
}

func (p *Pipeline) alreadyDone(ctx context.Context, mod events.ModDiscovered) (bool, error) {
	stored, ok, err := p.store.Processed(ctx, mod.ModID)
	if err != nil {
		return false, err
	}

	return ok && stored >= mod.MDate, nil
}

// collect downloads every unseen file of the mod, unpacks it and returns the
// maps whose names match.
func (p *Pipeline) collect(ctx context.Context, mod events.ModDiscovered, dir string) ([]bsp.MapFile, error) {
	if len(mod.Files) == 0 {
		return nil, nil
	}

	extractor := archive.NewExtractor(p.log, p.cfg.MaxDepth)

	var found []bsp.MapFile

	for _, f := range mod.Files {
		if f.AvResult != "" && !strings.EqualFold(f.AvResult, "clean") {
			p.log.Warn("skipping file, av not clean",
				"mod_id", mod.ModID, "file", f.Name, "av_result", f.AvResult)

			continue
		}

		seen, err := p.store.FileDone(ctx, f.ID)
		if err != nil {
			return nil, err
		}
		if seen {
			p.log.Debug("file already processed", "mod_id", mod.ModID, "file", f.Name)

			continue
		}

		archivePath := filepath.Join(dir, "download", f.Name)

		p.log.Info("downloading", "mod_id", mod.ModID, "file", f.Name, "size", f.Size)

		if err = p.dl.Fetch(ctx, f.DownloadURL, archivePath, f.MD5); err != nil {
			return nil, fmt.Errorf("download %s: %w", f.Name, err)
		}

		maps, err := extractor.ExtractAll(ctx, archivePath, filepath.Join(dir, "extract", f.ID))
		if err != nil {
			return nil, fmt.Errorf("extract %s: %w", f.Name, err)
		}

		p.log.Info("extracted", "mod_id", mod.ModID, "file", f.Name, "maps", len(maps))

		found = append(found, p.keepMatching(mod.CategoryID, maps)...)

		if err := p.store.MarkFile(ctx, f.ID, mod.ModID, f.MD5); err != nil {
			return nil, err
		}
	}

	if len(found) == 0 {
		return nil, nil
	}

	collected, err := archive.CollectMaps(found, filepath.Join(dir, "maps"))
	if err != nil {
		return nil, fmt.Errorf("collect maps: %w", err)
	}

	return p.derive(collected, filepath.Join(dir, "maps"))
}

func (p *Pipeline) keepMatching(categoryID int, maps []bsp.MapFile) []bsp.MapFile {
	out := make([]bsp.MapFile, 0, len(maps))

	for _, m := range maps {
		if _, ok := p.matcher.Map(categoryID, m.Name); !ok {
			p.log.Debug("map skipped by prefix", "map", m.Name)

			continue
		}

		out = append(out, m)
	}

	return out
}

// derive fills in the format the author did not ship: servers need the .bsp,
// FastDL needs the .bsp.bz2, and a mod usually carries only one of them.
func (p *Pipeline) derive(maps []bsp.MapFile, dir string) ([]bsp.MapFile, error) {
	have := make(map[string]map[bool]bool)
	for _, m := range maps {
		if have[m.Name] == nil {
			have[m.Name] = make(map[bool]bool)
		}

		have[m.Name][m.Compressed] = true
	}

	conv := bsp.NewConverter(p.cfg.CompressionLevel)
	out := append([]bsp.MapFile(nil), maps...)

	for _, m := range maps {
		if have[m.Name][!m.Compressed] {
			continue
		}

		if !m.Compressed {
			if err := bsp.ValidateBSP(m.Path); err != nil {
				return nil, fmt.Errorf("validate %s: %w", m.Name, err)
			}
		}

		dest := filepath.Join(dir, m.Name+".bsp")
		if !m.Compressed {
			dest += ".bz2"
		}

		if _, err := conv.Convert(m.Path, dest); err != nil {
			return nil, fmt.Errorf("convert %s: %w", m.Name, err)
		}

		info, err := os.Stat(dest)
		if err != nil {
			return nil, err
		}

		out = append(out, bsp.MapFile{
			Path:       dest,
			Name:       m.Name,
			Compressed: !m.Compressed,
			Size:       info.Size(),
		})

		have[m.Name][!m.Compressed] = true
	}

	return out, nil
}

func (p *Pipeline) upload(ctx context.Context, res *Result) error {
	if p.uploader == nil {
		return nil
	}

	uploaded := make([]storage.Map, 0, len(res.Maps))

	for _, m := range res.Maps {
		if !m.Compressed {
			continue
		}

		dir, ok := p.matcher.Map(res.Mod.CategoryID, m.Name)
		if !ok {
			continue
		}

		remote := path.Join(dir, filepath.Base(m.Path))
		if err := p.uploader.Upload(ctx, m.Path, remote); err != nil {
			return err
		}

		uploaded = append(uploaded, storage.Map{
			Name:       m.Name,
			ModID:      res.Mod.ModID,
			RemotePath: remote,
			BZ2Size:    m.Size,
		})
	}

	return p.store.SaveMaps(ctx, uploaded)
}

func (p *Pipeline) command(ctx context.Context, res *Result) error {
	if p.commander == nil {
		return nil
	}

	return p.commander.AddMaps(ctx, names(res.Maps))
}

func (p *Pipeline) finishSkipped(ctx context.Context, mod events.ModDiscovered, reason string) error {
	p.log.Info("mod skipped", "mod_id", mod.ModID, "name", mod.Name, "reason", reason)

	return p.finish(ctx, Result{Mod: mod, Skipped: true})
}

// finish records the outcome and announces it.
func (p *Pipeline) finish(ctx context.Context, res Result) error {
	m := storage.Mod{
		ModID:    res.Mod.ModID,
		Name:     res.Mod.Name,
		MDate:    res.Mod.MDate,
		Status:   storage.StatusDone,
		MapCount: res.MapCount,
		BSPSize:  res.BSPSize,
		BZ2Size:  res.BZ2Size,
		Uploaded: res.Uploaded,
		RCONDone: res.RCONDone,
	}

	switch {
	case res.Skipped:
		m.Status = storage.StatusSkipped
	case res.Err != nil:
		m.Status = storage.StatusFailed
		m.LastError = res.Err.Error()
	}

	if err := p.store.SaveMod(ctx, m); err != nil {
		return err
	}

	if res.Skipped || p.announcer == nil {
		return nil
	}

	if err := p.announcer.Announce(ctx, res); err != nil {
		return fmt.Errorf("announce: %w", err)
	}

	return nil
}

// countNames counts distinct maps, not files
func countNames(maps []bsp.MapFile) int {
	seen := make(map[string]struct{}, len(maps))
	for _, m := range maps {
		seen[m.Name] = struct{}{}
	}

	return len(seen)
}

func names(maps []bsp.MapFile) []string {
	seen := make(map[string]struct{}, len(maps))
	out := make([]string, 0, len(maps))

	for _, m := range maps {
		if _, ok := seen[m.Name]; ok {
			continue
		}

		seen[m.Name] = struct{}{}
		out = append(out, m.Name)
	}

	return out
}
