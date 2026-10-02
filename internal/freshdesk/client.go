// Package freshdesk is a read-only client for the Freshdesk REST API.
// It has no write methods on purpose. Reliability features (rate limit,
// retries, circuit breaker, cache, coalescing) live here so every caller
// gets them.
package freshdesk

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"io"
	"math/rand/v2"
	"net/http"
	"net/url"
	"strconv"
	"sync/atomic"
	"time"

	"freshdesk-mcp-connector/internal/breaker"
	"freshdesk-mcp-connector/internal/cache"
	"freshdesk-mcp-connector/internal/clock"
	"freshdesk-mcp-connector/internal/coalesce"
	"freshdesk-mcp-connector/internal/limiter"
)

const maxBodyBytes = 8 << 20 // refuse to read more than 8 MiB from one response

// Stats counts what the client did. Handy for tests and debugging.
type Stats struct {
	UpstreamRequests int64 // HTTP requests actually sent
	Retries          int64
	CacheHits        int64
	Coalesced        int64 // calls that shared another call's request
}

type Client struct {
	cfg    Config
	base   string
	auth   string // precomputed "Basic ..." header value; never logged
	http   *http.Client
	clk    clock.Clock
	bucket *limiter.Bucket
	brk    *breaker.Breaker
	cache  *cache.Cache[[]byte]
	group  coalesce.Group[[]byte]

	rlTotal, rlRemaining        atomic.Int64
	requests, retries, hits, co atomic.Int64
}

// NewClient validates the config and builds a client.
func NewClient(cfg Config) (*Client, error) {
	if cfg.APIKey == "" {
		return nil, errors.New("Freshdesk API key is missing")
	}
	base, err := cfg.resolveBaseURL()
	if err != nil {
		return nil, err
	}
	if cfg.Clock == nil {
		cfg.Clock = clock.System{}
	}
	if cfg.HTTPClient == nil {
		cfg.HTTPClient = &http.Client{}
	}
	if cfg.Timeout <= 0 {
		cfg.Timeout = 30 * time.Second
	}
	if cfg.BackoffBase <= 0 {
		cfg.BackoffBase = 500 * time.Millisecond
	}
	if cfg.BackoffMax <= 0 {
		cfg.BackoffMax = 8 * time.Second
	}
	if cfg.MaxRetryAfter <= 0 {
		cfg.MaxRetryAfter = 60 * time.Second
	}
	if cfg.BreakerCooldown <= 0 {
		cfg.BreakerCooldown = 30 * time.Second
	}
	c := &Client{
		cfg:    cfg,
		base:   base,
		auth:   "Basic " + base64.StdEncoding.EncodeToString([]byte(cfg.APIKey+":X")),
		http:   cfg.HTTPClient,
		clk:    cfg.Clock,
		bucket: limiter.New(cfg.RequestsPerMinute, cfg.Burst, cfg.Clock),
		cache:  cache.New[[]byte](cfg.CacheTTL, 256, cfg.Clock),
	}
	c.brk = breaker.New(breaker.Config{
		FailureThreshold: cfg.BreakerThreshold,
		Cooldown:         cfg.BreakerCooldown,
		Clock:            cfg.Clock,
		OnChange:         func(from, to breaker.State) { c.logf("circuit breaker %s -> %s", from, to) },
	})
	return c, nil
}

// BaseURL returns the API root, for display and tests.
func (c *Client) BaseURL() string { return c.base }

// RateLimit returns the allowance Freshdesk reported on its latest response.
func (c *Client) RateLimit() RateLimit {
	return RateLimit{Total: int(c.rlTotal.Load()), Remaining: int(c.rlRemaining.Load())}
}

func (c *Client) Stats() Stats {
	return Stats{c.requests.Load(), c.retries.Load(), c.hits.Load(), c.co.Load()}
}

func (c *Client) logf(format string, args ...any) {
	if c.cfg.Logf != nil {
		c.cfg.Logf(format, args...)
	}
}

// getJSON fetches path and decodes the JSON answer into out.
func (c *Client) getJSON(ctx context.Context, path string, q url.Values, out any) error {
	body, err := c.fetch(ctx, path, q)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(body, out); err != nil {
		return &Error{Kind: KindUpstream, Message: "Freshdesk returned a response we could not read", Err: err}
	}
	return nil
}

// fetch layers cache -> coalescing -> retrying request.
func (c *Client) fetch(ctx context.Context, path string, q url.Values) ([]byte, error) {
	target := c.base + path
	if len(q) > 0 {
		target += "?" + q.Encode()
	}
	if b, ok := c.cache.Get(target); ok {
		c.hits.Add(1)
		return b, nil
	}
	b, shared, err := c.group.Do(ctx, target, func() ([]byte, error) {
		b, err := c.fetchWithRetry(ctx, target)
		if err == nil {
			c.cache.Set(target, b)
		}
		return b, err
	})
	if shared {
		c.co.Add(1)
	}
	if err != nil && !isTyped(err) {
		return nil, ctxError(err)
	}
	return b, err
}

func isTyped(err error) bool {
	var e *Error
	return errors.As(err, &e)
}

// attempt is the outcome of one HTTP request.
type attempt struct {
	status     int
	body       []byte
	retryAfter time.Duration
	err        error // network-level error
}

