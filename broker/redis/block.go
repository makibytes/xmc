//go:build redis

package redis

import (
	"context"
	"time"

	"github.com/redis/go-redis/v9"
)

// maxBlockSlice bounds a single blocking stream read (XREAD/XREADGROUP BLOCK).
// go-redis applies a context's deadline to the socket but never its
// cancellation, so one long BLOCK (--wait blocks for 24h) ignored every way
// of stopping a consumer — Esc in the AI shell, killing a background
// process, a relay being shut down — until it elapsed. Re-issuing the read
// in short slices bounds how late a cancellation is noticed.
const maxBlockSlice = 500 * time.Millisecond

// blockingRead calls read with BLOCK slices of at most maxBlockSlice until it
// yields data or a real error, total elapses, or ctx ends. read must return
// redis.Nil when its slice expired without data, which is also what
// blockingRead returns once total (or ctx's deadline) is used up — so callers
// keep mapping redis.Nil to "no message available".
func blockingRead[T any](ctx context.Context, total time.Duration, read func(block time.Duration) (T, error)) (T, error) {
	var zero T
	deadline := time.Now().Add(total)
	if d, ok := ctx.Deadline(); ok && d.Before(deadline) {
		deadline = d
	}
	for {
		if err := ctx.Err(); err != nil {
			return zero, err
		}
		slice := min(time.Until(deadline), maxBlockSlice)
		if slice <= 0 {
			return zero, redis.Nil
		}
		// BLOCK has millisecond resolution and "BLOCK 0" means forever.
		slice = max(slice, time.Millisecond)
		res, err := read(slice)
		if err == redis.Nil { //nolint:errorlint // go-redis returns the sentinel unwrapped
			continue
		}
		return res, err
	}
}
