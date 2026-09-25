// Package rcon runs commands on the game servers once maps are in place.
package rcon

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/gorcon/rcon"
)

type Server struct {
	Address  string
	Password string
}

type Config struct {
	Servers  []Server
	Commands []string
	Timeout  time.Duration
	CmdDelay time.Duration
}

func DefaultConfig() Config {
	return Config{
		Commands: []string{"sm_addmap %s"},
		Timeout:  10 * time.Second,
		CmdDelay: 750 * time.Millisecond,
	}
}

func (c Config) Validate() error {
	if len(c.Servers) == 0 {
		return fmt.Errorf("rcon: at least one server is required")
	}

	for i, s := range c.Servers {
		if s.Address == "" {
			return fmt.Errorf("rcon: servers[%d].address must not be empty", i)
		}
	}

	if len(c.Commands) == 0 {
		return fmt.Errorf("rcon: at least one command is required")
	}

	for i, cmd := range c.Commands {
		if !strings.Contains(cmd, "%s") {
			return fmt.Errorf("rcon: commands[%d] (%q) has no %%s for the map name", i, cmd)
		}
	}

	if c.Timeout <= 0 {
		return fmt.Errorf("rcon: timeout must be positive")
	}

	if c.CmdDelay < 0 {
		return fmt.Errorf("rcon: cmd_delay must not be negative")
	}

	return nil
}

type Result struct {
	Server   string
	Command  string
	Response string
	Err      error
}

type Client struct {
	cfg Config
}

func New(cfg Config) *Client {
	return &Client{cfg: cfg}
}

// AddMaps runs the configured commands for every map on every server.
func (c *Client) AddMaps(ctx context.Context, names []string) error {
	if len(names) == 0 || len(c.cfg.Servers) == 0 {
		return nil
	}

	commands := make([]string, 0, len(names)*len(c.cfg.Commands))
	for _, name := range names {
		for _, tmpl := range c.cfg.Commands {
			commands = append(commands, strings.ReplaceAll(tmpl, "%s", name))
		}
	}

	var failed []string

	for _, r := range c.Broadcast(ctx, commands...) {
		if r.Err != nil {
			failed = append(failed, fmt.Sprintf("%s: %s: %v", r.Server, r.Command, r.Err))
		}
	}

	if len(failed) > 0 {
		return fmt.Errorf("rcon: %s", strings.Join(failed, "; "))
	}

	return nil
}

// Broadcast runs commands on every server, one connection per server.
func (c *Client) Broadcast(ctx context.Context, commands ...string) []Result {
	if len(c.cfg.Servers) == 0 || len(commands) == 0 {
		return nil
	}

	var (
		mu      sync.Mutex
		results []Result
		wg      sync.WaitGroup
	)

	for _, srv := range c.cfg.Servers {
		wg.Go(func() {
			r := c.Exec(ctx, srv, commands...)

			mu.Lock()
			results = append(results, r...)
			mu.Unlock()
		})
	}

	wg.Wait()

	return results
}

// Exec opens one connection and runs the commands in order on it. Source RCON
// is a stateful session, so the commands share a connection rather than each
// authenticating anew.
func (c *Client) Exec(ctx context.Context, srv Server, commands ...string) []Result {
	results := make([]Result, 0, len(commands))

	conn, err := rcon.Dial(srv.Address, srv.Password, rcon.SetDeadline(c.cfg.Timeout))
	if err != nil {
		for _, cmd := range commands {
			results = append(results, Result{Server: srv.Address, Command: cmd, Err: fmt.Errorf("dial: %w", err)})
		}

		return results
	}
	defer func() {
		_ = conn.Close()
	}()

	for i, cmd := range commands {
		if ctxErr := ctx.Err(); ctxErr != nil {
			results = append(results, Result{Server: srv.Address, Command: cmd, Err: ctxErr})

			return results
		}

		resp, execErr := conn.Execute(cmd)
		results = append(results, Result{
			Server:   srv.Address,
			Command:  cmd,
			Response: resp,
			Err:      execErr,
		})

		if i < len(commands)-1 {
			sleep(ctx, c.cfg.CmdDelay)
		}
	}

	return results
}

func sleep(ctx context.Context, d time.Duration) {
	if d <= 0 {
		return
	}

	timer := time.NewTimer(d)
	defer timer.Stop()

	select {
	case <-ctx.Done():
	case <-timer.C:
	}
}
