package bsp

import (
	"fmt"
	"io"
	"os"
	"strings"

	"github.com/dsnet/compress/bzip2"
)

// Compression levels accepted by NewConverter. The range is 1 (fastest)
// to 9 (smallest output); 6 balances speed and ratio for BSP files.
const (
	MinCompression     = 1
	DefaultCompression = 6
	MaxCompression     = 9
)

type converter struct {
	compressionLevel int
}

func NewConverter(compressionLevel int) *converter {
	if compressionLevel < MinCompression || compressionLevel > MaxCompression {
		compressionLevel = DefaultCompression
	}

	return &converter{compressionLevel: compressionLevel}
}

// Compress writes a bzip2-compressed copy of bspPath to bspbz2Path.
// An empty bspbz2Path defaults to bspPath with ".bz2" appended.
// The destination file is removed if compression fails.
func (c *converter) Compress(bspPath string, bspbz2Path string) error {
	if !isBsp(bspPath) {
		return fmt.Errorf("expected .bsp file, got %q", bspPath)
	}

	if strings.TrimSpace(bspbz2Path) == "" {
		bspbz2Path = bspPath + ".bz2"
	}

	in, err := os.Open(bspPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(bspbz2Path)
	if err != nil {
		return err
	}
	defer out.Close()

	bw, err := bzip2.NewWriter(out, &bzip2.WriterConfig{Level: c.compressionLevel})
	if err != nil {
		_ = os.Remove(bspbz2Path)
		return fmt.Errorf("create bzip2 writer: %w", err)
	}

	if _, err := io.Copy(bw, in); err != nil {
		_ = bw.Close()
		_ = os.Remove(bspbz2Path)
		return fmt.Errorf("compress: %w", err)
	}

	if err := bw.Close(); err != nil {
		_ = os.Remove(bspbz2Path)
		return fmt.Errorf("finalize bzip2 stream: %w", err)
	}

	return nil
}

// Decompress writes the decompressed contents of bspbz2Path to bspPath.
// An empty bspPath defaults to bspbz2Path with ".bz2" stripped.
// The destination file is removed if decompression fails.
func (c *converter) Decompress(bspbz2Path string, bspPath string) error {
	if !isBspBz2(bspbz2Path) {
		return fmt.Errorf("expected .bsp.bz2 file, got %q", bspbz2Path)
	}

	if strings.TrimSpace(bspPath) == "" {
		bspPath = strings.TrimSuffix(bspbz2Path, ".bz2")
	}

	in, err := os.Open(bspbz2Path)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(bspPath)
	if err != nil {
		return err
	}
	defer out.Close()

	br, err := bzip2.NewReader(in, nil)
	if err != nil {
		return fmt.Errorf("create bzip2 reader: %w", err)
	}
	defer func() {
		_ = br.Close()
	}()

	if _, err := io.Copy(out, br); err != nil {
		_ = os.Remove(bspPath)
		return fmt.Errorf("decompress: %w", err)
	}

	return nil
}

// Convert picks the direction from the extension and returns the path it
// wrote. An empty dest takes the default for that direction.
func (c *converter) Convert(src, dest string) (string, error) {
	var (
		defaultDest string
		convert     func(string, string) error
	)

	switch {
	case isBsp(src):
		defaultDest, convert = src+".bz2", c.Compress
	case isBspBz2(src):
		defaultDest, convert = strings.TrimSuffix(src, ".bz2"), c.Decompress
	default:
		return "", fmt.Errorf("unsupported file type %q: expected .bsp or .bsp.bz2", src)
	}

	if strings.TrimSpace(dest) == "" {
		dest = defaultDest
	}

	return dest, convert(src, dest)
}
