//go:build ibmmq

package ibmmq

import (
	"context"
	"strings"

	"github.com/ibm-messaging/mq-golang/v5/ibmmq"
	"github.com/makibytes/xmc/broker/backends"
)

// QueueAdapter adapts IBM MQ to the QueueBackend interface
type QueueAdapter struct {
	connArgs ConnArguments
	qMgr     ibmmq.MQQueueManager
}

// NewQueueAdapter creates a new IBM MQ queue adapter
func NewQueueAdapter(connArgs ConnArguments) (*QueueAdapter, error) {
	qMgr, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}

	return &QueueAdapter{
		connArgs: connArgs,
		qMgr:     qMgr,
	}, nil
}

// Send implements backends.QueueBackend
func (a *QueueAdapter) Send(ctx context.Context, opts backends.SendOptions) error {
	var persistence int
	if opts.Persistent {
		persistence = 1
	}

	args := SendArguments{
		Queue:         opts.Queue,
		Message:       opts.Message,
		Properties:    backends.StringifyProps(opts.Properties),
		MessageID:     opts.MessageID,
		CorrelationID: opts.CorrelationID,
		ReplyTo:       opts.ReplyTo,
		ContentType:   opts.ContentType,
		Priority:      opts.Priority,
		Persistence:   persistence,
		TTL:           opts.TTL,
	}

	return SendMessage(a.qMgr, args)
}

// Receive implements backends.QueueBackend
func (a *QueueAdapter) Receive(ctx context.Context, opts backends.ReceiveOptions) (*backends.Message, error) {
	args := ReceiveArguments{
		Queue:       opts.Queue,
		Timeout:     opts.Timeout,
		Wait:        opts.Wait,
		Acknowledge: opts.Acknowledge,
		Selector:    opts.Selector,
	}

	md, data, msgHandle, err := ReceiveMessage(a.qMgr, args)
	if err != nil {
		if strings.Contains(err.Error(), "timeout") {
			return nil, backends.ErrNoMessageAvailable
		}
		return nil, err
	}
	// On success the handle still carries the message properties; delete it
	// only after they have been extracted.
	defer msgHandle.DltMH(ibmmq.NewMQDMHO()) //nolint:errcheck
	if data == nil {
		return nil, backends.ErrNoMessageAvailable
	}

	return convertMQMDToBackendMessage(md, data, msgHandle, opts.Verbosity >= backends.VerbosityVerbose), nil
}

// Close implements backends.QueueBackend
func (a *QueueAdapter) Close() error {
	return a.qMgr.Disc()
}

func convertMQMDToBackendMessage(md *ibmmq.MQMD, data []byte, msgHandle ibmmq.MQMessageHandle, withMetadata bool) *backends.Message {
	result := &backends.Message{
		Data:             data,
		Properties:       make(map[string]any),
		InternalMetadata: make(map[string]any),
	}

	// Extract message metadata (printable IDs round-trip as-is, binary
	// broker-generated IDs are hex-encoded — see mqIDToString).
	if msgId := mqIDToString(md.MsgId[:]); msgId != "" {
		result.MessageID = msgId
	}

	if correlId := mqIDToString(md.CorrelId[:]); correlId != "" {
		result.CorrelationID = correlId
	}

	if md.ReplyToQ != "" {
		result.ReplyTo = strings.TrimSpace(md.ReplyToQ)
	}

	result.Priority = int(md.Priority)
	result.Persistent = md.Persistence == int32(ibmmq.MQPER_PERSISTENT)

	// Extract properties from message handle
	impo := ibmmq.NewMQIMPO()
	impo.Options = ibmmq.MQIMPO_INQ_FIRST
	pd := ibmmq.NewMQPD()

	for {
		name, value, err := msgHandle.InqMP(impo, pd, "%")
		if err != nil {
			// No more properties
			break
		}
		if name != "" {
			// Strip IBM MQ folder prefix for JMS compatibility.
			// IBM MQ stores user properties as "usr.key" internally,
			// but JMS exposes them without the prefix.
			// Skip non-user folders (jms., mcd., root.) as they are
			// internal IBM MQ/JMS metadata.
			if strings.HasPrefix(name, "usr.") {
				result.Properties[name[4:]] = value
			}
		}
		impo.Options = ibmmq.MQIMPO_INQ_NEXT
	}

	// content-type rides as a message-handle property (see send.go — MQMD has
	// no native content-type field); pull it back out into ContentType so it
	// doesn't show up as an ordinary application property, mirroring how
	// Kafka/Pulsar/Redis extract their four reserved Prop* headers.
	if ct, ok := result.Properties[backends.PropContentType]; ok {
		if s, isStr := ct.(string); isStr {
			result.ContentType = s
		}
		delete(result.Properties, backends.PropContentType)
	}

	// Add internal metadata for verbose display
	if withMetadata {
		result.InternalMetadata["Format"] = strings.TrimSpace(md.Format)
		result.InternalMetadata["Priority"] = md.Priority
		result.InternalMetadata["Persistence"] = md.Persistence
	}

	return result
}
