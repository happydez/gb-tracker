// Package download fetches mod archives from GameBanana.
package download

import (
	"context"
	"crypto/md5"
	"encoding/hex"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"
)

type Config struct {
	UserAgent  string
	ChunkBytes int
	Timeout    time.Duration
	Attempts   int
	BaseDelay  time.Duration
	MaxDelay   time.Duration
}

func DefaultConfig() Config {
	return Config{
		UserAgent:  "gb-mms/1.0",
		ChunkBytes: 5 << 20,
		Timeout:    10 * time.Minute,
		Attempts:   3,
		BaseDelay:  2 * time.Second,
		MaxDelay:   60 * time.Second,
	}
}

func (c Config) Validate() error {
	if c.UserAgent == "" {
		return fmt.Errorf("download: user agent must not be empty")
	}
	if c.ChunkBytes <= 0 {
		return fmt.Errorf("download: chunk bytes must be positive")
	}
	if c.Timeout <= 0 {
		return fmt.Errorf("download: timeout must be positive")
	}
	if c.Attempts < 0 {
		return fmt.Errorf("download: attempts must not be negative")
	}
	if c.BaseDelay <= 0 {
		return fmt.Errorf("download: base delay must be positive")
	}
	if c.MaxDelay < c.BaseDelay {
		return fmt.Errorf("download: max delay must not be less than base delay")
	}

	return nil
}

type Downloader struct {
	cfg    Config
	client *http.Client
}

func New(cfg Config) *Downloader {
	return &Downloader{
		cfg:    cfg,
		client: &http.Client{Timeout: cfg.Timeout},
	}
}

// Fetch downloads url into dest, verifying md5 when it is given.
func (d *Downloader) Fetch(ctx context.Context, url, dest, wantMD5 string) error {
	if err := os.MkdirAll(filepath.Dir(dest), 0o750); err != nil {
		return fmt.Errorf("create download dir: %w", err)
	}

	part := dest + ".part"

	var lastErr error

	for attempt := 0; attempt <= d.cfg.Attempts; attempt++ {
		if attempt > 0 {
			select {
			case <-ctx.Done():
				return ctx.Err()
			case <-time.After(d.backoff(attempt - 1)):
			}
		}

		err := d.fetchOnce(ctx, url, part, dest, wantMD5)
		if err == nil {
			return nil
		}

		if ctx.Err() != nil {
			return ctx.Err()
		}

		lastErr = err
	}

	return fmt.Errorf("after %d attempts: %w", d.cfg.Attempts+1, lastErr)
}

func (d *Downloader) fetchOnce(ctx context.Context, url, part, dest, wantMD5 string) error {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return err
	}

	req.Header.Set("User-Agent", d.cfg.UserAgent)

	resp, err := d.client.Do(req)
	if err != nil {
		return err
	}
	defer func() {
		_, _ = io.Copy(io.Discard, resp.Body)
		_ = resp.Body.Close()
	}()

	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("unexpected status %d", resp.StatusCode)
	}

	written, sum, err := writeToPart(part, resp.Body, d.cfg.ChunkBytes)
	if err != nil {
		_ = os.Remove(part)
		return err
	}

	if resp.ContentLength > 0 && written != resp.ContentLength {
		_ = os.Remove(part)
		return fmt.Errorf("incomplete body: got %d of %d bytes", written, resp.ContentLength)
	}

	if wantMD5 != "" && !strings.EqualFold(sum, wantMD5) {
		_ = os.Remove(part)
		return fmt.Errorf("md5 mismatch: got %s, want %s", sum, wantMD5)
	}

	if err := os.Rename(part, dest); err != nil {
		_ = os.Remove(part)
		return fmt.Errorf("rename: %w", err)
	}

	return nil
}

func (d *Downloader) backoff(attempt int) time.Duration {
	delay := float64(d.cfg.BaseDelay) * math.Pow(2, float64(attempt))
	if delay > float64(d.cfg.MaxDelay) {
		delay = float64(d.cfg.MaxDelay)
	}

	half := int64(delay) / 2

	return time.Duration(half + rand.Int63n(half+1))
}

func writeToPart(part string, src io.Reader, bufSize int) (written int64, sum string, err error) {
	out, err := os.Create(part)
	if err != nil {
		return 0, "", err
	}

	hash := md5.New()
	buf := make([]byte, bufSize)

	written, err = io.CopyBuffer(io.MultiWriter(out, hash), src, buf)
	if closeErr := out.Close(); err == nil {
		err = closeErr
	}
	if err != nil {
		return 0, "", err
	}

	return written, hex.EncodeToString(hash.Sum(nil)), nil
}
