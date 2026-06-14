package cmd

import (
	"fmt"
	"time"

	"github.com/spf13/cobra"
)

// Closeable is satisfied by both QueueBackend and TopicBackend (each exposes
// Close). It lets the ping command accept a broker connection without caring
// whether the broker is queue- or topic-oriented.
type Closeable interface {
	Close() error
}

// Connector establishes a fresh connection to the broker, performing whatever
// authentication and TLS handshake the broker requires. Each broker wires this
// to its adapter constructor.
type Connector func() (Closeable, error)

// NewPingCommand creates a ping command that verifies broker connectivity. It is
// broker-agnostic: connecting (and, for most brokers, completing the auth/TLS
// handshake) is itself the health signal, mirroring tools like redis-cli ping.
//
// The command exits non-zero if any attempt fails, which makes it suitable for
// readiness checks and CI pipelines.
func NewPingCommand(connect Connector) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "ping",
		Short: "Check connectivity to the broker",
		Long: `Connects to the broker (including authentication and TLS handshake where
applicable) and reports whether it succeeds, with the round-trip time.

By default a single attempt is made. Use --count for repeated probes and
--interval to space them out. The command exits non-zero if any attempt fails.`,
		Args: cobra.NoArgs,
		RunE: func(c *cobra.Command, args []string) error {
			return doPing(c, connect)
		},
	}

	cmd.Flags().IntP("count", "n", 1, "Number of attempts (0 = until interrupted)")
	cmd.Flags().VarP(newDurationValue(time.Second, time.Second), "interval", "i", "Time between attempts (e.g. \"1s\", \"500ms\")")

	return cmd
}

func doPing(c *cobra.Command, connect Connector) error {
	count, _ := c.Flags().GetInt("count")
	interval := getDuration(c, "interval")
	target, _ := c.Flags().GetString("server") // inherited persistent flag

	label := target
	if label == "" {
		label = "broker"
	}
	fmt.Printf("PING %s\n", label)

	ctx, stop := interruptContext()
	defer stop()

	var ok, failed int
	var min, max, sum time.Duration

	for seq := 1; count <= 0 || seq <= count; seq++ {
		start := time.Now()
		conn, err := connect()
		elapsed := time.Since(start)

		if err != nil {
			failed++
			fmt.Printf("connect failed: seq=%d error=%v\n", seq, err)
		} else {
			_ = conn.Close()
			ok++
			if ok == 1 || elapsed < min {
				min = elapsed
			}
			if elapsed > max {
				max = elapsed
			}
			sum += elapsed
			fmt.Printf("connected: seq=%d time=%s\n", seq, elapsed.Round(time.Microsecond))
		}

		if count > 0 && seq >= count {
			break
		}

		select {
		case <-ctx.Done():
		case <-time.After(interval):
		}
		if ctx.Err() != nil {
			break
		}
	}

	total := ok + failed
	fmt.Printf("--- %s ping statistics ---\n", label)
	if ok > 0 {
		avg := sum / time.Duration(ok)
		fmt.Printf("%d attempt(s), %d ok, %d failed, rtt min/avg/max = %s/%s/%s\n",
			total, ok, failed,
			min.Round(time.Microsecond), avg.Round(time.Microsecond), max.Round(time.Microsecond))
	} else {
		fmt.Printf("%d attempt(s), %d ok, %d failed\n", total, ok, failed)
	}

	if failed > 0 {
		return fmt.Errorf("%d of %d ping(s) failed", failed, total)
	}
	return nil
}
