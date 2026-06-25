package cmd

import (
	"context"
	"fmt"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/spf13/cobra"
)

// NewSendCommand creates a send command for queue-based brokers.
// When resolver is non-nil, it is used to map the positional <to> (and
// optionally -e/-q flags when exchRouting is true) into a destination.
func NewSendCommand(backend backends.QueueBackend, resolver TargetResolver, produceExtra func(*cobra.Command) map[string]string, exchRouting ...bool) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "send <queue> [message]",
		Aliases: []string{"put"},
		Short:   "Send a message to a queue",
		RunE: func(cmd *cobra.Command, args []string) error {
			return doSend(cmd, args, backend, resolver, produceExtra)
		},
	}

	registerProduceFlags(cmd)

	hasExchRouting := len(exchRouting) > 0 && exchRouting[0]
	if hasExchRouting {
		cmd.Use = "send [-e <exchange> [--routing-key <key>] | -q <queue>] [message]"
		cmd.Flags().StringP("exchange", "e", "", "Exchange to send to")
		cmd.Flags().String("routing-key", "", "Routing key for the exchange (omit for fanout/headers)")
		cmd.Flags().StringP("queue", "q", "", "Queue to send to (AMQP 1.0 v2: /queues/<name>)")
		cmd.Args = cobra.MaximumNArgs(2)
	} else if resolver != nil {
		// Resolver without exchange routing: positional still required.
		cmd.Args = cobra.MinimumNArgs(1)
	} else {
		cmd.Args = cobra.MinimumNArgs(1)
	}

	return cmd
}

func doSend(cmd *cobra.Command, args []string, backend backends.QueueBackend, resolver TargetResolver, extraFn func(*cobra.Command) map[string]string) error {
	pf, err := parseProduceFlags(cmd)
	if err != nil {
		return err
	}

	queue, msgArgs, err := resolveProduceTarget(cmd, args, resolver, false)
	if err != nil {
		return err
	}

	var extra map[string]string
	if extraFn != nil {
		extra = extraFn(cmd)
	}

	emit := func(ctx context.Context, data []byte) error {
		return backend.Send(ctx, backends.SendOptions{
			Queue:         queue,
			Message:       data,
			Properties:    pf.properties,
			MessageID:     pf.messageID,
			CorrelationID: pf.correlationID,
			ReplyTo:       pf.replyTo,
			ContentType:   pf.contentType,
			Priority:      pf.priority,
			Persistent:    pf.persistent,
			TTL:           pf.ttl,
			Extra:         extra,
		})
	}

	emitRecord := func(ctx context.Context, rec messageRecord) error {
		data, err := rec.payload()
		if err != nil {
			return err
		}
		return backend.Send(ctx, backends.SendOptions{
			Queue:         queue,
			Message:       data,
			Properties:    rec.Properties,
			MessageID:     rec.MessageID,
			CorrelationID: rec.CorrelationID,
			ReplyTo:       rec.ReplyTo,
			ContentType:   rec.ContentType,
			Priority:      rec.Priority,
			Persistent:    rec.Persistent,
		})
	}

	// Reconstruct args so that readCommandMessage sees args[1] as the message.
	runArgs := append([]string{queue}, msgArgs...)
	return runProduce(cmd.Context(), cmd.InOrStdin(), runArgs, pf, emit, emitRecord, "sent")
}

// resolveProduceTarget parses -e/-q/<to> for send/publish commands.
// When resolver is nil, args[0] is the destination and args[1:] are message args.
// When resolver is non-nil, the flags and positionals are parsed according to
// the rules documented in the plan. Returns (destination, messageArgs, error).
func resolveProduceTarget(cmd *cobra.Command, args []string, resolver TargetResolver, isTopic bool) (string, []string, error) {
	if resolver == nil {
		// No resolver: positional is the destination.
		if len(args) < 1 {
			return "", nil, fmt.Errorf("requires at least 1 arg(s), only received %d", len(args))
		}
		return args[0], args[1:], nil
	}

	exchange, _ := cmd.Flags().GetString("exchange")
	queue, _ := cmd.Flags().GetString("queue")

	// -e and -q are mutually exclusive.
	if exchange != "" && queue != "" {
		return "", nil, fmt.Errorf("--exchange and --queue are mutually exclusive")
	}

	var to string
	var msgArgs []string

	switch {
	case queue != "":
		// -q <queue>: positional <to> is forbidden; all args are message.
		if len(args) > 1 {
			return "", nil, fmt.Errorf("unexpected argument %q when --queue is specified", args[0])
		}
		msgArgs = args
	case exchange != "":
		// -e <exchange>: routing key comes from --routing-key flag or the legacy
		// two-positional form (<key> <message>). A single positional is the
		// message body (correct for fanout/headers exchanges that take no key).
		routingKey, _ := cmd.Flags().GetString("routing-key")
		switch {
		case routingKey != "":
			to = routingKey
			msgArgs = args
		case len(args) >= 2:
			to = args[0]
			msgArgs = args[1:]
		default:
			to = ""
			msgArgs = args
		}
	default:
		// No flags: first positional is <to> (required), rest is message.
		if len(args) < 1 {
			return "", nil, fmt.Errorf("requires a destination argument, or use --exchange / --queue")
		}
		to = args[0]
		msgArgs = args[1:]
	}

	dest, err := resolver(TargetSpec{
		IsTopic:  isTopic,
		To:       to,
		Exchange: exchange,
		Queue:    queue,
	})
	if err != nil {
		return "", nil, err
	}

	return dest, msgArgs, nil
}
