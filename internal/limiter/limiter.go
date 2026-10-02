// Package limiter is a token bucket: a small allowance of requests that
// refills at a steady rate. It keeps us under Freshdesk's per-minute limit
// instead of finding out by being rejected.
package limiter

import (
	"context"
	"sync"
	"time"

	"freshdesk-mcp-connector/internal/clock"
)

// Bucket hands out one token per request. A nil *Bucket means "no limit".
type Bucket struct {
	mu       sync.Mutex
	capacity float64
	tokens   float64
	perSec   float64
	last     time.Time
	clk      clock.Clock
}

// New makes a bucket that refills perMinute tokens per minute and can hold
// up to burst tokens. perMinute <= 0 disables limiting (returns nil).
func New(perMinute, burst int, clk clock.Clock) *Bucket {
	if perMinute <= 0 {
		return nil
	}
	if burst <= 0 {
		burst = 1
	}
	return &Bucket{
		capacity: float64(burst),
		tokens:   float64(burst), // start full so the first calls are instant
		perSec:   float64(perMinute) / 60,
		last:     clk.Now(),
		clk:      clk,
	}
}

// Wait blocks until a token is available or ctx ends.
func (b *Bucket) Wait(ctx context.Context) error {
	if b == nil {
		return ctx.Err()
	}
	for {
		wait, ok := b.take()
		if ok {
			return nil
		}
		if err := b.clk.Sleep(ctx, wait); err != nil {
			return err
		}
	}
}

// take removes a token if there is one, otherwise says how long to wait.
func (b *Bucket) take() (time.Duration, bool) {
	b.mu.Lock()
	defer b.mu.Unlock()
	now := b.clk.Now()
	b.tokens += now.Sub(b.last).Seconds() * b.perSec
	if b.tokens > b.capacity {
		b.tokens = b.capacity
	}
	b.last = now
	if b.tokens >= 1 {
		b.tokens--
		return 0, true
	}
	missing := 1 - b.tokens
	return time.Duration(missing / b.perSec * float64(time.Second)), false
}
