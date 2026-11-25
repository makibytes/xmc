package cmd

import (
	"context"
	"testing"
	"time"
)

func TestRateLimiter_Disabled(t *testing.T) {
	r := newRateLimiter(0)
	start := time.Now()
	for range 5 {
		_ = r.wait(context.Background())
	}
	if elapsed := time.Since(start); elapsed > 50*time.Millisecond {
		t.Errorf("disabled limiter should not block, took %v", elapsed)
	}
}

func TestRateLimiter_Throttles(t *testing.T) {
	// 100 msg/s => ~10ms interval. First wait is immediate, the next four add
	// ~40ms total. Assert a conservative lower bound to avoid flakiness.
	r := newRateLimiter(100)
	start := time.Now()
	for range 5 {
		_ = r.wait(context.Background())
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Errorf("expected throttling to take >=30ms, took %v", elapsed)
	}
}

// A slow rate must not make cancellation wait out the interval.
func TestRateLimiter_WaitIsInterruptible(t *testing.T) {
	r := newRateLimiter(0.1) // one message per 10 s
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Millisecond)
	defer cancel()

	if err := r.wait(ctx); err != nil { // first call is immediate
		t.Fatalf("first wait: %v", err)
	}
	start := time.Now()
	err := r.wait(ctx)
	if err == nil {
		t.Fatal("expected the cancelled context's error")
	}
	if elapsed := time.Since(start); elapsed > 2*time.Second {
		t.Errorf("wait ignored cancellation for %v", elapsed)
	}
}
