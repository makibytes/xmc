package cmd

import (
	"context"
	"fmt"

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

	registerConsumeFlags(cmd, false, "Number of messages to receive (0 = drain all available)", true)
	registerConsumeExchangeFlags(cmd, exchRouting,
		"receive [--exchange <exchange> [--routing-key <key>] | --queue <queue>] [<to>]",
		"Exchange to receive from",
		"Queue to receive from (AMQP 1.0 v2: /queues/<name>)")

	return cmd
}

func doReceive(cmd *cobra.Command, args []string, backend backends.QueueBackend, acknowledge bool, resolver TargetResolver, extraFn func(*cobra.Command) map[string]string) error {
	v, err := parseConsumeFlags(cmd, true)
	if err != nil {
		return err
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
		Timeout:     v.timeout,
		Wait:        v.wait,
		Acknowledge: acknowledge,
		Verbosity:   commandVerbosity(v.quiet),
		Selector:    v.selector,
		Extra:       extra,
	}

	cfg := consumeConfig{
		count:      v.count,
		jsonOutput: v.jsonOutput,
		verbosity:  opts.Verbosity,
		format:     v.format,
		ndjson:     v.ndjson,
		follow:     v.streaming.Follow,
		omit:       v.omit,
		dataOut:    cmd.OutOrStdout(),
		metaOut:    cmd.ErrOrStderr(),
	}

	// parentCtx is cmd.Context(), which is cancellable by the AI TUI's Esc
	// handler (via execCancel → ExecuteContext ctx). In the plain shell it is
	// context.Background(), so cancellation relies on SIGINT as before.
	parentCtx := cmd.Context()

	// When peeking (acknowledge=false), open a browse cursor for the whole
	// invocation. backends.Browse uses the backend's native cursor when
	// available (fixing "peek -n 0", which would otherwise repeat the first
	// message forever because a stateless Receive re-reads the queue head
	// every call) and falls back to exactly that stateless Receive loop
	// otherwise, so non-browse brokers are unaffected.
	//
	// In shell/AI mode the backend is always a *reconnectingQueue wrapper
	// (cmd/reconnect.go), which itself implements BrowseBackend by delegating
	// to the underlying adapter.
	if !acknowledge {
		browser, err := backends.Browse(parentCtx, backend, opts)
		if err != nil {
			return err
		}
		defer browser.Close()
		return runConsume(browser.Next, cfg, v.streaming.Duration, v.streaming.Stats, parentCtx)
	}

	return runConsume(func(ctx context.Context) (*backends.Message, error) {
		return backend.Receive(ctx, opts)
	}, cfg, v.streaming.Duration, v.streaming.Stats, parentCtx)
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
	queueName, _ := cmd.Flags().GetString("queue")

	if exchange != "" && queueName != "" {
		return "", fmt.Errorf("--exchange and --queue are mutually exclusive")
	}

	var to string
	switch {
	case queueName != "":
		if len(args) > 0 {
			return "", fmt.Errorf("unexpected argument %q when --queue is specified", args[0])
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
			return "", fmt.Errorf("requires a destination argument, or use --exchange / --queue")
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
