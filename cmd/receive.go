package cmd

import (
	"context"
	"errors"
	"fmt"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/spf13/cobra"
)


// NewReceiveCommand creates a receive command for queue-based brokers.
// When resolver is non-nil, --exchange and --queue flags are registered
// for exchange-routed brokers (e.g. RabbitMQ). Note: -q is already taken
// by --quiet and -e is not available on read commands, so long-form only.
func NewReceiveCommand(backend backends.QueueBackend, resolver TargetResolver, consumeExtra func(*cobra.Command) map[string]string, exchRouting ...bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "receive <queue>",
		Aliases: []string{"get"},
		Short:   "Receive a message from a queue (destructive read)",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doReceive(cmd, args, backend, true, resolver, consumeExtra)
		},
	}

	cmd.Flags().VarP(newDurationValue(100*time.Millisecond, time.Second), "timeout", "t", "Time to wait for a message (e.g. \"100ms\", \"5s\")")
	cmd.Flags().BoolP("quiet", "q", false, "Quiet about properties, show data only")
	cmd.Flags().BoolP("wait", "w", false, "Wait (endless) for a message to arrive")
	cmd.Flags().IntP("count", "n", 1, "Number of messages to receive (0 = drain all available)")
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
		cmd.Use = "receive [--exchange <exchange> [--routing-key <key>] | --queue-name <queue>] [<to>]"
		cmd.Flags().String("exchange", "", "Exchange to receive from")
		cmd.Flags().String("routing-key", "", "Routing key for the exchange (omit for fanout/headers)")
		cmd.Flags().String("queue-name", "", "Queue to receive from (AMQP 1.0 v2: /queues/<name>)")
		cmd.Args = cobra.MaximumNArgs(1)
	} else {
		cmd.Args = cobra.MinimumNArgs(1)
	}

	return cmd
}

func doReceive(cmd *cobra.Command, args []string, backend backends.QueueBackend, acknowledge bool, resolver TargetResolver, extraFn func(*cobra.Command) map[string]string) error {
	timeout := float32(getDuration(cmd, "timeout").Seconds())
	wait, _ := cmd.Flags().GetBool("wait")
	quiet, _ := cmd.Flags().GetBool("quiet")
	count, _ := cmd.Flags().GetInt("count")
	jsonOutput, _ := cmd.Flags().GetBool("json")
	selector, _ := cmd.Flags().GetString("selector")
	format, _ := cmd.Flags().GetString("format")
	ndjson, _ := cmd.Flags().GetBool("ndjson")
	forStr, _ := cmd.Flags().GetString("for")
	forever, _ := cmd.Flags().GetBool("forever")
	stats, _ := cmd.Flags().GetBool("stats")
	omit, _ := cmd.Flags().GetInt("omit")

	duration, err := parseDurationFlag(forStr)
	if err != nil {
		return err
	}
	follow := duration > 0 || forever || stats
	// When streaming by time or --forever, treat as "drain all" unless the user
	// explicitly set -n / --count.
	if (duration > 0 || forever) && !cmd.Flags().Changed("count") {
		count = 0
	}

	queue, err := resolveConsumeTarget(cmd, args, resolver, false)
	if err != nil {
		return err
	}

	var extra map[string]string
	if extraFn != nil {
		extra = extraFn(cmd)
	}

	opts := backends.ReceiveOptions{
		Queue:       queue,
		Timeout:     timeout,
		Wait:        wait,
		Acknowledge: acknowledge,
		Verbosity:   commandVerbosity(quiet),
		Selector:    selector,
		Extra:       extra,
	}

	cfg := consumeConfig{
		count:      count,
		jsonOutput: jsonOutput,
		verbosity:  opts.Verbosity,
		format:     format,
		ndjson:     ndjson,
		follow:     follow,
		omit:       omit,
		dataOut:    cmd.OutOrStdout(),
		metaOut:    cmd.ErrOrStderr(),
	}

	// parentCtx is cmd.Context(), which is cancellable by the AI TUI's Esc
	// handler (via execCancel → ExecuteContext ctx). In the plain shell it is
	// context.Background(), so cancellation relies on SIGINT as before.
	parentCtx := cmd.Context()

	// When peeking (acknowledge=false) and the backend supports stateful
	// browsing, open a single browse cursor for the whole invocation.  This
	// fixes "peek -n 0" which would otherwise repeat the first message forever
	// because each stateless Receive call re-reads the queue head.
	//
	// In shell/AI mode the backend is always a *reconnectingQueue wrapper
	// (cmd/reconnect.go), which itself implements BrowseBackend by delegating
	// to the underlying adapter. If the adapter does not support browsing the
	// wrapper returns backends.ErrBrowseUnsupported — treat that as "fall
	// through" to the normal Receive loop so non-browse brokers are unaffected.
	if !acknowledge {
		if bb, ok := backend.(backends.BrowseBackend); ok {
			browser, err := bb.Browse(parentCtx, opts)
			switch {
			case err == nil:
				defer browser.Close()
				return runConsume(browser.Next, cfg, duration, stats, parentCtx)
			case !errors.Is(err, backends.ErrBrowseUnsupported):
				return err
			// ErrBrowseUnsupported: fall through to the plain Receive loop below
			}
		}
	}

	return runConsume(func(ctx context.Context) (*backends.Message, error) {
		return backend.Receive(ctx, opts)
	}, cfg, duration, stats, parentCtx)
}

// resolveConsumeTarget parses --exchange/--queue/<to> for receive/subscribe
// commands. When resolver is nil, args[0] is the destination.
func resolveConsumeTarget(cmd *cobra.Command, args []string, resolver TargetResolver, isTopic bool) (string, error) {
	if resolver == nil {
		if len(args) < 1 {
			return "", fmt.Errorf("requires at least 1 arg(s), only received %d", len(args))
		}
		return args[0], nil
	}

	exchange, _ := cmd.Flags().GetString("exchange")
	queueName, _ := cmd.Flags().GetString("queue-name")

	if exchange != "" && queueName != "" {
		return "", fmt.Errorf("--exchange and --queue-name are mutually exclusive")
	}

	var to string
	switch {
	case queueName != "":
		if len(args) > 0 {
			return "", fmt.Errorf("unexpected argument %q when --queue-name is specified", args[0])
		}
	case exchange != "":
		routingKey, _ := cmd.Flags().GetString("routing-key")
		if routingKey != "" {
			to = routingKey
		} else if len(args) > 0 {
			to = args[0]
		}
	default:
		if len(args) < 1 {
			return "", fmt.Errorf("requires a destination argument, or use --exchange / --queue-name")
		}
		to = args[0]
	}

	return resolver(TargetSpec{
		IsTopic:  isTopic,
		To:       to,
		Exchange: exchange,
		Queue:    queueName,
	})
}
