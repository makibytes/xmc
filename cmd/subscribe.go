package cmd

import (
	"context"

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
	registerConsumeFlags(cmd, true, "Number of messages to receive (0 = until interrupted)", false)
	cmd.Flags().BoolP("durable", "D", false, "Create a durable subscription that survives disconnection")
	registerConsumeExchangeFlags(cmd, exchRouting,
		"subscribe [--exchange <exchange> [--routing-key <key>] | --queue <queue>] [<to>]",
		"Exchange to subscribe to (default: amq.topic)",
		"Queue to subscribe to (AMQP 1.0 v2: /queues/<name>)")

	return cmd
}

func doSubscribe(cmd *cobra.Command, args []string, backend backends.TopicBackend, resolver TargetResolver, extraFn func(*cobra.Command) map[string]string) error {
	groupID, _ := cmd.Flags().GetString("group")
	durable, _ := cmd.Flags().GetBool("durable")

	v, err := parseConsumeFlags(cmd, false)
	if err != nil {
		return err
	}
	// --wait defaults to true on subscribe (unlike receive/peek), so an explicit
	// --timeout with no explicit --wait would otherwise be silently ignored.
	// Honour the user's explicit timeout unless they also explicitly asked to wait.
	if cmd.Flags().Changed("timeout") && !cmd.Flags().Changed("wait") {
		v.wait = false
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
		Timeout:     v.timeout,
		Wait:        v.wait,
		Verbosity:   commandVerbosity(v.quiet),
		Selector:    v.selector,
		Durable:     durable,
		Acknowledge: true,
		Extra:       extra,
	}

	parentCtx := cmd.Context()
	return runConsume(func(ctx context.Context) (*backends.Message, error) {
		return backend.Subscribe(ctx, opts)
	}, consumeConfig{
		count:      v.count,
		jsonOutput: v.jsonOutput,
		verbosity:  opts.Verbosity,
		format:     v.format,
		ndjson:     v.ndjson,
		follow:     v.streaming.Follow,
		dataOut:    cmd.OutOrStdout(),
		metaOut:    cmd.ErrOrStderr(),
	}, v.streaming.Duration, v.streaming.Stats, parentCtx)
}
