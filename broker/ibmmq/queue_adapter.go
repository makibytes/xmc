//go:build ibmmq

package ibmmq

import (
	"context"
	"fmt"
	"strings"

	"github.com/ibm-messaging/mq-golang/v5/ibmmq"
	"github.com/makibytes/amc/broker/backends"
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
	properties := make(map[string]string)
	for k, v := range opts.Properties {
		properties[k] = fmt.Sprintf("%v", v)
	}

	var persistence int
	if opts.Persistent {
		persistence = 1
	} else {
		persistence = 0
	}

	args := SendArguments{
		Queue:         opts.Queue,
		Message:       opts.Message,
		Properties:    properties,
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
		Queue:                     opts.Queue,
		Timeout:                   opts.Timeout,
		Wait:                      opts.Wait,
		Number:                    1,
		Acknowledge:               opts.Acknowledge,
		Selector:                  opts.Selector,
		WithHeaderAndProperties:   opts.WithHeaderAndProperties,
		WithApplicationProperties: opts.WithApplicationProperties,
	}

	md, data, msgHandle, err := ReceiveMessage(a.qMgr, args)
	if err != nil {
		if strings.Contains(err.Error(), "timeout") {
			return nil, context.DeadlineExceeded
		}
		return nil, err
	}
	if data == nil {
		return nil, fmt.Errorf("no message available")
	}

	return convertMQMDToBackendMessage(md, data, msgHandle, opts.WithHeaderAndProperties), nil
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

	// Extract message metadata
	msgId := strings.TrimRight(string(md.MsgId[:]), "\x00")
	if msgId != "" {
		result.MessageID = fmt.Sprintf("%x", md.MsgId)
	}

	correlId := strings.TrimRight(string(md.CorrelId[:]), "\x00")
	if correlId != "" {
		result.CorrelationID = fmt.Sprintf("%x", md.CorrelId)
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

	// Add internal metadata for verbose display
	if withMetadata {
		result.InternalMetadata["Format"] = strings.TrimSpace(md.Format)
		result.InternalMetadata["Priority"] = md.Priority
		result.InternalMetadata["Persistence"] = md.Persistence
	}

	return result
}
