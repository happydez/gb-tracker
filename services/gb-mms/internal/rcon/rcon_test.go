package rcon

import (
	"strings"
	"testing"
	"time"
)

func validConfig() Config {
	cfg := DefaultConfig()
	cfg.Servers = []Server{{Address: "127.0.0.1:27015", Password: "secret"}}

	return cfg
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{name: "valid", mutate: func(*Config) {}},
		{name: "no servers", mutate: func(c *Config) { c.Servers = nil }, wantErr: true},
		{
			name:    "server without an address",
			mutate:  func(c *Config) { c.Servers = []Server{{Password: "x"}} },
			wantErr: true,
		},
		{name: "no commands", mutate: func(c *Config) { c.Commands = nil }, wantErr: true},
		{
			name:    "command without a placeholder",
			mutate:  func(c *Config) { c.Commands = []string{"sm_reloadadmins"} },
			wantErr: true,
		},
		{name: "zero timeout", mutate: func(c *Config) { c.Timeout = 0 }, wantErr: true},
		{name: "negative delay", mutate: func(c *Config) { c.CmdDelay = -1 }, wantErr: true},
		{name: "zero delay is allowed", mutate: func(c *Config) { c.CmdDelay = 0 }},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := validConfig()
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

func TestAddMapsNoopWithoutWork(t *testing.T) {
	c := New(validConfig())

	if err := c.AddMaps(t.Context(), nil); err != nil {
		t.Errorf("AddMaps(nil): %v, want a no-op", err)
	}

	empty := validConfig()
	empty.Servers = nil

	if err := New(empty).AddMaps(t.Context(), []string{"bhop_a"}); err != nil {
		t.Errorf("AddMaps with no servers: %v, want a no-op", err)
	}
}

func TestAddMapsReportsEveryFailure(t *testing.T) {
	cfg := validConfig()
	cfg.Servers = []Server{{Address: "127.0.0.1:1"}, {Address: "127.0.0.1:2"}}
	cfg.Commands = []string{"sm_addmap %s", "sm_map %s"}
	cfg.Timeout = 200 * time.Millisecond
	cfg.CmdDelay = 0

	err := New(cfg).AddMaps(t.Context(), []string{"bhop_a", "bhop_b"})
	if err == nil {
		t.Fatal("AddMaps: expected an error with nothing listening")
	}

	msg := err.Error()

	for _, want := range []string{"127.0.0.1:1", "127.0.0.1:2", "sm_addmap bhop_a", "sm_map bhop_b"} {
		if !strings.Contains(msg, want) {
			t.Errorf("error is missing %q:\n%s", want, msg)
		}
	}
}

func TestBroadcastBuildsPerServerResults(t *testing.T) {
	cfg := validConfig()
	cfg.Servers = []Server{{Address: "127.0.0.1:1"}, {Address: "127.0.0.1:2"}}
	cfg.Timeout = 200 * time.Millisecond

	results := New(cfg).Broadcast(t.Context(), "sm_addmap bhop_a")

	if len(results) != 2 {
		t.Fatalf("got %d results, want one per server", len(results))
	}

	for _, r := range results {
		if r.Err == nil {
			t.Errorf("server %s reported success with nothing listening", r.Server)
		}
	}
}

func TestBroadcastNoop(t *testing.T) {
	c := New(validConfig())

	if got := c.Broadcast(t.Context()); got != nil {
		t.Errorf("Broadcast with no commands = %v, want nil", got)
	}
}
