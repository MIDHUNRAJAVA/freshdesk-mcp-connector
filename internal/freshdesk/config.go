package freshdesk

import (
	"fmt"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"strings"
	"time"

	"freshdesk-mcp-connector/internal/clock"
)

// Config holds everything the client needs. Zero values mean "use a sensible
// default", except where noted.
type Config struct {
	Domain  string // "acme" for acme.freshdesk.com
	APIKey  string // never printed, see String
	BaseURL string // overrides Domain; used for the fake server and tests

	RequestsPerMinute int // client-side limit; 0 = no limit
	Burst             int // how many requests may go out back to back
	MaxRetries        int // retries after the first attempt, on 429 / 5xx / network errors
	Timeout           time.Duration
	BackoffBase       time.Duration
	BackoffMax        time.Duration
	MaxRetryAfter     time.Duration // longer server waits are returned as an error, not slept

	CacheTTL         time.Duration // 0 = no cache
	BreakerThreshold int           // 0 = no circuit breaker
	BreakerCooldown  time.Duration

	HTTPClient *http.Client
	Clock      clock.Clock
	Logf       func(format string, args ...any) // optional; must never receive secrets
}

// String hides the API key so a stray log line cannot leak it.
func (c Config) String() string {
	key := "unset"
	if c.APIKey != "" {
		key = "[redacted]"
	}
	return fmt.Sprintf("freshdesk.Config{domain=%q base_url=%q api_key=%s}", c.Domain, c.BaseURL, key)
}

// GoString covers the %#v verb for the same reason.
func (c Config) GoString() string { return c.String() }

var domainPattern = regexp.MustCompile(`^[a-z0-9][a-z0-9-]*$`)

// NormalizeDomain accepts "acme", "acme.freshdesk.com" or a full URL.
func NormalizeDomain(d string) (string, error) {
	d = strings.ToLower(strings.TrimSpace(d))
	d = strings.TrimPrefix(d, "https://")
	d = strings.TrimSuffix(d, "/")
	d = strings.TrimSuffix(d, ".freshdesk.com")
	if !domainPattern.MatchString(d) {
		return "", fmt.Errorf("invalid Freshdesk domain %q: use just the subdomain, for example \"acme\"", d)
	}
	return d, nil
}

// resolveBaseURL builds the API root. The key is sent on every request, so
// plain http is only allowed for a local fake server.
func (c Config) resolveBaseURL() (string, error) {
	if c.BaseURL == "" {
		d, err := NormalizeDomain(c.Domain)
		if err != nil {
			return "", err
		}
		return "https://" + d + ".freshdesk.com/api/v2", nil
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || u.Host == "" {
		return "", fmt.Errorf("invalid base URL")
	}
	if u.Scheme != "https" && !(u.Scheme == "http" && isLoopback(u.Hostname())) {
		return "", fmt.Errorf("base URL must use https (http is allowed only for localhost)")
	}
	return strings.TrimSuffix(c.BaseURL, "/"), nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
