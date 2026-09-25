// Package ftp uploads maps to the FastDL host.
package ftp

import (
	"context"
	"fmt"
	"io"
	"os"
	"path"
	"strings"
	"time"

	"github.com/jlaffaye/ftp"
	"golang.org/x/time/rate"
)

type Config struct {
	Addr string

	User     string
	Password string

	// BaseDir prefixes every remote path. Empty means the server root. The rest
	// of the path comes from the caller, which is what puts maps into folders of
	// their own.
	BaseDir string

	// BytesPerSec caps upload throughput. Zero means unlimited.
	BytesPerSec int

	UseTLS bool

	// DisableEPSV forces plain PASV. Some servers and firewalls choke on the
	// extended form.
	DisableEPSV bool

	DialTimeout time.Duration
}

func DefaultConfig() Config {
	return Config{DialTimeout: 10 * time.Second}
}

func (c Config) Validate() error {
	if c.Addr == "" {
		return fmt.Errorf("ftp: addr must not be empty")
	}
	if c.BytesPerSec < 0 {
		return fmt.Errorf("ftp: bytes_per_sec must not be negative")
	}
	if c.DialTimeout <= 0 {
		return fmt.Errorf("ftp: dial_timeout must be positive")
	}

	return nil
}

// Uploader dials the server for each batch rather than holding a connection
// open.
type Uploader struct {
	cfg Config
}

func New(cfg Config) *Uploader {
	return &Uploader{cfg: cfg}
}

// Upload stores one local file under the configured directory.
func (u *Uploader) Upload(ctx context.Context, local, remote string) error {
	conn, err := u.dial(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = conn.Quit()
	}()

	return u.store(ctx, conn, local, remote)
}

// UploadAll sends several files over one connection.
func (u *Uploader) UploadAll(ctx context.Context, files map[string]string) error {
	if len(files) == 0 {
		return nil
	}

	conn, err := u.dial(ctx)
	if err != nil {
		return err
	}
	defer func() {
		_ = conn.Quit()
	}()

	for local, remote := range files {
		if err := ctx.Err(); err != nil {
			return err
		}

		if err := u.store(ctx, conn, local, remote); err != nil {
			return err
		}
	}

	return nil
}

func (u *Uploader) dial(ctx context.Context) (*ftp.ServerConn, error) {
	opts := []ftp.DialOption{
		ftp.DialWithTimeout(u.cfg.DialTimeout),
		ftp.DialWithContext(ctx),
		ftp.DialWithDisabledEPSV(u.cfg.DisableEPSV),
	}

	if u.cfg.UseTLS {
		opts = append(opts, ftp.DialWithExplicitTLS(nil))
	}

	conn, err := ftp.Dial(u.cfg.Addr, opts...)
	if err != nil {
		return nil, fmt.Errorf("dial %s: %w", u.cfg.Addr, err)
	}

	user := u.cfg.User
	if user == "" {
		user = "anonymous"
	}

	if err := conn.Login(user, u.cfg.Password); err != nil {
		_ = conn.Quit()

		return nil, fmt.Errorf("login: %w", err)
	}

	return conn, nil
}

func (u *Uploader) store(ctx context.Context, conn *ftp.ServerConn, local, remote string) error {
	file, err := os.Open(local)
	if err != nil {
		return fmt.Errorf("open %s: %w", local, err)
	}
	defer func() {
		_ = file.Close()
	}()

	full := u.RemotePath(remote)

	if dir := path.Dir(full); dir != "." && dir != "/" {
		mkdirAll(conn, dir)
	}

	var src io.Reader = file
	if u.cfg.BytesPerSec > 0 {
		limiter := rate.NewLimiter(rate.Limit(u.cfg.BytesPerSec), u.cfg.BytesPerSec)
		src = &throttledReader{reader: src, limiter: limiter, ctx: ctx}
	}

	if err := conn.Stor(full, src); err != nil {
		return fmt.Errorf("upload %s: %w", remote, err)
	}

	return nil
}

// RemotePath is where a file ends up on the server. name may carry folders of
// its own, e.g. "bhop/bhop_arena.bsp.bz2".
func (u *Uploader) RemotePath(name string) string {
	return path.Clean("/" + path.Join(u.cfg.BaseDir, name))
}

// mkdirAll walks the path creating each segment. An existing directory comes
// back as an error, which is the normal case and is ignored.
func mkdirAll(conn *ftp.ServerConn, dir string) {
	current := "/"
	for _, p := range strings.Split(strings.TrimPrefix(path.Clean(dir), "/"), "/") {
		if p == "" {
			continue
		}

		current = path.Join(current, p)
		_ = conn.MakeDir(current)
	}
}

// throttledReader limits throughput with a token bucket.
type throttledReader struct {
	reader  io.Reader
	limiter *rate.Limiter
	ctx     context.Context
}

func (r *throttledReader) Read(p []byte) (int, error) {
	if err := r.ctx.Err(); err != nil {
		return 0, err
	}

	if burst := r.limiter.Burst(); len(p) > burst {
		p = p[:burst]
	}

	n, err := r.reader.Read(p)
	if n > 0 {
		if waitErr := r.limiter.WaitN(r.ctx, n); waitErr != nil {
			return n, waitErr
		}
	}

	return n, err
}
