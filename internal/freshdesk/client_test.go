package freshdesk

import (
	"context"
	"encoding/base64"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"sync/atomic"
	"testing"
	"time"

	"freshdesk-mcp-connector/internal/clock"
)

const testKey = "test-key-123"

// newTestClient points a client at handler, with a fake clock so retries are
// instant. Extra functions can adjust the config.
func newTestClient(t *testing.T, h http.Handler, tweak ...func(*Config)) (*Client, *clock.Fake) {
	t.Helper()
	srv := httptest.NewServer(h)
	t.Cleanup(srv.Close)
	clk := clock.NewFake(time.Unix(1_700_000_000, 0))
	cfg := Config{APIKey: testKey, BaseURL: srv.URL + "/api/v2", Clock: clk, MaxRetries: 3,
		BackoffBase: 100 * time.Millisecond, BackoffMax: time.Second}
	for _, f := range tweak {
		f(&cfg)
	}
	c, err := NewClient(cfg)
	if err != nil {
		t.Fatal(err)
	}
	return c, clk
}

func jsonHandler(body string) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		fmt.Fprint(w, body)
	}
}

func TestAuthHeaderUsesKeyAsUsernameAndX(t *testing.T) {
	var gotAuth, gotPath string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		gotAuth, gotPath = r.Header.Get("Authorization"), r.URL.Path
		fmt.Fprint(w, `[]`)
	}))
	if _, err := c.ListTickets(context.Background(), ListOptions{}); err != nil {
		t.Fatal(err)
	}
	want := "Basic " + base64.StdEncoding.EncodeToString([]byte(testKey+":X"))
	if gotAuth != want {
		t.Fatalf("auth header = %q, want %q", gotAuth, want)
	}
	if gotPath != "/api/v2/tickets" {
		t.Fatalf("path = %q", gotPath)
	}
}

func TestBaseURLFromDomain(t *testing.T) {
	tests := []struct{ in, want string }{
		{"acme", "https://acme.freshdesk.com/api/v2"},
		{"Acme.freshdesk.com", "https://acme.freshdesk.com/api/v2"},
		{"https://acme.freshdesk.com/", "https://acme.freshdesk.com/api/v2"},
	}
	for _, tc := range tests {
		c, err := NewClient(Config{Domain: tc.in, APIKey: "k"})
		if err != nil || c.BaseURL() != tc.want {
			t.Errorf("%q -> %v %v, want %s", tc.in, c, err, tc.want)
		}
	}
}

func TestConfigValidation(t *testing.T) {
	tests := []struct {
		name string
		cfg  Config
	}{
		{"no key", Config{Domain: "acme"}},
		{"bad domain", Config{Domain: "evil.com/x", APIKey: "k"}},
		{"no domain", Config{APIKey: "k"}},
		{"plain http to a remote host", Config{BaseURL: "http://example.com/api/v2", APIKey: "k"}},
	}
	for _, tc := range tests {
		if _, err := NewClient(tc.cfg); err == nil {
			t.Errorf("%s: expected an error", tc.name)
		}
	}
	if _, err := NewClient(Config{BaseURL: "http://127.0.0.1:9999/api/v2", APIKey: "k"}); err != nil {
		t.Errorf("http on loopback should be allowed: %v", err)
	}
}

func TestConfigNeverPrintsKey(t *testing.T) {
	cfg := Config{Domain: "acme", APIKey: testKey}
	for _, s := range []string{fmt.Sprintf("%v", cfg), fmt.Sprintf("%+v", cfg), fmt.Sprintf("%#v", cfg), cfg.String()} {
		if strings.Contains(s, testKey) {
			t.Fatalf("key leaked: %s", s)
		}
	}
}

func TestListTicketsQueryAndPagination(t *testing.T) {
	var got []string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		got = append(got, r.URL.RawQuery)
		fmt.Fprint(w, `[{"id":1,"status":2,"priority":3},{"id":2,"status":4,"priority":1}]`)
	}))
	since := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	page, err := c.ListTickets(context.Background(), ListOptions{Page: 2, PerPage: 2, UpdatedSince: since,
		Include: []string{"requester"}, OrderBy: "updated_at", OrderType: "desc"})
	if err != nil {
		t.Fatal(err)
	}
	q := got[0]
	for _, want := range []string{"page=2", "per_page=2", "updated_since=2026-01-02T03%3A04%3A05Z", "include=requester", "order_by=updated_at", "order_type=desc"} {
		if !strings.Contains(q, want) {
			t.Errorf("query %q missing %q", q, want)
		}
	}
	if !page.HasMore || page.Page != 2 || len(page.Tickets) != 2 {
		t.Fatalf("page = %+v", page)
	}
	if page.Tickets[0].StatusLabel != "open" || page.Tickets[0].PriorityLabel != "high" {
		t.Fatalf("labels not set: %+v", page.Tickets[0])
	}
}

