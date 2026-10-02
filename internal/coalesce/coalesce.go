// Package coalesce merges identical calls that happen at the same time into
// one. If ten callers ask for the same ticket at once, one request goes out
// and all ten share the answer. (This is the idea behind Go's "singleflight".)
package coalesce

import (
	"context"
	"sync"
)

type call[V any] struct {
	done chan struct{}
	val  V
	err  error
}

// Group's zero value is ready to use.
type Group[V any] struct {
	mu    sync.Mutex
	calls map[string]*call[V]
}

// Do runs fn once per key at a time. The first caller runs fn; callers that
// arrive while it runs wait for the result. shared is true for those waiters.
// A waiter whose own ctx ends stops waiting, but the running call carries on.
func (g *Group[V]) Do(ctx context.Context, key string, fn func() (V, error)) (v V, shared bool, err error) {
	g.mu.Lock()
	if g.calls == nil {
		g.calls = map[string]*call[V]{}
	}
	if c, ok := g.calls[key]; ok {
		g.mu.Unlock()
		select {
		case <-c.done:
			return c.val, true, c.err
		case <-ctx.Done():
			return v, true, ctx.Err()
		}
	}
	c := &call[V]{done: make(chan struct{})}
	g.calls[key] = c
	g.mu.Unlock()

	// Clean up even if fn panics, so waiters are never stuck forever.
	defer func() {
		g.mu.Lock()
		delete(g.calls, key)
		g.mu.Unlock()
		close(c.done)
	}()
	c.val, c.err = fn()
	return c.val, false, c.err
}
