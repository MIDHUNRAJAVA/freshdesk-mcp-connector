// Package breaker is a circuit breaker. After several failures in a row it
// "opens" and refuses calls for a cooldown, so we stop hammering a server
// that is already struggling. After the cooldown it lets one test call
// through; if that works the breaker closes again.
package breaker

import (
	"errors"
	"sync"
	"time"

	"freshdesk-mcp-connector/internal/clock"
)

type State int

const (
	Closed   State = iota // normal: calls flow
	Open                  // failing: calls are refused
	HalfOpen              // testing: one call allowed to see if the server is back
)

func (s State) String() string {
	return [...]string{"closed", "open", "half-open"}[s]
}

// ErrOpen is returned by Allow while the breaker is refusing calls.
var ErrOpen = errors.New("circuit breaker is open")

type Config struct {
	FailureThreshold int           // consecutive failures before opening; <= 0 disables
	Cooldown         time.Duration // how long to stay open
	Clock            clock.Clock
	OnChange         func(from, to State) // optional, for logging
}

type Breaker struct {
	cfg      Config
	mu       sync.Mutex
	state    State
	failures int
	openedAt time.Time
	probing  bool
}

// New returns nil when disabled; a nil *Breaker allows everything.
func New(cfg Config) *Breaker {
	if cfg.FailureThreshold <= 0 {
		return nil
	}
	return &Breaker{cfg: cfg}
}

// Allow says whether a call may go ahead. Every allowed call must be
// followed by exactly one of Success, Failure or Abandon.
func (b *Breaker) Allow() error {
	if b == nil {
		return nil
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	switch b.state {
	case Open:
		if b.cfg.Clock.Now().Sub(b.openedAt) < b.cfg.Cooldown {
			return ErrOpen
		}
		b.set(HalfOpen)
		b.probing = true
		return nil
	case HalfOpen:
		if b.probing {
			return ErrOpen // someone else is already testing
		}
		b.probing = true
	}
	return nil
}

// Success records a healthy response.
func (b *Breaker) Success() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.failures = 0
	b.probing = false
	b.set(Closed)
}

// Failure records a server-side failure (5xx, network error).
func (b *Breaker) Failure() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
	b.failures++
	if b.state == HalfOpen || b.failures >= b.cfg.FailureThreshold {
		b.openedAt = b.cfg.Clock.Now()
		b.set(Open)
	}
}

// Abandon releases a call that says nothing about server health, such as one
// cancelled by the caller.
func (b *Breaker) Abandon() {
	if b == nil {
		return
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	b.probing = false
}

func (b *Breaker) State() State {
	if b == nil {
		return Closed
	}
	b.mu.Lock()
	defer b.mu.Unlock()
	return b.state
}

func (b *Breaker) set(to State) {
	if b.state == to {
		return
	}
	from := b.state
	b.state = to
	if b.cfg.OnChange != nil {
		b.cfg.OnChange(from, to)
	}
}