func TestListTicketsStatusFilterAndValidation(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(`[{"id":1,"status":2},{"id":2,"status":4},{"id":3,"status":2}]`))
	page, err := c.ListTickets(context.Background(), ListOptions{Status: StatusOpen})
	if err != nil || len(page.Tickets) != 2 {
		t.Fatalf("got %+v %v", page, err)
	}
	if page.HasMore {
		t.Fatal("3 results with per_page 30 means no more pages")
	}
	bad := []ListOptions{{PerPage: 101}, {Page: -1}, {Filter: "nope"}, {OrderBy: "x"}, {Include: []string{"x"}}, {Status: 9}}
	for _, o := range bad {
		if _, err := c.ListTickets(context.Background(), o); !errors.Is(err, ErrInvalid) {
			t.Errorf("%+v: err = %v, want invalid input", o, err)
		}
	}
}

func TestSearchTickets(t *testing.T) {
	var rawQuery string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		rawQuery = r.URL.Query().Get("query")
		fmt.Fprint(w, `{"total":95,"results":[{"id":7,"status":2,"priority":2}]}`)
	}))
	res, err := c.SearchTickets(context.Background(), `status:2 AND tag:'refund'`, 3)
	if err != nil {
		t.Fatal(err)
	}
	if rawQuery != `"status:2 AND tag:'refund'"` {
		t.Fatalf("query sent = %q (must be wrapped in double quotes)", rawQuery)
	}
	if res.Total != 95 || !res.HasMore || res.Page != 3 || res.Tickets[0].StatusLabel != "open" {
		t.Fatalf("res = %+v", res)
	}
}

func TestSearchLimitsAndValidation(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(`{"total":300,"results":[]}`))
	last, err := c.SearchTickets(context.Background(), "status:2", MaxSearchPages)
	if err != nil || last.HasMore {
		t.Fatalf("page 10 must report no more pages: %+v %v", last, err)
	}
	for name, tc := range map[string]struct {
		q    string
		page int
	}{
		"empty":        {"  ", 1},
		"page 11":      {"status:2", 11},
		"negative":     {"status:2", -1},
		"double quote": {`tag:"x"y"`, 1},
		"too long":     {strings.Repeat("a", 501), 1},
	} {
		if _, err := c.SearchTickets(context.Background(), tc.q, tc.page); !errors.Is(err, ErrInvalid) {
			t.Errorf("%s: err = %v", name, err)
		}
	}
}

func TestGetTicketAndConversations(t *testing.T) {
	var paths []string
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		paths = append(paths, r.URL.RequestURI())
		if strings.HasSuffix(r.URL.Path, "/conversations") {
			fmt.Fprint(w, `[{"id":9,"incoming":true,"body_text":"hi"}]`)
			return
		}
		fmt.Fprint(w, `{"id":5,"subject":"s","status":3,"priority":4}`)
	}))
	tk, err := c.GetTicket(context.Background(), 5, true)
	if err != nil || tk.StatusLabel != "pending" || tk.PriorityLabel != "urgent" {
		t.Fatalf("%+v %v", tk, err)
	}
	convs, err := c.ListConversations(context.Background(), 5, 1, 50)
	if err != nil || len(convs) != 1 || !convs[0].Incoming {
		t.Fatalf("%+v %v", convs, err)
	}
	if !strings.Contains(paths[0], "/tickets/5?include=requester%2Cconversations") || !strings.Contains(paths[1], "/tickets/5/conversations?page=1&per_page=50") {
		t.Fatalf("paths = %v", paths)
	}
	if _, err := c.GetTicket(context.Background(), 0, false); !errors.Is(err, ErrInvalid) {
		t.Fatal("id 0 must be invalid")
	}
}

func TestStatusMapping(t *testing.T) {
	tests := []struct {
		status int
		want   error
		calls  int32
	}{
		{401, ErrAuth, 1}, {403, ErrAuth, 1}, {404, ErrNotFound, 1}, {400, ErrBadRequest, 1}, {422, ErrBadRequest, 1},
	}
	for _, tc := range tests {
		t.Run(fmt.Sprint(tc.status), func(t *testing.T) {
			var calls atomic.Int32
			c, clk := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				calls.Add(1)
				w.WriteHeader(tc.status)
				fmt.Fprint(w, `{"description":"nope"}`)
			}))
			_, err := c.GetTicket(context.Background(), 1, false)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			if calls.Load() != tc.calls || len(clk.Slept()) != 0 {
				t.Fatalf("client errors must not be retried: calls=%d", calls.Load())
			}
			if strings.Contains(err.Error(), testKey) {
				t.Fatal("error text leaked the API key")
			}
		})
	}
}

