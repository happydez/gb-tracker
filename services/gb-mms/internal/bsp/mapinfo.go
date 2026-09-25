package bsp

import (
	"encoding/binary"
	"fmt"
	"io"
	"os"
	"strings"
)

// MapFile describes a Source engine map file found during extraction.
// A single map may exist in both formats (.bsp and .bsp.bz2); each one
// is a separate MapFile sharing the same Name.
type MapFile struct {
	Path       string
	Name       string
	Compressed bool // true for .bsp.bz2
	Size       int64
}

func isBsp(filePath string) bool {
	return strings.HasSuffix(strings.ToLower(filePath), ".bsp")
}

func isBspBz2(filePath string) bool {
	return strings.HasSuffix(strings.ToLower(filePath), ".bsp.bz2")
}

const (
	sourceMagic     = "VBSP"
	sourceLumpCount = 64
	goldsrcLumps    = 15
)

// ValidateBSP checks that the file at path looks like a structurally sound BSP.
// It returns a descriptive error when the file is truncated, has an unknown
// header or contains lumps that point outside the file.
func ValidateBSP(bspPath string) error {
	in, err := os.Open(bspPath)
	if err != nil {
		return fmt.Errorf("open: %w", err)
	}
	defer in.Close()

	stat, err := in.Stat()
	if err != nil {
		return fmt.Errorf("stat: %w", err)
	}

	return ValidateBSPReader(in, stat.Size())
}

// ValidateBSPReader is ValidateBSP over a stream. The size cannot be taken
// from the file, so the caller passes it: the lump offsets are checked against
// it and a wrong one turns a sound map into a rejected one.
func ValidateBSPReader(r io.Reader, size int64) error {
	var head [8]byte
	if _, err := io.ReadFull(r, head[:]); err != nil {
		return fmt.Errorf("file too small to be a bsp (%d bytes)", size)
	}

	if string(head[:4]) == sourceMagic {
		return validateBSPSource(head, r, size)
	}

	return validateBSPGoldsrc(head, r, size)
}

func validateBSPSource(head [8]byte, r io.Reader, size int64) error {
	v := int64(binary.LittleEndian.Uint32(head[4:8]))
	if v < 17 || v > 29 {
		return fmt.Errorf("unsupported vbsp version %d", v)
	}

	// header: magic(4) + version(4) + 64 lumps * 16 bytes + revision(4)
	const headerSize = 8 + sourceLumpCount*16 + 4
	if size < headerSize {
		return fmt.Errorf("truncated vbsp header: file is %d bytes, need %d", size, headerSize)
	}

	lumps := make([]byte, sourceLumpCount*16)
	if _, err := io.ReadFull(r, lumps); err != nil {
		return fmt.Errorf("read lump table: %w", err)
	}

	for i := 0; i < sourceLumpCount; i++ {
		off := int64(binary.LittleEndian.Uint32(lumps[i*16:]))
		ln := int64(binary.LittleEndian.Uint32(lumps[i*16+4:]))

		if ln == 0 {
			continue
		}

		if off+ln > size {
			return fmt.Errorf("lump %d out of bounds: offset=%d length=%d filesize=%d", i, off, ln, size)
		}
	}

	return nil
}

func validateBSPGoldsrc(head [8]byte, r io.Reader, size int64) error {
	v := int64(binary.LittleEndian.Uint32(head[0:4]))
	if v != 29 && v != 30 {
		return fmt.Errorf("unknown bsp header (not VBSP, version field %d)", v)
	}

	// header: version(4) + 15 lumps * 8 bytes.
	const headerSize = 4 + goldsrcLumps*8
	if size < headerSize {
		return fmt.Errorf("truncated goldsrc bsp header: file is %d bytes, need %d", size, headerSize)
	}

	rest := make([]byte, goldsrcLumps*8-4)
	if _, err := io.ReadFull(r, rest); err != nil {
		return fmt.Errorf("read lump table: %w", err)
	}

	lumps := make([]byte, 0, goldsrcLumps*8)
	lumps = append(lumps, head[4:8]...)
	lumps = append(lumps, rest...)
	for i := 0; i < goldsrcLumps; i++ {
		off := int64(binary.LittleEndian.Uint32(lumps[i*8:]))
		ln := int64(binary.LittleEndian.Uint32(lumps[i*8+4:]))

		if ln == 0 {
			continue
		}
		if off+ln > size {
			return fmt.Errorf("lump %d out of bounds: offset=%d length=%d filesize=%d", i, off, ln, size)
		}
	}

	return nil
}
