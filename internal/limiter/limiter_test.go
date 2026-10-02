package limiter

import (
	"context"
	"testing"
	"time"

	"freshdesk-mcp-connector/internal/clock"
)

func TestBurstThenThrottle(t *testing.T) {
	clk := clock.NewFake(time.Unix(1000, 0))
	b := New(60, 2, clk) // 1 token per second, burst of 2
	ctx := context.Background()

	for i := 0; i < 2; i++ {
		if err := b.Wait(ctx); err != nil {
			t.Fatal(err)
		}
	}
	if got := len(clk.Slept()); got != 0 {
		t.Fatalf("burst should not sleep, slept %d times", got)
	}
	if err := b.Wait(ctx); err != nil {
		t.Fatal(err)
	}
	slept := clk.Slept()
	if len(slept) != 1 || slept[0] < 900*time.Millisecond || slept[0] > 1100*time.Millisecond {
		t.Fatalf("third call should wait about 1s, got %v", slept)
	}
}

func TestRefillOverTime(t *testing.T) {
	tests := []struct {
		name      string
		idle      time.Duration
		wantSleep bool
	}{
		{"no idle time", 0, true},
		{"idle long enough", 2 * time.Second, false},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			clk := clock.NewFake(time.Unix(0, 0))
			b := New(60, 1, clk)
			_ = b.Wait(context.Background()) // drain
			clk.Advance(tc.idle)
			_ = b.Wait(context.Background())
			if got := len(clk.Slept()) > 0; got != tc.wantSleep {
				t.Fatalf("slept=%v want %v", got, tc.wantSleep)
			}
		})
	}
}

func TestDisabledAndCancelled(t *testing.T) {
	var none *Bucket
	if err := none.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if New(0, 5, clock.System{}) != nil {
		t.Fatal("perMinute 0 should disable the limiter")
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	b := New(1, 1, clock.System{})
	_ = b.Wait(context.Background())
	if err := b.Wait(ctx); err == nil {
		t.Fatal("expected context error")
	}
}
