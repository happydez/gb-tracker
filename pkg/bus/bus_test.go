package bus

import (
	"testing"
	"time"
)

func TestDefaultConfigIsValid(t *testing.T) {
	if err := DefaultConfig().Validate(); err != nil {
		t.Fatalf("the default config is invalid: %v", err)
	}
}

func TestConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*Config)
		wantErr bool
	}{
		{name: "default", mutate: func(*Config) {}},
		{name: "empty url", mutate: func(c *Config) { c.URL = "" }, wantErr: true},
		{name: "empty stream", mutate: func(c *Config) { c.Stream = "" }, wantErr: true},
		{name: "no subjects", mutate: func(c *Config) { c.Subjects = nil }, wantErr: true},
		{name: "zero max age", mutate: func(c *Config) { c.MaxAge = 0 }, wantErr: true},
		{name: "zero duplicate window", mutate: func(c *Config) { c.DuplicateWindow = 0 }, wantErr: true},
		{
			name:    "duplicate window beyond max age",
			mutate:  func(c *Config) { c.DuplicateWindow = c.MaxAge + time.Hour },
			wantErr: true,
		},
		{name: "zero connect timeout", mutate: func(c *Config) { c.ConnectTimeout = 0 }, wantErr: true},
		{name: "zero publish timeout", mutate: func(c *Config) { c.PublishTimeout = 0 }, wantErr: true},
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

func TestConnectRejectsInvalidConfig(t *testing.T) {
	cfg := DefaultConfig()
	cfg.Stream = ""
	if _, err := Connect(t.Context(), cfg, testLogger()); err == nil {
		t.Fatal("connect: expected an error for an invalid config")
	}
}

func TestConnectFailsWithoutServer(t *testing.T) {
	cfg := DefaultConfig()
	cfg.URL = "nats://127.0.0.1:1"
	cfg.ConnectTimeout = 200 * time.Millisecond
	if _, err := Connect(t.Context(), cfg, testLogger()); err == nil {
		t.Fatal("connect: expected an error when no broker is reachable")
	}
}

func TestConsumerConfigValidate(t *testing.T) {
	tests := []struct {
		name    string
		mutate  func(*ConsumerConfig)
		wantErr bool
	}{
		{name: "default", mutate: func(*ConsumerConfig) {}},
		{name: "no durable", mutate: func(c *ConsumerConfig) { c.Durable = "" }, wantErr: true},
		{name: "no subjects", mutate: func(c *ConsumerConfig) { c.Subjects = nil }, wantErr: true},
		{name: "zero max deliver", mutate: func(c *ConsumerConfig) { c.MaxDeliver = 0 }, wantErr: true},
		{name: "no backoff", mutate: func(c *ConsumerConfig) { c.BackOff = nil }, wantErr: true},
		{
			name:    "first backoff step below a second",
			mutate:  func(c *ConsumerConfig) { c.BackOff[0] = 100 * time.Millisecond },
			wantErr: true,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			cfg := DefaultConsumerConfig("test", "mods.>")
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

func TestAckWaitFollowsBackOff(t *testing.T) {
	cfg := DefaultConsumerConfig("test", "mods.>")

	if got := ackWait(cfg); got != cfg.BackOff[0] {
		t.Errorf("ack wait = %s, want the first backoff step %s", got, cfg.BackOff[0])
	}

	cfg.BackOff = nil
	if got := ackWait(cfg); got != DefaultAckWait {
		t.Errorf("ack wait = %s, want the default %s with no backoff", got, DefaultAckWait)
	}
}
