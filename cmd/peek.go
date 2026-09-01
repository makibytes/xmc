package cmd

import (
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

	registerConsumeFlags(cmd, false, "Number of messages to peek (0 = all available)", true)
	registerConsumeExchangeFlags(cmd, exchRouting,
		"peek [--exchange <exchange> [--routing-key <key>] | --queue <queue>] [<to>]",
		"Exchange to peek from",
		"Queue to peek from (AMQP 1.0 v2: /queues/<name>)")

	return cmd
}
