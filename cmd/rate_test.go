package cmd

import (
	"testing"
	"time"
)

func TestRateLimiter_Disabled(t *testing.T) {
	r := newRateLimiter(0)
	start := time.Now()
	for i := 0; i < 5; i++ {
		r.wait()
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
	for i := 0; i < 5; i++ {
		r.wait()
	}
	if elapsed := time.Since(start); elapsed < 30*time.Millisecond {
		t.Errorf("expected throttling to take >=30ms, took %v", elapsed)
	}
}
