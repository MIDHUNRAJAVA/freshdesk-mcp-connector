package config

import (
	"strings"
	"testing"
	"time"
)

func env(m map[string]string) func(string) string { return func(k string) string { return m[k] } }

func TestDefaultsAreSafe(t *testing.T) {
	s, err := Load(env(map[string]string{"FRESHDESK_DOMAIN": "acme", "FRESHDESK_API_KEY": "k"}))
	if err != nil {
		t.Fatal(err)
	}
	if !s.Mask.Enabled {
		t.Fatal("masking must be on by default")
	}
	if s.Mask.Names {
		t.Fatal("name masking is opt-in")
	}
	c := s.Client
	if c.RequestsPerMinute != 40 || c.MaxRetries != 3 || c.CacheTTL != 30*time.Second || c.BreakerThreshold != 5 || s.MaxResultBytes != 40000 {
		t.Fatalf("unexpected defaults: %+v", c)
	}
}

func TestOverrides(t *testing.T) {
	s, err := Load(env(map[string]string{
		"FRESHDESK_DOMAIN": "acme", "FRESHDESK_API_KEY": "k", "FRESHDESK_MASK_PII": "false", "FRESHDESK_MASK_NAMES": "yes",
		"FRESHDESK_REQUESTS_PER_MINUTE": "10", "FRESHDESK_CACHE_TTL": "0s", "FRESHDESK_AUDIT_LOG": "off",
	}))
	if err != nil {
		t.Fatal(err)
	}
	if s.Mask.Enabled || !s.Mask.Names || s.Client.RequestsPerMinute != 10 || s.Client.CacheTTL != 0 || s.AuditLog != "off" {
		t.Fatalf("%+v", s)
	}
}

func TestErrors(t *testing.T) {
	base := map[string]string{"FRESHDESK_DOMAIN": "acme", "FRESHDESK_API_KEY": "k"}
	with := func(k, v string) map[string]string {
		m := map[string]string{}
		for a, b := range base {
			m[a] = b
		}
		m[k] = v
		return m
	}
	tests := []struct {
		name string
		env  map[string]string
		want string
	}{
		{"no key", map[string]string{"FRESHDESK_DOMAIN": "acme"}, "FRESHDESK_API_KEY"},
		{"no domain", map[string]string{"FRESHDESK_API_KEY": "k"}, "FRESHDESK_DOMAIN"},
		{"bad int", with("FRESHDESK_MAX_RETRIES", "many"), "FRESHDESK_MAX_RETRIES"},
		{"bad duration", with("FRESHDESK_TIMEOUT", "soon"), "FRESHDESK_TIMEOUT"},
		{"bad bool", with("FRESHDESK_MASK_PII", "maybe"), "FRESHDESK_MASK_PII"},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			_, err := Load(env(tc.env))
			if err == nil || !strings.Contains(err.Error(), tc.want) {
				t.Fatalf("err = %v, want mention of %s", err, tc.want)
			}
			if strings.Contains(err.Error(), "=k") {
				t.Fatal("error must not echo secrets")
			}
		})
	}
}
