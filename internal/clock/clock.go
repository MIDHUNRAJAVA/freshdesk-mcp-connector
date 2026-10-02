// Package clock lets code ask "what time is it" and "wait a bit" through an
// interface, so tests can run instantly and deterministically.
package clock

import (
	"context"
	"sync"
	"time"
)

// Clock is the time source used by the limiter, breaker, cache and client.
type Clock interface {
	Now() time.Time
	// Sleep waits for d, or returns early with the context's error.
	Sleep(ctx context.Context, d time.Duration) error
}

// System is the real clock.
type System struct{}

func (System) Now() time.Time { return time.Now() }

func (System) Sleep(ctx context.Context, d time.Duration) error {
	if d <= 0 {
		return ctx.Err()
	}
	t := time.NewTimer(d)
	defer t.Stop()
	select {
	case <-t.C:
		return nil
	case <-ctx.Done():
		return ctx.Err()
	}
}

// Fake is a clock for tests. Sleep does not wait: it moves time forward and
// remembers how long it was asked to sleep.
type Fake struct {
	mu    sync.Mutex
	now   time.Time
	slept []time.Duration
}

func NewFake(start time.Time) *Fake { return &Fake{now: start} }

func (f *Fake) Now() time.Time {
	f.mu.Lock()
	defer f.mu.Unlock()
	return f.now
}

func (f *Fake) Sleep(ctx context.Context, d time.Duration) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	f.slept = append(f.slept, d)
	f.now = f.now.Add(d)
	return nil
}

// Advance moves time forward without recording a sleep.
func (f *Fake) Advance(d time.Duration) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.now = f.now.Add(d)
}

// Slept returns a copy of every duration passed to Sleep.
func (f *Fake) Slept() []time.Duration {
	f.mu.Lock()
	defer f.mu.Unlock()
	return append([]time.Duration(nil), f.slept...)
}
