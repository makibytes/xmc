package cmd

import (
	"context"
	"fmt"
	"io"
	"sync/atomic"
	"time"

	"github.com/spf13/cobra"
)

// streamContext returns a context that is cancelled on interrupt (Ctrl-C) and,
// when duration > 0, also after that wall-clock duration elapses. It underpins
// the streaming features: --for bounds a stream by time, while the interrupt
// handler lets an unbounded stream stop cleanly and print its summary.
//
// An optional parent context may be supplied so that external cancellation
// (e.g. from the AI TUI's Esc handler) also propagates into the consume loop.
// Pass nil or omit to use context.Background() as the parent.
//
// The returned CancelFunc must be called (typically via defer).
func streamContext(duration time.Duration, parents ...context.Context) (context.Context, context.CancelFunc) {
	var parent context.Context
	if len(parents) > 0 && parents[0] != nil {
		parent = parents[0]
	}
	ctx, stop := interruptContext(parent)
	if duration <= 0 {
		return ctx, stop
	}
	timed, cancelTimed := context.WithTimeout(ctx, duration)
	return timed, func() {
		cancelTimed()
		stop()
	}
}

// StreamingFlags holds the parsed values of the --for, --forever, and
// --stats flags, plus a convenience Follow field.
type StreamingFlags struct {
	Duration time.Duration
	Forever  bool
	Stats    bool
	Follow   bool
}

// ParseStreamingFlags reads the --for, --forever, and --stats flags from cmd
// and returns a StreamingFlags value. When --forever is set, Duration is
// forced to 0 regardless of --for.
func ParseStreamingFlags(cmd *cobra.Command) (StreamingFlags, error) {
	forStr, _ := cmd.Flags().GetString("for")
	forever, _ := cmd.Flags().GetBool("forever")
	stats, _ := cmd.Flags().GetBool("stats")

	duration, err := parseDurationFlag(forStr)
	if err != nil {
		return StreamingFlags{}, err
	}
	if forever {
		duration = 0
	}
	follow := duration > 0 || forever || stats
	return StreamingFlags{
		Duration: duration,
		Forever:  forever,
		Stats:    stats,
		Follow:   follow,
	}, nil
}

// parseDurationFlag parses a --for value. An empty string means "no limit".
func parseDurationFlag(value string) (time.Duration, error) {
	if value == "" {
		return 0, nil
	}
	d, err := time.ParseDuration(value)
	if err != nil {
		return 0, fmt.Errorf("invalid --for duration %q: %w", value, err)
	}
	if d < 0 {
		return 0, fmt.Errorf("--for duration must not be negative: %q", value)
	}
	return d, nil
}

// streamStats accumulates message counts and byte totals for the --stats option.
// Counters are atomic so a background reporter goroutine can read them while the
// consume/forward loop updates them.
type streamStats struct {
	count atomic.Int64
	bytes atomic.Int64
	start time.Time
}

func newStreamStats() *streamStats {
	return &streamStats{start: time.Now()}
}

func (s *streamStats) record(payloadLen int) {
	s.count.Add(1)
	s.bytes.Add(int64(payloadLen))
}

// summary returns the one-line totals printed when a stream ends.
func (s *streamStats) summary() string {
	elapsed := time.Since(s.start)
	count := s.count.Load()
	secs := elapsed.Seconds()
	rate := 0.0
	if secs > 0 {
		rate = float64(count) / secs
	}
	return fmt.Sprintf("[stats] done: %d msgs in %s (%.0f msg/s, %s)",
		count, elapsed.Round(time.Millisecond), rate, humanBytes(s.bytes.Load()))
}

// startStatsReporter periodically prints throughput to w until the returned
// stop function is called. Callers pass cmd.ErrOrStderr() (or equivalent) so
// that the output is captured when running as a background process in the TUI
// rather than leaking to the raw terminal.
func startStatsReporter(s *streamStats, interval time.Duration, w io.Writer) (stop func()) {
	stopCh := make(chan struct{})
	go func() {
		ticker := time.NewTicker(interval)
		defer ticker.Stop()
		lastCount := int64(0)
		lastTime := time.Now()
		for {
			select {
			case <-stopCh:
				return
			case now := <-ticker.C:
				count := s.count.Load()
				secs := now.Sub(lastTime).Seconds()
				rate := 0.0
				if secs > 0 {
					rate = float64(count-lastCount) / secs
				}
				fmt.Fprintf(w, "[stats] %d msgs, %.0f msg/s, %s total\n",
					count, rate, humanBytes(s.bytes.Load()))
				lastCount = count
				lastTime = now
			}
		}
	}()
	return func() { close(stopCh) }
}

// humanBytes renders a byte count in a compact human-readable form.
func humanBytes(n int64) string {
	const unit = 1024
	if n < unit {
		return fmt.Sprintf("%d B", n)
	}
	div, exp := int64(unit), 0
	for v := n / unit; v >= unit; v /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f %cB", float64(n)/float64(div), "KMGTPE"[exp])
}
