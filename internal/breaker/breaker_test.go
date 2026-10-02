package breaker

import (
	"testing"
	"time"

	"freshdesk-mcp-connector/internal/clock"
)

func newTestBreaker(clk clock.Clock) *Breaker {
	return New(Config{FailureThreshold: 3, Cooldown: 10 * time.Second, Clock: clk})
}

func TestOpensAfterThresholdAndRecovers(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	b := newTestBreaker(clk)

	for i := 0; i < 3; i++ {
		if err := b.Allow(); err != nil {
			t.Fatalf("call %d should be allowed: %v", i, err)
		}
		b.Failure()
	}
	if b.State() != Open {
		t.Fatalf("state = %v, want open", b.State())
	}
	if err := b.Allow(); err != ErrOpen {
		t.Fatalf("open breaker should refuse, got %v", err)
	}

	clk.Advance(11 * time.Second)
	if err := b.Allow(); err != nil {
		t.Fatalf("after cooldown one probe is allowed: %v", err)
	}
	if b.State() != HalfOpen {
		t.Fatalf("state = %v, want half-open", b.State())
	}
	if err := b.Allow(); err != ErrOpen {
		t.Fatal("only one probe at a time")
	}
	b.Success()
	if b.State() != Closed {
		t.Fatalf("state = %v, want closed", b.State())
	}
}

func TestFailedProbeReopens(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	b := newTestBreaker(clk)
	for i := 0; i < 3; i++ {
		_ = b.Allow()
		b.Failure()
	}
	clk.Advance(11 * time.Second)
	_ = b.Allow()
	b.Failure()
	if b.State() != Open {
		t.Fatalf("state = %v, want open", b.State())
	}
	if err := b.Allow(); err != ErrOpen {
		t.Fatal("cooldown should restart after a failed probe")
	}
}

func TestSuccessResetsFailureCount(t *testing.T) {
	b := newTestBreaker(clock.NewFake(time.Unix(0, 0)))
	for i := 0; i < 10; i++ {
		_ = b.Allow()
		b.Failure()
		_ = b.Allow()
		b.Failure()
		_ = b.Allow()
		b.Success()
	}
	if b.State() != Closed {
		t.Fatal("non-consecutive failures must not open the breaker")
	}
}

func TestAbandonReleasesProbe(t *testing.T) {
	clk := clock.NewFake(time.Unix(0, 0))
	b := newTestBreaker(clk)
	for i := 0; i < 3; i++ {
		_ = b.Allow()
		b.Failure()
	}
	clk.Advance(11 * time.Second)
	_ = b.Allow()
	b.Abandon()
	if err := b.Allow(); err != nil {
		t.Fatalf("a new probe should be allowed after abandon: %v", err)
	}
}

func TestDisabledAndOnChange(t *testing.T) {
	var none *Breaker
	if none.Allow() != nil || none.State() != Closed {
		t.Fatal("nil breaker must allow everything")
	}
	none.Failure()
	none.Success()
	none.Abandon()

	var changes []string
	clk := clock.NewFake(time.Unix(0, 0))
	b := New(Config{FailureThreshold: 1, Cooldown: time.Second, Clock: clk,
		OnChange: func(from, to State) { changes = append(changes, from.String()+">"+to.String()) }})
	_ = b.Allow()
	b.Failure()
	clk.Advance(2 * time.Second)
	_ = b.Allow()
	b.Success()
	want := []string{"closed>open", "open>half-open", "half-open>closed"}
	if len(changes) != 3 || changes[0] != want[0] || changes[1] != want[1] || changes[2] != want[2] {
		t.Fatalf("changes = %v", changes)
	}
}
