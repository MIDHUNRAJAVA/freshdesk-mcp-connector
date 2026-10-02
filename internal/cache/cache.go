// Package cache is a tiny in-memory cache whose entries expire. It stops the
// agent from asking Freshdesk the same question twice in a few seconds.
package cache

import (
	"sync"
	"time"

	"freshdesk-mcp-connector/internal/clock"
)

type item[V any] struct {
	value   V
	expires time.Time
}

// Cache is safe for concurrent use. A nil *Cache never stores anything.
type Cache[V any] struct {
	mu    sync.Mutex
	ttl   time.Duration
	max   int
	clk   clock.Clock
	items map[string]item[V]
}

// New returns nil when ttl <= 0 (caching off). max bounds memory use.
func New[V any](ttl time.Duration, max int, clk clock.Clock) *Cache[V] {
	if ttl <= 0 {
		return nil
	}
	if max <= 0 {
		max = 256
	}
	return &Cache[V]{ttl: ttl, max: max, clk: clk, items: map[string]item[V]{}}
}

func (c *Cache[V]) Get(key string) (V, bool) {
	var zero V
	if c == nil {
		return zero, false
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	it, ok := c.items[key]
	if !ok {
		return zero, false
	}
	if !c.clk.Now().Before(it.expires) {
		delete(c.items, key)
		return zero, false
	}
	return it.value, true
}

func (c *Cache[V]) Set(key string, v V) {
	if c == nil {
		return
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	if _, exists := c.items[key]; !exists && len(c.items) >= c.max {
		c.makeRoom()
	}
	c.items[key] = item[V]{value: v, expires: c.clk.Now().Add(c.ttl)}
}

func (c *Cache[V]) Len() int {
	if c == nil {
		return 0
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.items)
}

// makeRoom drops expired entries first, then the entry closest to expiring.
// A linear scan is fine because max is small.
func (c *Cache[V]) makeRoom() {
	now := c.clk.Now()
	var oldestKey string
	var oldest time.Time
	for k, it := range c.items {
		if !now.Before(it.expires) {
			delete(c.items, k)
			continue
		}
		if oldestKey == "" || it.expires.Before(oldest) {
			oldestKey, oldest = k, it.expires
		}
	}
	if len(c.items) >= c.max && oldestKey != "" {
		delete(c.items, oldestKey)
	}
}
