package coalesce

import (
	"context"
	"errors"
	"sync"
	"sync/atomic"
	"testing"
	"time"
)

func TestParallelCallsShareOneRun(t *testing.T) {
	var g Group[int]
	var runs atomic.Int32
	release := make(chan struct{})
	const callers = 10

	var started, finished sync.WaitGroup
	started.Add(callers)
	finished.Add(callers)
	var sharedCount atomic.Int32
	for i := 0; i < callers; i++ {
		go func() {
			defer finished.Done()
			started.Done()
			v, shared, err := g.Do(context.Background(), "k", func() (int, error) {
				runs.Add(1)
				<-release
				return 42, nil
			})
			if err != nil || v != 42 {
				t.Errorf("v=%d err=%v", v, err)
			}
			if shared {
				sharedCount.Add(1)
			}
		}()
	}
	started.Wait()
	time.Sleep(50 * time.Millisecond) // let every goroutine reach Do
	close(release)
	finished.Wait()

	if runs.Load() != 1 {
		t.Fatalf("fn ran %d times, want 1", runs.Load())
	}
	if sharedCount.Load() != callers-1 {
		t.Fatalf("shared = %d, want %d", sharedCount.Load(), callers-1)
	}
}

func TestErrorsAreSharedAndKeyIsReleased(t *testing.T) {
	var g Group[string]
	boom := errors.New("boom")
	_, _, err := g.Do(context.Background(), "k", func() (string, error) { return "", boom })
	if !errors.Is(err, boom) {
		t.Fatalf("err = %v", err)
	}
	v, shared, err := g.Do(context.Background(), "k", func() (string, error) { return "ok", nil })
	if err != nil || v != "ok" || shared {
		t.Fatalf("second call should run fresh: %q %v %v", v, shared, err)
	}
}

func TestWaiterCanGiveUp(t *testing.T) {
	var g Group[int]
	release := make(chan struct{})
	go g.Do(context.Background(), "k", func() (int, error) { <-release; return 1, nil })
	time.Sleep(30 * time.Millisecond)

	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Millisecond)
	defer cancel()
	_, shared, err := g.Do(ctx, "k", func() (int, error) { return 2, nil })
	if !shared || !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("shared=%v err=%v", shared, err)
	}
	close(release)
}
