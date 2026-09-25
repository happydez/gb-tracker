package gb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"math/rand"
	"net"
	"net/http"
	"net/url"
	"strconv"
	"time"
)

const (
	defaultSort = "Generic_LatestModified"
	modFields   = "name,description," +
		"Updates().aGetLatestUpdates(),Updates().nUpdatesCount()," +
		"Nsfw().bIsNsfw(),Owner().name,udate,mdate," +
		"Game().name,RootCategory().name,Category().name," +
		"Files().aFiles(),Url().sProfileUrl()," +
		"Preview().sStructuredDataFullsizeUrl()"
)

type Client struct {
	httpClient *http.Client
	cfg        Config
}

func NewClient(cfg Config) *Client {
	tr := &http.Transport{
		Proxy: http.ProxyFromEnvironment,
		DialContext: (&net.Dialer{
			Timeout:   30 * time.Second,
			KeepAlive: 30 * time.Second,
		}).DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		MaxIdleConnsPerHost:   20,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   20 * time.Second,
		ExpectContinueTimeout: 2 * time.Second,
	}

	return &Client{
		httpClient: &http.Client{Transport: tr, Timeout: 0},
		cfg:        cfg,
	}
}

type RecordParams struct {
	PerPage  int
	Sort     string
	Category int
	Page     int
}

func (p RecordParams) values() url.Values {
	if p.PerPage <= 0 {
		p.PerPage = 20
	}
	if p.Sort == "" {
		p.Sort = defaultSort
	}
	if p.Page <= 0 {
		p.Page = 1
	}

	v := url.Values{}
	v.Set("_nPerpage", strconv.Itoa(p.PerPage))
	v.Set("_sSort", p.Sort)
	v.Set("_aFilters[Generic_Category]", strconv.Itoa(p.Category))
	v.Set("_nPage", strconv.Itoa(p.Page))

	return v
}

func (c *Client) FetchRecords(ctx context.Context, params RecordParams) ([]Record, error) {
	var modIndex ModIndex
	reqURL := c.cfg.ModIndexURL + "?" + params.values().Encode()
	err := c.withRetry(ctx, func() error {
		resp, err := c.get(ctx, reqURL)
		if err != nil {
			return fmt.Errorf("fetch records: %w", err)
		}
		defer resp.Body.Close()

		modIndex = ModIndex{}
		body := io.LimitReader(resp.Body, c.cfg.MaxBodyBytes)
		if err := json.NewDecoder(body).Decode(&modIndex); err != nil {
			return fmt.Errorf("decode records: %w", err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return modIndex.Records, nil
}

func (c *Client) FetchMod(ctx context.Context, modID int) (*Mod, error) {
	v := url.Values{}
	v.Set("itemtype", "Mod")
	v.Set("itemid", strconv.Itoa(modID))
	v.Set("fields", modFields)
	v.Set("return_keys", "1")
	v.Set("format", "json")

	reqURL := c.cfg.ModURL + "?" + v.Encode()

	var mod Mod

	err := c.withRetry(ctx, func() error {
		resp, err := c.get(ctx, reqURL)
		if err != nil {
			return fmt.Errorf("fetch mod %d: %w", modID, err)
		}
		defer resp.Body.Close()

		body, err := io.ReadAll(io.LimitReader(resp.Body, c.cfg.MaxBodyBytes))
		if err != nil {
			return &retryableError{err: fmt.Errorf("read body: %w", err)}
		}

		var apiErr struct {
			Error string `json:"error"`
		}
		if json.Unmarshal(body, &apiErr) == nil && apiErr.Error != "" {
			return fmt.Errorf("fetch mod %d: api error: %s", modID, apiErr.Error)
		}

		mod = Mod{}
		if err := json.Unmarshal(body, &mod); err != nil {
			return fmt.Errorf("decode mod %d: %w", modID, err)
		}

		return nil
	})
	if err != nil {
		return nil, err
	}

	return &mod, nil
}

func (c *Client) get(ctx context.Context, reqURL string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, reqURL, nil)
	if err != nil {
		return nil, err
	}

	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", c.cfg.UserAgent)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		return nil, &retryableError{err: err}
	}

	switch {
	case resp.StatusCode == http.StatusOK:
		return resp, nil
	case resp.StatusCode == http.StatusTooManyRequests, resp.StatusCode >= 500:
		retryAfter := parseRetryAfter(resp.Header.Get("Retry-After"))
		_ = resp.Body.Close()
		return nil, &retryableError{
			err:        fmt.Errorf("status %d", resp.StatusCode),
			retryAfter: retryAfter,
		}
	default:
		_ = resp.Body.Close()
		return nil, fmt.Errorf("status %d", resp.StatusCode)
	}
}

// parseRetryAfter reads the Retry-After header, which may hold either a number
// of seconds or an HTTP date. Returns zero when absent or unparseable.
func parseRetryAfter(value string) time.Duration {
	if value == "" {
		return 0
	}

	if seconds, err := strconv.Atoi(value); err == nil && seconds >= 0 {
		return time.Duration(seconds) * time.Second
	}

	if t, err := http.ParseTime(value); err == nil {
		if d := time.Until(t); d > 0 {
			return d
		}
	}

	return 0
}

func (c *Client) withRetry(ctx context.Context, op func() error) error {
	var lastErr error
	for attempt := 0; attempt <= c.cfg.RetryAttempts; attempt++ {
		if err := ctx.Err(); err != nil {
			return err
		}

		err := op()
		if err == nil {
			return nil
		}

		lastErr = err

		var rerr *retryableError
		if !errors.As(err, &rerr) {
			return err
		}

		if attempt == c.cfg.RetryAttempts {
			break
		}

		delay := rerr.retryAfter
		if delay == 0 {
			delay = c.backoff(attempt)
		}

		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(delay):
		}
	}

	if c.cfg.RetryAttempts == 0 {
		return lastErr
	}

	return fmt.Errorf("after %d attempts: %w", c.cfg.RetryAttempts+1, lastErr)
}

// backoff returns an exponentially growing delay with jitter, so that several
// workers retrying at once do not hit the API in lockstep.
func (c *Client) backoff(attempt int) time.Duration {
	delay := float64(c.cfg.BaseDelay) * math.Pow(2, float64(attempt))
	if delay > float64(c.cfg.MaxDelay) {
		delay = float64(c.cfg.MaxDelay)
	}

	half := int64(delay) / 2

	return time.Duration(half + rand.Int63n(half+1))
}

type retryableError struct {
	err        error
	retryAfter time.Duration
}

func (e *retryableError) Error() string {
	return e.err.Error()
}

func (e *retryableError) Unwrap() error {
	return e.err
}
