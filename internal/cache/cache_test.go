package cache

import (
	"testing"
	"time"

	"freshdesk-mcp-connector/internal/clock"
)

func TestExpiry(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	c := New[string](10*time.Second, 10, clk)
	c.Set("a", "1")
	steps := []struct {
		advance time.Duration
		wantHit bool
	}{
		{5 * time.Second, true},
		{4 * time.Second, true},
		{2 * time.Second, false}, // 11s total, past the 10s limit
	}
	for _, s := range steps {
		clk.Advance(s.advance)
		_, ok := c.Get("a")
		if ok != s.wantHit {
			t.Fatalf("after +%v hit=%v want %v", s.advance, ok, s.wantHit)
		}
	}
	if c.Len() != 0 {
		t.Fatal("expired entry should be removed on read")
	}
}

func TestBoundedSize(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	c := New[int](time.Minute, 3, clk)
	for i, k := range []string{"a", "b", "c", "d", "e"} {
		c.Set(k, i)
		clk.Advance(time.Second)
	}
	if c.Len() != 3 {
		t.Fatalf("len = %d, want 3", c.Len())
	}
	if _, ok := c.Get("a"); ok {
		t.Fatal("oldest entry should have been evicted")
	}
	if v, ok := c.Get("e"); !ok || v != 4 {
		t.Fatal("newest entry should be present")
	}
}

func TestDisabled(t *testing.T) {
	c := New[int](0, 5, clock.System{})
	c.Set("a", 1)
	if _, ok := c.Get("a"); ok || c.Len() != 0 {
		t.Fatal("ttl 0 must disable the cache")
	}
}