// fetchWithRetry sends the request, retrying 429, 5xx and network errors with
// exponential backoff plus jitter, up to cfg.MaxRetries extra attempts.
func (c *Client) fetchWithRetry(ctx context.Context, target string) ([]byte, error) {
	for n := 0; ; n++ {
		if err := c.bucket.Wait(ctx); err != nil {
			return nil, ctxError(err)
		}
		if err := c.brk.Allow(); err != nil {
			return nil, &Error{Kind: KindUnavailable, Message: "Freshdesk has been failing, so requests are paused briefly. Try again in a little while.", Err: err}
		}
		res := c.once(ctx, target)
		if res.err != nil && ctx.Err() != nil {
			c.brk.Abandon() // the caller gave up; says nothing about the server
			return nil, ctxError(ctx.Err())
		}
		failure, wait := c.classify(res, n)
		if failure == nil {
			return res.body, nil
		}
		if !isRetryable(failure) || n >= c.cfg.MaxRetries {
			return nil, failure
		}
		if failure.Kind == KindRateLimited && failure.RetryAfter > c.cfg.MaxRetryAfter {
			return nil, failure // waiting that long would stall the agent
		}
		c.retries.Add(1)
		c.logf("retry %d/%d in %s (%s)", n+1, c.cfg.MaxRetries, wait.Round(time.Millisecond), failure.Kind)
		if err := c.clk.Sleep(ctx, wait); err != nil {
			return nil, ctxError(err)
		}
	}
}

func isRetryable(e *Error) bool {
	return e.Kind == KindRateLimited || e.Kind == KindUpstream
}

// classify turns an attempt into (nil, _) on success or a typed error plus how
// long to wait before retrying. It also reports health to the breaker.
func (c *Client) classify(res attempt, n int) (*Error, time.Duration) {
	backoff := c.backoff(n)
	switch {
	case res.err != nil:
		// A per-request timeout counts as a server problem and is retried.
		// A cancelled caller is handled before classify is reached.
		c.brk.Failure()
		return &Error{Kind: KindUpstream, Message: "could not reach Freshdesk", Err: res.err}, backoff
	case res.status >= 200 && res.status < 300:
		c.brk.Success()
		return nil, 0
	case res.status == http.StatusTooManyRequests:
		c.brk.Success() // the server is alive, just busy
		wait := backoff
		if res.retryAfter > 0 {
			wait = res.retryAfter // the server's own instruction beats our guess
		}
		return &Error{Kind: KindRateLimited, Status: res.status, RetryAfter: res.retryAfter,
			Message: "Freshdesk rate limit reached"}, wait
	case res.status >= 500:
		c.brk.Failure()
		return &Error{Kind: KindUpstream, Status: res.status, Message: "Freshdesk had a server error"}, backoff
	}
	c.brk.Success()
	msg := apiMessage(res.body)
	switch res.status {
	case http.StatusUnauthorized, http.StatusForbidden:
		return &Error{Kind: KindAuth, Status: res.status, Message: "credentials were rejected or lack access"}, 0
	case http.StatusNotFound:
		return &Error{Kind: KindNotFound, Status: res.status, Message: "no such resource"}, 0
	}
	return &Error{Kind: KindBadRequest, Status: res.status, Message: msg}, 0
}

// backoff doubles each attempt up to BackoffMax, then keeps a random half of
// it ("jitter") so many clients do not retry at the same instant.
func (c *Client) backoff(n int) time.Duration {
	d := c.cfg.BackoffBase
	for i := 0; i < n && d < c.cfg.BackoffMax; i++ {
		d *= 2
	}
	if d > c.cfg.BackoffMax {
		d = c.cfg.BackoffMax
	}
	half := d / 2
	return half + time.Duration(rand.Int64N(int64(half)+1))
}

// once sends a single request under its own timeout.
func (c *Client) once(ctx context.Context, target string) attempt {
	ctx, cancel := context.WithTimeout(ctx, c.cfg.Timeout)
	defer cancel()
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, target, nil)
	if err != nil {
		return attempt{err: err}
	}
	req.Header.Set("Authorization", c.auth)
	req.Header.Set("Accept", "application/json")
	req.Header.Set("User-Agent", "freshdesk-mcp-connector/0.1")
	c.requests.Add(1)
	resp, err := c.http.Do(req)
	if err != nil {
		return attempt{err: err}
	}
	defer resp.Body.Close()
	c.recordRateLimit(resp.Header)
	body, err := io.ReadAll(io.LimitReader(resp.Body, maxBodyBytes))
	if err != nil {
		return attempt{err: err}
	}
	return attempt{status: resp.StatusCode, body: body, retryAfter: parseRetryAfter(resp.Header.Get("Retry-After"), c.clk.Now())}
}

func (c *Client) recordRateLimit(h http.Header) {
	if v, err := strconv.ParseInt(h.Get("X-Ratelimit-Total"), 10, 64); err == nil {
		c.rlTotal.Store(v)
	}
	if v, err := strconv.ParseInt(h.Get("X-Ratelimit-Remaining"), 10, 64); err == nil {
		c.rlRemaining.Store(v)
	}
}

// parseRetryAfter understands both "120" (seconds) and an HTTP date.
func parseRetryAfter(v string, now time.Time) time.Duration {
	if v == "" {
		return 0
	}
	if secs, err := strconv.Atoi(v); err == nil && secs >= 0 {
		return time.Duration(secs) * time.Second
	}
	if t, err := http.ParseTime(v); err == nil && t.After(now) {
		return t.Sub(now)
	}
	return 0
}

// apiMessage pulls Freshdesk's own explanation out of an error body.
func apiMessage(body []byte) string {
	var e struct {
		Description string `json:"description"`
		Message     string `json:"message"`
	}
	_ = json.Unmarshal(body, &e)
	msg := e.Description
	if msg == "" {
		msg = e.Message
	}
	if msg == "" {
		return "Freshdesk rejected the request"
	}
	if len(msg) > 200 {
		msg = msg[:200]
	}
	return msg
}

func ctxError(err error) error {
	if isTyped(err) {
		return err
	}
	return &Error{Kind: KindUpstream, Message: "request timed out or was cancelled", Err: err}
}
