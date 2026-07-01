//go:build ibmmq

package ibmmq

import (
	"context"
	"fmt"
	"strings"

	"github.com/ibm-messaging/mq-golang/v5/ibmmq"
	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
)

// Defaults for the temporary dynamic reply queue used by Request.
// SYSTEM.DEFAULT.MODEL.QUEUE exists on every queue manager, but locked-down
// installations (e.g. the MQ developer container's "app" user, which may only
// touch DEV.**) can point --model-queue / --dynamic-queue elsewhere.
const (
	defaultReplyModelQueue   = "SYSTEM.DEFAULT.MODEL.QUEUE"
	defaultReplyDynamicQueue = "XMC.REPLY.*"
)

// Request implements backends.RequestReplyBackend natively for IBM MQ using
// the classic MQ pattern: the reply lands on a temporary dynamic queue
// (created from a model queue and deleted again on close) and the queue
// manager matches it by CorrelId server-side, so concurrent requestors never
// read each other's replies — even on a caller-named shared reply queue.
func (a *QueueAdapter) Request(ctx context.Context, opts backends.RequestOptions) (*backends.Message, error) {
	correlationID := requestCorrelationID(&opts)
	if len(correlationID) > 24 {
		return nil, &backends.RequestSendError{Err: fmt.Errorf("correlation ID must be at most 24 bytes, got %d", len(correlationID))}
	}
	timeout := opts.Timeout
	if timeout <= 0 {
		timeout = backends.DefaultRequestTimeout
	}

	// Open (or create) the reply queue before sending, so a responder cannot
	// answer faster than the reply destination exists.
	mqod := ibmmq.NewMQOD()
	mqod.ObjectType = ibmmq.MQOT_Q
	openOptions := ibmmq.MQOO_FAIL_IF_QUIESCING
	closeOptions := int32(0)
	if opts.ReplyTo == "" {
		mqod.ObjectName = defaultReplyModelQueue
		if a.connArgs.ModelQueue != "" {
			mqod.ObjectName = a.connArgs.ModelQueue
		}
		mqod.DynamicQName = defaultReplyDynamicQueue
		if a.connArgs.DynamicQueue != "" {
			mqod.DynamicQName = a.connArgs.DynamicQueue
		}
		openOptions |= ibmmq.MQOO_INPUT_EXCLUSIVE
		closeOptions = ibmmq.MQCO_DELETE_PURGE
	} else {
		mqod.ObjectName = opts.ReplyTo
		openOptions |= ibmmq.MQOO_INPUT_SHARED
	}
	replyObj, err := a.qMgr.Open(mqod, openOptions)
	if err != nil {
		return nil, &backends.RequestSendError{Err: fmt.Errorf("opening reply queue: %w", err)}
	}
	defer replyObj.Close(closeOptions)

	// For a dynamic queue the queue manager fills in the generated name.
	replyTo := strings.TrimSpace(replyObj.Name)
	log.Verbose("reply queue: %s", replyTo)

	var persistence int
	if opts.Persistent {
		persistence = 1
	}
	if err := SendMessage(a.qMgr, SendArguments{
		Queue:         opts.Address,
		Message:       opts.Message,
		Properties:    backends.StringifyProps(opts.Properties),
		MessageID:     opts.MessageID,
		CorrelationID: correlationID,
		ReplyTo:       replyTo,
		ContentType:   opts.ContentType,
		Priority:      opts.Priority,
		Persistence:   persistence,
	}); err != nil {
		return nil, &backends.RequestSendError{Err: err}
	}

	return receiveByCorrelID(a.qMgr, replyObj, correlationID, timeout)
}

// receiveByCorrelID gets one message from obj whose CorrelId matches
// correlationID (zero-padded to MQMD's 24 bytes), waiting up to timeout
// seconds. The match happens in the queue manager via MQMO_MATCH_CORREL_ID.
func receiveByCorrelID(qMgr ibmmq.MQQueueManager, obj ibmmq.MQObject, correlationID string, timeout float32) (*backends.Message, error) {
	gmo := ibmmq.NewMQGMO()
	gmo.Options = ibmmq.MQGMO_NO_SYNCPOINT | ibmmq.MQGMO_FAIL_IF_QUIESCING | ibmmq.MQGMO_WAIT
	gmo.WaitInterval = int32(timeout * 1000)
	gmo.MatchOptions = ibmmq.MQMO_MATCH_CORREL_ID

	cmho := ibmmq.NewMQCMHO()
	msgHandle, err := qMgr.CrtMH(cmho)
	if err != nil {
		return nil, fmt.Errorf("failed to create message handle: %w", err)
	}
	defer msgHandle.DltMH(ibmmq.NewMQDMHO())
	gmo.MsgHandle = msgHandle

	md := ibmmq.NewMQMD()
	copy(md.CorrelId[:], correlationID)
	buffer := make([]byte, 0)

	datalen, err := obj.Get(md, gmo, buffer)
	if err != nil {
		mqret, ok := err.(*ibmmq.MQReturn)
		switch {
		case ok && mqret.MQRC == ibmmq.MQRC_NO_MSG_AVAILABLE:
			return nil, backends.ErrNoMessageAvailable
		case ok && mqret.MQRC == ibmmq.MQRC_TRUNCATED_MSG_FAILED:
			buffer = make([]byte, datalen)
			md = ibmmq.NewMQMD()
			copy(md.CorrelId[:], correlationID)
			datalen, err = obj.Get(md, gmo, buffer)
			if err != nil {
				return nil, fmt.Errorf("failed to get reply: %w", err)
			}
		default:
			return nil, fmt.Errorf("failed to get reply: %w", err)
		}
	}

	return convertMQMDToBackendMessage(md, buffer[:datalen], msgHandle, false), nil
}

// requestCorrelationID mirrors backends.EnsureCorrelationID but generates an
// MQ-sized id when one has to be invented: the broker-neutral generator
// produces 28 characters, which does not fit MQMD.CorrelId's 24 bytes.
func requestCorrelationID(opts *backends.RequestOptions) string {
	if opts.CorrelationID == "" {
		if opts.MessageID != "" {
			opts.CorrelationID = opts.MessageID
		} else {
			opts.CorrelationID = "xmc-" + backends.RandomSuffix()
		}
	}
	return opts.CorrelationID
}