func TestRateLimitHonoursRetryAfterThenSucceeds(t *testing.T) {
	var calls atomic.Int32
	c, clk := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.Header().Set("Retry-After", "2")
			w.Header().Set("X-Ratelimit-Total", "100")
			w.Header().Set("X-Ratelimit-Remaining", "0")
			w.WriteHeader(429)
			return
		}
		w.Header().Set("X-Ratelimit-Total", "100")
		w.Header().Set("X-Ratelimit-Remaining", "98")
		fmt.Fprint(w, `[]`)
	}))
	if _, err := c.ListTickets(context.Background(), ListOptions{}); err != nil {
		t.Fatal(err)
	}
	if s := clk.Slept(); len(s) != 1 || s[0] != 2*time.Second {
		t.Fatalf("slept = %v, want exactly the Retry-After of 2s", s)
	}
	if rl := c.RateLimit(); rl.Total != 100 || rl.Remaining != 98 {
		t.Fatalf("rate limit = %+v", rl)
	}
	if st := c.Stats(); st.UpstreamRequests != 2 || st.Retries != 1 {
		t.Fatalf("stats = %+v", st)
	}
}

func TestRetryAfterHTTPDate(t *testing.T) {
	now := time.Unix(1_700_000_000, 0)
	got := parseRetryAfter(now.Add(5*time.Second).UTC().Format(http.TimeFormat), now)
	if got < 4*time.Second || got > 5*time.Second {
		t.Fatalf("got %v", got)
	}
	for _, bad := range []string{"", "soon", "-3"} {
		if parseRetryAfter(bad, now) != 0 {
			t.Errorf("%q should parse to 0", bad)
		}
	}
}

func TestRateLimitWithHugeRetryAfterReturnsImmediately(t *testing.T) {
	var calls atomic.Int32
	c, clk := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.Header().Set("Retry-After", "3600")
		w.WriteHeader(429)
	}))
	_, err := c.ListTickets(context.Background(), ListOptions{})
	var fe *Error
	if !errors.Is(err, ErrRateLimited) || !errors.As(err, &fe) || fe.RetryAfter != time.Hour {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 1 || len(clk.Slept()) != 0 {
		t.Fatalf("should not sleep an hour: calls=%d slept=%v", calls.Load(), clk.Slept())
	}
}

func TestServerErrorsBackOffWithJitter(t *testing.T) {
	var calls atomic.Int32
	c, clk := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) <= 3 {
			w.WriteHeader(503)
			return
		}
		fmt.Fprint(w, `[]`)
	}))
	if _, err := c.ListTickets(context.Background(), ListOptions{}); err != nil {
		t.Fatal(err)
	}
	slept := clk.Slept()
	if len(slept) != 3 {
		t.Fatalf("slept = %v", slept)
	}
	// Attempt n waits between half and all of base*2^n.
	for i, d := range slept {
		full := 100 * time.Millisecond << i
		if d < full/2 || d > full {
			t.Errorf("sleep %d = %v, want within [%v, %v]", i, d, full/2, full)
		}
	}
}

func TestMaxRetriesExhausted(t *testing.T) {
	var calls atomic.Int32
	c, clk := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		w.WriteHeader(500)
	}), func(c *Config) { c.MaxRetries = 2 })
	_, err := c.GetTicket(context.Background(), 1, false)
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v", err)
	}
	if calls.Load() != 3 || len(clk.Slept()) != 2 {
		t.Fatalf("want 1 try + 2 retries, got calls=%d sleeps=%d", calls.Load(), len(clk.Slept()))
	}
}

func TestRateLimitedErrorAfterRetriesExhausted(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Retry-After", "1")
		w.WriteHeader(429)
	}), func(c *Config) { c.MaxRetries = 1 })
	if _, err := c.GetTicket(context.Background(), 1, false); !errors.Is(err, ErrRateLimited) {
		t.Fatalf("err = %v", err)
	}
}

func TestTimeoutIsAnUpstreamError(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		select {
		case <-r.Context().Done():
		case <-time.After(300 * time.Millisecond):
		}
	}), func(c *Config) { c.Timeout = 30 * time.Millisecond; c.MaxRetries = 0 })
	_, err := c.GetTicket(context.Background(), 1, false)
	if !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v", err)
	}
}

func TestCallerCancellationStopsRetries(t *testing.T) {
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) { w.WriteHeader(500) }))
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	if _, err := c.GetTicket(ctx, 1, false); !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled in the chain", err)
	}
}

