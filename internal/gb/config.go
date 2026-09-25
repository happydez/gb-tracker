package gb

import (
	"fmt"
	"time"
)

// GameBanana Config
type Config struct {
	// ModIndexURL is the apiv11 listing endpoint, ModURL the Core/Item/Data
	// detail endpoint. They live on different hosts.
	ModIndexURL string
	ModURL      string

	UserAgent string

	// MaxBodyBytes caps how much of a response is read, so that an unexpected
	// reply cannot exhaust memory.
	MaxBodyBytes int64

	// RetryAttempts is the number of retries after the first try, so 0 means a
	// single attempt and no retrying.
	RetryAttempts int

	// BaseDelay is the first backoff delay, doubling with every attempt up to
	// MaxDelay. A Retry-After header from the server overrides both.
	BaseDelay time.Duration
	MaxDelay  time.Duration
}

// DefaultConfig returns a Config that works against the public API as it is
// today. Callers override what they need and leave the rest.
func DefaultConfig() Config {
	return Config{
		ModIndexURL:   "https://gamebanana.com/apiv11/Mod/Index",
		ModURL:        "https://api.gamebanana.com/Core/Item/Data",
		UserAgent:     "gb-tracker",
		MaxBodyBytes:  100 << 20,
		RetryAttempts: 3,
		BaseDelay:     2 * time.Second,
		MaxDelay:      60 * time.Second,
	}
}

// Validate reports whether the config can be used to build a client.
func (c Config) Validate() error {
	if c.ModIndexURL == "" {
		return fmt.Errorf("gb: mod index url must not be empty")
	}
	if c.ModURL == "" {
		return fmt.Errorf("gb: mod url must not be empty")
	}
	if c.UserAgent == "" {
		return fmt.Errorf("gb: user agent must not be empty")
	}
	if c.MaxBodyBytes <= 0 {
		return fmt.Errorf("gb: max body bytes must be positive, got %d", c.MaxBodyBytes)
	}
	if c.RetryAttempts < 0 {
		return fmt.Errorf("gb: retry attempts must not be negative, got %d", c.RetryAttempts)
	}
	if c.BaseDelay <= 0 {
		return fmt.Errorf("gb: base delay must be positive")
	}
	if c.MaxDelay < c.BaseDelay {
		return fmt.Errorf("gb: max delay (%s) must not be less than base delay (%s)", c.MaxDelay, c.BaseDelay)
	}

	return nil
}
