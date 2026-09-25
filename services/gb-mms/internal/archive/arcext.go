package archive

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"log/slog"
	"os/exec"
	"path/filepath"
	"strings"
)

type arcext struct {
	name      string
	logger    *slog.Logger
	buildArgs func(srcPath string, destPath string) []string
}

// Extract runs the underlying command-line tool to unpack srcPath into
// destPath. Nested archives are not touched: the caller is responsible for
// walking the result and extracting them.
func (a arcext) Extract(ctx context.Context, srcPath string, destPath string) error {
	cmd := exec.CommandContext(ctx, a.name, a.buildArgs(srcPath, destPath)...)
	var stderr bytes.Buffer
	cmd.Stderr = &stderr
	if err := cmd.Run(); err != nil {
		msg := strings.TrimSpace(stderr.String())

		if ctx.Err() != nil {
			return fmt.Errorf("%s %s: %w", a.name, filepath.Base(srcPath), ctx.Err())
		}

		var ee *exec.ExitError
		if errors.As(err, &ee) && ee.ExitCode() == 1 {
			a.logger.Warn("extractor finished with warnings", "tool", a.name, "src", filepath.Base(srcPath), "stderr", msg)
			return nil
		}

		return fmt.Errorf("%s extract %s: %w: %s", a.name, filepath.Base(srcPath), err, msg)
	}

	return nil
}

// new7Zip returns an extractor backed by the 7zz binary. It handles zip, 7z,
// tar, gzip, bzip2, xz, zstd.
func new7Zip(logger *slog.Logger) arcext {
	return arcext{
		name:   "7zz",
		logger: logger,
		buildArgs: func(srcPath string, destPath string) []string {
			return []string{"x", srcPath, "-o" + destPath, "-y", "-bd", "-bso0", "-p"}
		},
	}
}

// newUnrar returns an extractor backed by the unrar binary.
func newUnrar(logger *slog.Logger) arcext {
	return arcext{
		name:   "unrar",
		logger: logger,
		buildArgs: func(srcPath string, destPath string) []string {
			if !strings.HasSuffix(destPath, "/") {
				destPath += "/"
			}
			return []string{"x", "-y", "-p-", "-o+", "-idq", srcPath, destPath}
		},
	}
}