func TestClientSideRateLimiter(t *testing.T) {
	c, clk := newTestClient(t, jsonHandler(`[]`), func(c *Config) { c.RequestsPerMinute = 60; c.Burst = 2 })
	for i := 0; i < 4; i++ {
		// A distinct page each time so the (disabled) cache cannot hide requests.
		if _, err := c.ListTickets(context.Background(), ListOptions{Page: i + 1}); err != nil {
			t.Fatal(err)
		}
	}
	if len(clk.Slept()) != 2 {
		t.Fatalf("two requests beyond the burst should each wait: %v", clk.Slept())
	}
}

func TestCircuitBreakerStopsHammeringAndRecovers(t *testing.T) {
	var calls atomic.Int32
	var healthy atomic.Bool
	c, clk := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if !healthy.Load() {
			w.WriteHeader(500)
			return
		}
		fmt.Fprint(w, `{"id":1}`)
	}), func(c *Config) { c.MaxRetries = 0; c.BreakerThreshold = 3; c.BreakerCooldown = 10 * time.Second })

	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := c.GetTicket(ctx, 1, false); !errors.Is(err, ErrUpstream) {
			t.Fatalf("call %d: %v", i, err)
		}
	}
	before := calls.Load()
	for i := 0; i < 5; i++ {
		if _, err := c.GetTicket(ctx, 1, false); !errors.Is(err, ErrUnavailable) {
			t.Fatalf("open breaker should fail fast, got %v", err)
		}
	}
	if calls.Load() != before {
		t.Fatal("no requests should reach the server while the breaker is open")
	}
	healthy.Store(true)
	clk.Advance(11 * time.Second)
	if _, err := c.GetTicket(ctx, 1, false); err != nil {
		t.Fatalf("breaker should recover after the cooldown: %v", err)
	}
}

func TestCacheServesRepeatedCallsWithinTTL(t *testing.T) {
	var calls atomic.Int32
	c, clk := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		fmt.Fprint(w, `{"id":1,"subject":"s"}`)
	}), func(c *Config) { c.CacheTTL = 30 * time.Second })
	ctx := context.Background()
	for i := 0; i < 3; i++ {
		if _, err := c.GetTicket(ctx, 1, false); err != nil {
			t.Fatal(err)
		}
	}
	if calls.Load() != 1 || c.Stats().CacheHits != 2 {
		t.Fatalf("calls=%d stats=%+v", calls.Load(), c.Stats())
	}
	clk.Advance(31 * time.Second)
	_, _ = c.GetTicket(ctx, 1, false)
	if calls.Load() != 2 {
		t.Fatal("entry should expire after the TTL")
	}
}

func TestCachedResultsAreIndependentCopies(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(`{"id":1,"subject":"original"}`), func(c *Config) { c.CacheTTL = time.Minute })
	a, _ := c.GetTicket(context.Background(), 1, false)
	a.Subject = "changed by caller"
	b, _ := c.GetTicket(context.Background(), 1, false)
	if b.Subject != "original" {
		t.Fatal("a caller mutating its copy must not affect later reads")
	}
}

func TestErrorsAreNotCached(t *testing.T) {
	var calls atomic.Int32
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if calls.Add(1) == 1 {
			w.WriteHeader(404)
			return
		}
		fmt.Fprint(w, `{"id":1}`)
	}), func(c *Config) { c.CacheTTL = time.Minute })
	_, _ = c.GetTicket(context.Background(), 1, false)
	if _, err := c.GetTicket(context.Background(), 1, false); err != nil {
		t.Fatalf("second call should hit the server again: %v", err)
	}
}

func TestIdenticalParallelCallsMakeOneRequest(t *testing.T) {
	var calls atomic.Int32
	release := make(chan struct{})
	c, _ := newTestClient(t, http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		<-release
		fmt.Fprint(w, `{"id":1}`)
	}))
	const n = 8
	var wg sync.WaitGroup
	for i := 0; i < n; i++ {
		wg.Add(1)
		go func() {
			defer wg.Done()
			if _, err := c.GetTicket(context.Background(), 1, false); err != nil {
				t.Error(err)
			}
		}()
	}
	deadline := time.Now().Add(2 * time.Second)
	for c.Stats().Coalesced < n-1 && time.Now().Before(deadline) {
		time.Sleep(5 * time.Millisecond)
	}
	close(release)
	wg.Wait()
	if calls.Load() != 1 {
		t.Fatalf("server saw %d requests, want 1", calls.Load())
	}
}

func TestMalformedJSONIsUpstreamError(t *testing.T) {
	c, _ := newTestClient(t, jsonHandler(`<html>oops`))
	if _, err := c.GetTicket(context.Background(), 1, false); !errors.Is(err, ErrUpstream) {
		t.Fatalf("err = %v", err)
	}
}
