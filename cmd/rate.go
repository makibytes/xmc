package cmd

import "time"

// rateLimiter paces message production to approximately a target number of
// messages per second, used by send/publish to drive controlled load. A rate of
// zero (or less) disables limiting, so wait becomes a no-op.
//
// It schedules on fixed intervals rather than sleeping a flat duration after
// each send, so the achieved rate does not drift due to the time spent inside
// each Send/Publish call.
type rateLimiter struct {
	interval time.Duration
	next     time.Time
}

// newRateLimiter returns a limiter for ratePerSec messages per second. A
// non-positive rate yields a disabled limiter.
func newRateLimiter(ratePerSec float64) *rateLimiter {
	if ratePerSec <= 0 {
		return &rateLimiter{}
	}
	return &rateLimiter{interval: time.Duration(float64(time.Second) / ratePerSec)}
}

// wait blocks until the next scheduled slot. The first call returns immediately.
func (r *rateLimiter) wait() {
	if r.interval <= 0 {
		return
	}
	now := time.Now()
	if r.next.IsZero() {
		r.next = now
	}
	if delay := r.next.Sub(now); delay > 0 {
		time.Sleep(delay)
	}
	r.next = r.next.Add(r.interval)
}
