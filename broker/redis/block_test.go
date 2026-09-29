//go:build redis

package redis

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/redis/go-redis/v9"
)

// A read blocked on an idle stream must notice cancellation promptly even
// when its overall block is effectively unbounded (--wait = 24h): go-redis
// itself never interrupts a blocking read on cancellation.
func TestBlockingRead_CancelInterruptsLongBlock(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	time.AfterFunc(50*time.Millisecond, cancel)

	start := time.Now()
	_, err := blockingRead(ctx, 24*time.Hour, func(block time.Duration) (int, error) {
		if block > maxBlockSlice || block < time.Millisecond {
			t.Errorf("slice %v outside [1ms, %v]", block, maxBlockSlice)
		}
		time.Sleep(block) // an idle stream: the whole slice passes without data
		return 0, redis.Nil
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("err = %v, want context.Canceled", err)
	}
	if d := time.Since(start); d > 2*maxBlockSlice {
		t.Errorf("cancellation noticed after %v, want ≤ %v", d, 2*maxBlockSlice)
	}
}

func TestBlockingRead_TimesOutWithRedisNil(t *testing.T) {
	calls := 0
	_, err := blockingRead(context.Background(), 30*time.Millisecond, func(block time.Duration) (int, error) {
		calls++
		time.Sleep(block)
		return 0, redis.Nil
	})
	if err != redis.Nil { //nolint:errorlint
		t.Fatalf("err = %v, want redis.Nil once the total timeout is used up", err)
	}
	if calls == 0 {
		t.Error("read never called")
	}
}

func TestBlockingRead_ReturnsDataAndRealErrors(t *testing.T) {
	n := 0
	got, err := blockingRead(context.Background(), time.Hour, func(time.Duration) (int, error) {
		n++
		if n < 3 {
			return 0, redis.Nil
		}
		return 42, nil
	})
	if err != nil || got != 42 {
		t.Fatalf("got %d, %v; want 42, nil", got, err)
	}

	boom := errors.New("boom")
	if _, err := blockingRead(context.Background(), time.Hour, func(time.Duration) (int, error) { return 0, boom }); !errors.Is(err, boom) {
		t.Fatalf("err = %v, want boom", err)
	}
}

// The context's deadline caps the total block (a --for bound shorter than
// the per-read timeout).
func TestBlockingRead_RespectsContextDeadline(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Millisecond)
	defer cancel()
	start := time.Now()
	_, err := blockingRead(ctx, time.Hour, func(block time.Duration) (int, error) {
		time.Sleep(block)
		return 0, redis.Nil
	})
	if err == nil {
		t.Fatal("expected an error after the deadline")
	}
	if d := time.Since(start); d > 300*time.Millisecond {
		t.Errorf("returned after %v, want shortly after the 40ms deadline", d)
	}
}
