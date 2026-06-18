package cmd

import (
	"context"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/spf13/cobra"
)

// NewSendCommand creates a send command for queue-based brokers
func NewSendCommand(backend backends.QueueBackend) *cobra.Command {
	cmd := &cobra.Command{
		Use:     "send <queue> [message]",
		Aliases: []string{"put"},
		Short:   "Send a message to a queue",
		Args:    cobra.MinimumNArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			return doSend(cmd, args, backend)
		},
	}

	registerProduceFlags(cmd)

	return cmd
}

func doSend(cmd *cobra.Command, args []string, backend backends.QueueBackend) error {
	pf, err := parseProduceFlags(cmd)
	if err != nil {
		return err
	}

	queue := args[0]

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

	return runProduce(cmd, args, pf, emit, emitRecord, "sent")
}
