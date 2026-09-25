package gb

import (
	"context"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"
)

func testConfig(srv *httptest.Server) Config {
	cfg := DefaultConfig()
	cfg.ModIndexURL = srv.URL
	cfg.ModURL = srv.URL
	cfg.UserAgent = "gb-tracker-test"
	cfg.BaseDelay = time.Millisecond
	cfg.MaxDelay = 10 * time.Millisecond

	return cfg
}

func TestFetchRecords(t *testing.T) {
	data := loadData(t, "mod_index.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		q := r.URL.Query()
		if got := q.Get("_aFilters[Generic_Category]"); got != "5568" {
			t.Errorf("category: got %q, want 5568", got)
		}
		if got := q.Get("_nPerpage"); got != "15" {
			t.Errorf("per page: got %q, want 15", got)
		}
		if got := r.Header.Get("User-Agent"); got != "gb-tracker-test" {
			t.Errorf("user agent: got %q", got)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	c := NewClient(testConfig(srv))
	records, err := c.FetchRecords(context.Background(), RecordParams{PerPage: 15, Category: 5568})
	if err != nil {
		t.Fatalf("fetch records: %v", err)
	}
	if len(records) == 0 {
		t.Fatal("no records returned")
	}
}

func TestFetchRecordsOn500(t *testing.T) {
	data := loadData(t, "mod_index.json")

	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls < 3 {
			w.WriteHeader(http.StatusInternalServerError)
			return
		}
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	c := NewClient(testConfig(srv))
	if _, err := c.FetchRecords(context.Background(), RecordParams{}); err != nil {
		t.Fatalf("expected success after retries: %v", err)
	}
	if calls != 3 {
		t.Errorf("got %d calls, want 3", calls)
	}
}

func TestFetchRecordsDoesNotRetryOn404(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		w.WriteHeader(http.StatusNotFound)
	}))
	defer srv.Close()

	c := NewClient(testConfig(srv))
	if _, err := c.FetchRecords(context.Background(), RecordParams{}); err == nil {
		t.Fatal("expected error for 404")
	}
	if calls != 1 {
		t.Errorf("got %d calls, want 1: a 404 must not be retried", calls)
	}
}

func TestRespectsRetryAfter(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		if calls == 1 {
			w.Header().Set("Retry-After", "1")
			w.WriteHeader(http.StatusTooManyRequests)
			return
		}
		_, _ = w.Write([]byte(`{"_aRecords":[]}`))
	}))
	defer srv.Close()

	c := NewClient(testConfig(srv))
	start := time.Now()
	if _, err := c.FetchRecords(context.Background(), RecordParams{}); err != nil {
		t.Fatalf("fetch: %v", err)
	}
	if elapsed := time.Since(start); elapsed < time.Second {
		t.Errorf("elapsed %v, expected at least 1s from Retry-After", elapsed)
	}
}

func TestFetchModAPIError(t *testing.T) {
	var calls int
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls++
		_, _ = w.Write([]byte(`{"error":"Item not found"}`))
	}))
	defer srv.Close()

	c := NewClient(testConfig(srv))
	if _, err := c.FetchMod(context.Background(), 12345); err == nil {
		t.Fatal("expected error for api error payload")
	}
	if calls != 1 {
		t.Errorf("got %d calls, want 1: an api error must not be retried", calls)
	}
}

func TestFetchMod(t *testing.T) {
	data := loadData(t, "mod_data.json")
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.URL.Query().Get("itemid"); got != "498415" {
			t.Errorf("itemid: got %q", got)
		}
		_, _ = w.Write(data)
	}))
	defer srv.Close()

	c := NewClient(testConfig(srv))
	mod, err := c.FetchMod(context.Background(), 498415)
	if err != nil {
		t.Fatalf("fetch mod: %v", err)
	}
	if mod.Name != "kz_bhop_genkai" {
		t.Errorf("name: got %q", mod.Name)
	}
	if len(mod.Files) != 1 {
		t.Errorf("files: got %d, want 1", len(mod.Files))
	}
}

func TestContextCancellation(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
	}))
	defer srv.Close()

	c := NewClient(testConfig(srv))
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()

	if _, err := c.FetchRecords(ctx, RecordParams{}); err == nil {
		t.Fatal("expected error on cancelled context")
	}
}
