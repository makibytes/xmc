package cmd

import (
	"context"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/spf13/cobra"
)

// NewSubscribeCommand creates a subscribe command for topic-based brokers.
// When resolver is non-nil, --exchange and --queue flags are registered
// for exchange-routed brokers (e.g. RabbitMQ). Note: -q is already taken
// by --quiet, so long-form only.
func NewSubscribeCommand(backend backends.TopicBackend, resolver TargetResolver, consumeExtra func(*cobra.Command) map[string]string, exchRouting ...bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:   "subscribe <topic>",
		Short: "Subscribe and receive a message from a topic",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doSubscribe(cmd, args, backend, resolver, consumeExtra)
		},
	}

	cmd.Flags().StringP("group", "g", "xmc-consumer-group", "Consumer group ID")
	cmd.Flags().VarP(newDurationValue(100*time.Millisecond, time.Second), "timeout", "t", "Time to wait for a message (e.g. \"100ms\", \"5s\")")
	cmd.Flags().BoolP("quiet", "q", false, "Quiet about properties, show data only")
	cmd.Flags().BoolP("wait", "w", true, "Wait (endless) for a message to arrive")
	cmd.Flags().IntP("count", "n", 1, "Number of messages to receive (0 = until interrupted)")
	cmd.Flags().BoolP("json", "J", false, "Output messages as JSON")
	cmd.Flags().StringP("format", "F", "", "Output format string, e.g. \"%i %s\\n\" (overrides --json)")
	cmd.Flags().Bool("ndjson", false, "Output one lossless JSON record per line (overrides --format/--json)")
	cmd.Flags().StringP("selector", "S", "", "Filter messages by property expression (e.g. \"color='red'\")")
	cmd.Flags().BoolP("durable", "D", false, "Create a durable subscription that survives disconnection")
	cmd.Flags().String("for", "", "Stream for a bounded duration then stop (e.g. \"30s\", \"5m\")")
	cmd.Flags().Bool("forever", false, "Stream until interrupted / until xmc quits (no time bound)")
	cmd.Flags().Bool("stats", false, "Print live throughput statistics to stderr while streaming")

	hasExchRouting := len(exchRouting) > 0 && exchRouting[0]
	if hasExchRouting {
		cmd.Use = "subscribe [--exchange <exchange> [--routing-key <key>] | --queue <queue>] [<to>]"
		cmd.Flags().String("exchange", "", "Exchange to subscribe to (default: amq.topic)")
		cmd.Flags().String("routing-key", "", "Routing key for the exchange (omit for fanout/headers)")
		// Long-form only: -q is --quiet on read commands. --queue-name is the
		// deprecated spelling, kept working via aliasNormalize.
		cmd.Flags().String("queue", "", "Queue to subscribe to (AMQP 1.0 v2: /queues/<name>)")
		cmd.Flags().SetNormalizeFunc(aliasNormalize)
		cmd.Args = cobra.MaximumNArgs(1)
	} else {
		cmd.Args = cobra.MinimumNArgs(1)
	}

	return cmd
}

func doSubscribe(cmd *cobra.Command, args []string, backend backends.TopicBackend, resolver TargetResolver, extraFn func(*cobra.Command) map[string]string) error {
	groupID, _ := cmd.Flags().GetString("group")
	timeout := float32(getDuration(cmd, "timeout").Seconds())
	wait, _ := cmd.Flags().GetBool("wait")
	// --wait defaults to true on subscribe (unlike receive/peek), so an explicit
	// --timeout with no explicit --wait would otherwise be silently ignored.
	// Honour the user's explicit timeout unless they also explicitly asked to wait.
	if cmd.Flags().Changed("timeout") && !cmd.Flags().Changed("wait") {
		wait = false
	}
	quiet, _ := cmd.Flags().GetBool("quiet")
	count, _ := cmd.Flags().GetInt("count")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	selector, _ := cmd.Flags().GetString("selector")
	durable, _ := cmd.Flags().GetBool("durable")
	format, _ := cmd.Flags().GetString("format")
	ndjson, _ := cmd.Flags().GetBool("ndjson")

	sf, err := ParseStreamingFlags(cmd)
	if err != nil {
		return err
	}
	if (sf.Duration > 0 || sf.Forever) && !cmd.Flags().Changed("count") {
		count = 0
	}

	topic, err := resolveConsumeTarget(cmd, args, resolver, true)
	if err != nil {
		return err
	}

	var extra map[string]string
	if extraFn != nil {
		extra = extraFn(cmd)
	}

	opts := backends.SubscribeOptions{
		Topic:       topic,
		GroupID:     groupID,
		Timeout:     timeout,
		Wait:        wait,
		Verbosity:   commandVerbosity(quiet),
		Selector:    selector,
		Durable:     durable,
		Acknowledge: true,
		Extra:       extra,
	}

	parentCtx := cmd.Context()
	return runConsume(func(ctx context.Context) (*backends.Message, error) {
		return backend.Subscribe(ctx, opts)
	}, consumeConfig{
		count:      count,
		jsonOutput: jsonOutput,
		verbosity:  opts.Verbosity,
		format:     format,
		ndjson:     ndjson,
		follow:     sf.Follow,
		dataOut:    cmd.OutOrStdout(),
		metaOut:    cmd.ErrOrStderr(),
	}, sf.Duration, sf.Stats, parentCtx)
}
