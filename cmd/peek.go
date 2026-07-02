package cmd

import (
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/spf13/cobra"
)

// NewPeekCommand creates a peek command for queue-based brokers.
// Mirrors NewReceiveCommand: the resolver maps bare names to broker addresses
// (Redis key prefixes, Pulsar persistent:// URLs) and exchRouting registers
// the --exchange/--queue flags for exchange-routed brokers (e.g. RabbitMQ).
func NewPeekCommand(backend backends.QueueBackend, resolver TargetResolver, consumeExtra func(*cobra.Command) map[string]string, exchRouting ...bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "peek <queue>",
		Short: "Peek at a message in the queue without removing it (non-destructive read)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doReceive(cmd, args, backend, false, resolver, consumeExtra)
		},
	}

	cmd.Flags().VarP(newDurationValue(100*time.Millisecond, time.Second), "timeout", "t", "Time to wait for a message (e.g. \"100ms\", \"5s\")")
	cmd.Flags().BoolP("quiet", "q", false, "Quiet about properties, show data only")
	cmd.Flags().BoolP("wait", "w", false, "Wait (endless) for a message to arrive")
	cmd.Flags().IntP("count", "n", 1, "Number of messages to peek (0 = all available)")
	cmd.Flags().BoolP("json", "J", false, "Output messages as JSON")
	cmd.Flags().StringP("format", "F", "", "Output format string, e.g. \"%i %s\\n\" (overrides --json)")
	cmd.Flags().Bool("ndjson", false, "Output one lossless JSON record per line (overrides --format/--json)")
	cmd.Flags().StringP("selector", "S", "", "Filter messages by property expression (e.g. \"color='red'\")")
	cmd.Flags().String("for", "", "Stream for a bounded duration then stop (e.g. \"30s\", \"5m\")")
	cmd.Flags().Bool("forever", false, "Stream until interrupted / until xmc quits (no time bound)")
	cmd.Flags().Bool("stats", false, "Print live throughput statistics to stderr while streaming")
	cmd.Flags().IntP("omit", "o", 0, "Skip (offset past) the first N messages before reading")

	hasExchRouting := len(exchRouting) > 0 && exchRouting[0]
	if hasExchRouting {
		cmd.Use = "peek [--exchange <exchange> [--routing-key <key>] | --queue <queue>] [<to>]"
		cmd.Flags().String("exchange", "", "Exchange to peek from")
		cmd.Flags().String("routing-key", "", "Routing key for the exchange (omit for fanout/headers)")
		// Long-form only: -q is --quiet on read commands. --queue-name is the
		// deprecated spelling, kept working via aliasNormalize.
		cmd.Flags().String("queue", "", "Queue to peek from (AMQP 1.0 v2: /queues/<name>)")
		cmd.Flags().SetNormalizeFunc(aliasNormalize)
		cmd.Args = cobra.MaximumNArgs(1)
	} else {
		cmd.Args = cobra.MinimumNArgs(1)
	}

	return cmd
}
