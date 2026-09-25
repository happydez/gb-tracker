package ftp

import (
	"testing"
	"time"
)

func TestRemotePath(t *testing.T) {
	tests := []struct {
		name    string
		baseDir string
		remote  string
		want    string
	}{
		{name: "routed into a folder", baseDir: "/fastdl/maps", remote: "bhop/bhop_a.bsp.bz2", want: "/fastdl/maps/bhop/bhop_a.bsp.bz2"},
		{name: "no folder", baseDir: "/fastdl/maps", remote: "bhop_a.bsp.bz2", want: "/fastdl/maps/bhop_a.bsp.bz2"},
		{name: "no base", remote: "bhop/bhop_a.bsp.bz2", want: "/bhop/bhop_a.bsp.bz2"},
		{name: "server root", remote: "bhop_a.bsp.bz2", want: "/bhop_a.bsp.bz2"},
		{name: "messy separators", baseDir: "/fastdl/", remote: "/bhop/bhop_a.bsp.bz2", want: "/fastdl/bhop/bhop_a.bsp.bz2"},
		{name: "empty name is the base directory", baseDir: "/fastdl/maps", want: "/fastdl/maps"},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			u := New(Config{BaseDir: tt.baseDir})

			if got := u.RemotePath(tt.remote); got != tt.want {
				t.Errorf("RemotePath(%q) = %q, want %q", tt.remote, got, tt.want)
			}
		})
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{name: "valid", mutate: func(c *Config) { c.Addr = "127.0.0.1:21" }},
		{name: "no addr", mutate: func(*Config) {}, wantErr: true},
		{
			name:    "negative throughput",
			mutate:  func(c *Config) { c.Addr = "127.0.0.1:21"; c.BytesPerSec = -1 },
			wantErr: true,
		},
		{
			name:    "zero dial timeout",
			mutate:  func(c *Config) { c.Addr = "127.0.0.1:21"; c.DialTimeout = 0 },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConfig()
			tt.mutate(&cfg)

			err := cfg.Validate()
			if tt.wantErr && err == nil {
				t.Fatal("validate: expected an error")
			}
			if !tt.wantErr && err != nil {
				t.Fatalf("validate: %v", err)
			}
		})
	}
}

func TestUploadAllEmptyIsNoop(t *testing.T) {
	u := New(Config{Addr: "127.0.0.1:1", DialTimeout: time.Millisecond})
	if err := u.UploadAll(t.Context(), nil); err != nil {
		t.Errorf("UploadAll: %v, want a no-op", err)
	}
}

func TestUploadFailsWithoutServer(t *testing.T) {
	u := New(Config{Addr: "127.0.0.1:1", DialTimeout: 200 * time.Millisecond})
	if err := u.Upload(t.Context(), "nonexistent", "bhop_a.bsp.bz2"); err == nil {
		t.Fatal("Upload: expected an error with nothing listening")
	}
}
