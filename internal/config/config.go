// Package config reads settings from environment variables. Secrets come
// only from the environment, never from flags or files, so they do not end
// up in shell history or in a repository.
package config

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"freshdesk-mcp-connector/internal/freshdesk"
	"freshdesk-mcp-connector/internal/mask"
)

// Settings is everything the program needs to start.
type Settings struct {
	Client         freshdesk.Config
	Mask           mask.Masker
	MaxResultBytes int
	AuditLog       string // "" = stderr, "off" = disabled, otherwise a file path
}

// Load reads settings through getenv (os.Getenv in production).
func Load(getenv func(string) string) (Settings, error) {
	r := reader{get: getenv}
	s := Settings{
		Client: freshdesk.Config{
			Domain:            getenv("FRESHDESK_DOMAIN"),
			APIKey:            getenv("FRESHDESK_API_KEY"),
			BaseURL:           getenv("FRESHDESK_BASE_URL"),
			RequestsPerMinute: r.integer("FRESHDESK_REQUESTS_PER_MINUTE", 40),
			Burst:             r.integer("FRESHDESK_BURST", 5),
			MaxRetries:        r.integer("FRESHDESK_MAX_RETRIES", 3),
			Timeout:           r.duration("FRESHDESK_TIMEOUT", 20*time.Second),
			BackoffBase:       r.duration("FRESHDESK_BACKOFF_BASE", 500*time.Millisecond),
			BackoffMax:        r.duration("FRESHDESK_BACKOFF_MAX", 8*time.Second),
			CacheTTL:          r.duration("FRESHDESK_CACHE_TTL", 30*time.Second),
			BreakerThreshold:  r.integer("FRESHDESK_BREAKER_THRESHOLD", 5),
			BreakerCooldown:   r.duration("FRESHDESK_BREAKER_COOLDOWN", 30*time.Second),
		},
		Mask: mask.Masker{
			Enabled: r.boolean("FRESHDESK_MASK_PII", true), // on unless explicitly turned off
			Names:   r.boolean("FRESHDESK_MASK_NAMES", false),
		},
		MaxResultBytes: r.integer("FRESHDESK_MAX_RESULT_BYTES", 40000),
		AuditLog:       getenv("FRESHDESK_AUDIT_LOG"),
	}
	if r.err != nil {
		return s, r.err
	}
	if s.Client.APIKey == "" {
		return s, fmt.Errorf("FRESHDESK_API_KEY is not set")
	}
	if s.Client.Domain == "" && s.Client.BaseURL == "" {
		return s, fmt.Errorf("FRESHDESK_DOMAIN is not set (for example \"acme\" for acme.freshdesk.com)")
	}
	return s, nil
}

// reader collects the first parsing problem so Load stays short.
type reader struct {
	get func(string) string
	err error
}

func (r *reader) fail(name, want, got string) {
	if r.err == nil {
		r.err = fmt.Errorf("%s must be %s, got %q", name, want, got)
	}
}

func (r *reader) integer(name string, def int) int {
	v := strings.TrimSpace(r.get(name))
	if v == "" {
		return def
	}
	n, err := strconv.Atoi(v)
	if err != nil || n < 0 {
		r.fail(name, "a whole number, 0 or more", v)
		return def
	}
	return n
}

func (r *reader) duration(name string, def time.Duration) time.Duration {
	v := strings.TrimSpace(r.get(name))
	if v == "" {
		return def
	}
	d, err := time.ParseDuration(v)
	if err != nil || d < 0 {
		r.fail(name, `a duration such as "30s" or "500ms"`, v)
		return def
	}
	return d
}

func (r *reader) boolean(name string, def bool) bool {
	v := strings.ToLower(strings.TrimSpace(r.get(name)))
	switch v {
	case "":
		return def
	case "1", "true", "yes", "on":
		return true
	case "0", "false", "no", "off":
		return false
	}
	r.fail(name, "true or false", v)
	return def
}
