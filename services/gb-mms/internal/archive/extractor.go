package archive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"io"
	"io/fs"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"

	"github.com/happydez/gb-mms/internal/bsp"
)

const defaultMapDepth = 8

// Extractor unpacks map archives, descending into nested archives up to
// maxDepth levels.
type Extractor struct {
	maxDepth int
	logger   *slog.Logger
	a7zip    arcext
	aUnrar   arcext
}

// NewExtractor returns an Extractor limited to maxDepth levels of archive
// nesting. Values below 1 fall back to defaultMapDepth = 8.
func NewExtractor(logger *slog.Logger, maxDepth int) *Extractor {
	if maxDepth <= 0 {
		maxDepth = defaultMapDepth
	}
	if logger == nil {
		panic("logger is nil")
	}

	return &Extractor{maxDepth: maxDepth, logger: logger, a7zip: new7Zip(logger), aUnrar: newUnrar(logger)}
}

// ExtractAll unpacks srcPath and every archive found inside it, returning all
// map files discovered. destPath is wiped before extraction starts.
func (e Extractor) ExtractAll(ctx context.Context, srcPath string, destPath string) ([]bsp.MapFile, error) {
	if err := os.RemoveAll(destPath); err != nil {
		return nil, err
	}

	if err := e.Extract(ctx, srcPath, destPath); err != nil {
		return nil, err
	}

	handled := make(map[string]bool)
	for depth := 1; depth <= e.maxDepth; depth++ {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}

		nested, err := e.findArchives(destPath, handled)
		if err != nil {
			return nil, err
		}
		if len(nested) == 0 {
			break
		}

		e.logger.Debug("nested archives found", "depth", depth, "count", len(nested))

		for _, arc := range nested {
			handled[arc] = true
			sub := arc + ".d"
			if err := e.Extract(ctx, arc, sub); err != nil {
				e.logger.Warn("nested archive skipped", "src", filepath.Base(arc), "err", err)
				_ = os.RemoveAll(sub)
				continue
			}
		}
	}

	if left, _ := e.findArchives(destPath, handled); len(left) > 0 {
		e.logger.Warn("depth limit reached, some archives left unpacked", "max_depth", e.maxDepth, "count", len(left))
	}

	return e.findMaps(destPath)
}

// Extract unpacks a single archive into destPath, leaving any nested archives
// inside untouched. Use ExtractAll to descend into them.
func (e Extractor) Extract(ctx context.Context, srcPath string, destPath string) error {
	if err := os.MkdirAll(destPath, 0o750); err != nil {
		return fmt.Errorf("create dest dir %s: %w", destPath, err)
	}

	if isRar(srcPath) {
		return e.aUnrar.Extract(ctx, srcPath, destPath)
	}

	return e.a7zip.Extract(ctx, srcPath, destPath)
}

func (e Extractor) findArchives(root string, handled map[string]bool) ([]string, error) {
	var result []string
	err := filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if d.IsDir() || handled[path] {
			return nil
		}

		if _, _, ok := parseMapName(path); ok {
			return nil
		}
		if isArchive(path) {
			result = append(result, path)
		}

		return nil
	})

	return result, err
}

func (e Extractor) findMaps(root string) ([]bsp.MapFile, error) {
	seen := make(map[string]bsp.MapFile)
	err := filepath.WalkDir(
		root,
		func(path string, d fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if d.IsDir() {
				return nil
			}

			name, cmprsd, ok := parseMapName(path)
			if !ok {
				return nil
			}

			var size int64
			if info, err := d.Info(); err == nil {
				size = info.Size()
			}

			key := name + "|" + strconv.FormatBool(cmprsd)
			if prev, ok := seen[key]; ok {
				if prev.Size != size {
					e.logger.Warn("duplicate map with different size",
						"name", name, "compressed", cmprsd,
						"kept", prev.Size, "skipped", size,
					)
				}
				return nil
			}

			seen[key] = bsp.MapFile{
				Path:       path,
				Name:       name,
				Compressed: cmprsd,
				Size:       size,
			}

			return nil
		},
	)
	if err != nil {
		return nil, err
	}

	result := make([]bsp.MapFile, 0, len(seen))
	for _, m := range seen {
		result = append(result, m)
	}

	return result, nil
}

func parseMapName(filePath string) (name string, compressed bool, ok bool) {
	base := filepath.Base(filePath)
	lower := strings.ToLower(base)
	switch {
	case strings.HasSuffix(lower, ".bsp.bz2"):
		return base[:len(base)-len(".bsp.bz2")], true, true
	case strings.HasSuffix(lower, ".bsp"):
		return base[:len(base)-len(".bsp")], false, true
	default:
		return "", false, false
	}
}

func isRar(filePath string) bool {
	file, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer file.Close()

	buf := make([]byte, 8)
	n, err := io.ReadFull(file, buf)
	if err != nil && !errors.Is(err, io.ErrUnexpectedEOF) {
		return false
	}
	if n < 7 {
		return false
	}

	return bytes.HasPrefix(buf[:n], []byte("Rar!\x1a\x07"))
}

func isArchive(filePath string) bool {
	file, err := os.Open(filePath)
	if err != nil {
		return false
	}
	defer file.Close()

	buf := make([]byte, 512)
	n, _ := file.Read(buf)
	b := buf[:n]
	switch {
	case hasPrefix(b, "PK\x03\x04"), hasPrefix(b, "PK\x05\x06"), hasPrefix(b, "PK\x07\x08"): // zip
		return true
	case hasPrefix(b, "Rar!\x1a\x07"): // rar
		return true
	case hasPrefix(b, "7z\xbc\xaf\x27\x1c"): // 7z
		return true
	case hasPrefix(b, "\x1f\x8b"): // gzip
		return true
	case hasPrefix(b, "BZh"): // bzip2
		return true
	case hasPrefix(b, "\xfd7zXZ\x00"): // xz
		return true
	case hasPrefix(b, "\x28\xb5\x2f\xfd"): // zstd
		return true
	case (n >= 262) && (string(b[257:262]) == "ustar"): // tar
		return true
	}

	return false
}

func hasPrefix(b []byte, prefix string) bool {
	return (len(b) >= len(prefix)) && (string(b[:len(prefix)]) == prefix)
}
