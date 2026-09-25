package archive

import (
	"fmt"
	"io"
	"os"
	"path/filepath"

	"github.com/happydez/gb-mms/internal/bsp"
)

func CollectMaps(maps []bsp.MapFile, outDir string) ([]bsp.MapFile, error) {
	if err := os.MkdirAll(outDir, 0o750); err != nil {
		return nil, err
	}

	result := make([]bsp.MapFile, 0, len(maps))
	for _, m := range maps {
		dest := filepath.Join(outDir, filepath.Base(m.Path))
		if err := copyFile(m.Path, dest); err != nil {
			return nil, fmt.Errorf("collect %s: %w", m.Name, err)
		}

		m.Path = dest
		result = append(result, m)
	}

	return result, nil
}

func copyFile(srcPath string, destPath string) error {
	in, err := os.Open(srcPath)
	if err != nil {
		return err
	}
	defer in.Close()

	out, err := os.Create(destPath)
	if err != nil {
		return err
	}
	defer out.Close()

	if _, err := io.Copy(out, in); err != nil {
		return err
	}

	return out.Close()
}
