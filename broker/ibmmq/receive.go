//go:build ibmmq

package ibmmq

import (
	"fmt"

	"github.com/ibm-messaging/mq-golang/v5/ibmmq"
	"github.com/makibytes/xmc/log"
)

// ReceiveMessage receives a message from an IBM MQ queue.
//
// On success the returned message handle still holds the message's properties:
// the caller owns it and must DltMH it after extracting them (deleting it here
// would invalidate the handle before conversion). On error the handle is
// already cleaned up.
func ReceiveMessage(qMgr ibmmq.MQQueueManager, args ReceiveArguments) (*ibmmq.MQMD, []byte, ibmmq.MQMessageHandle, error) {
	// Open queue for input
	mqod := ibmmq.NewMQOD()
	mqod.ObjectType = ibmmq.MQOT_Q
	mqod.ObjectName = args.Queue

	var openOptions int32
	if args.Acknowledge {
		// GET: destructive read
		openOptions = ibmmq.MQOO_INPUT_EXCLUSIVE | ibmmq.MQOO_FAIL_IF_QUIESCING
		log.Verbose("📥 opening queue %s for getting (destructive read)...", args.Queue)
	} else {
		// PEEK: browse without removing
		openOptions = ibmmq.MQOO_BROWSE | ibmmq.MQOO_FAIL_IF_QUIESCING
		log.Verbose("📥 opening queue %s for peeking (browse)...", args.Queue)
	}

	// A message selector is evaluated by the queue manager; it is part of the
	// object descriptor at open time (MQOD version 4 SelectionString).
	if args.Selector != "" {
		log.Verbose("applying selector: %s", args.Selector)
		mqod.SelectionString = args.Selector
	}

	qObject, err := qMgr.Open(mqod, openOptions)
	if err != nil {
		return nil, nil, ibmmq.MQMessageHandle{}, fmt.Errorf("failed to open queue: %w", err)
	}
	defer qObject.Close(0)

	// Create message descriptor and get options
	gmo := ibmmq.NewMQGMO()
	gmo.Options = ibmmq.MQGMO_NO_SYNCPOINT | ibmmq.MQGMO_FAIL_IF_QUIESCING | ibmmq.MQGMO_WAIT
	if !args.Acknowledge {
		gmo.Options |= ibmmq.MQGMO_BROWSE_FIRST
	}

	// Set timeout
	if args.Wait {
		gmo.WaitInterval = ibmmq.MQWI_UNLIMITED
	} else {
		gmo.WaitInterval = int32(args.Timeout * 1000) // seconds → milliseconds
	}

	md := ibmmq.NewMQMD()
	buffer := make([]byte, 0)

	// Create message handle to retrieve properties
	cmho := ibmmq.NewMQCMHO()
	msgHandle, err := qMgr.CrtMH(cmho)
	if err != nil {
		return nil, nil, msgHandle, fmt.Errorf("failed to create message handle: %w", err)
	}
	discardHandle := func() { msgHandle.DltMH(ibmmq.NewMQDMHO()) } //nolint:errcheck

	gmo.MsgHandle = msgHandle

	// Get message
	log.Verbose("📩 receiving message from queue %s...", args.Queue)
	datalen, err := qObject.Get(md, gmo, buffer)
	if err != nil {
		mqret := err.(*ibmmq.MQReturn)
		switch mqret.MQRC {
		case ibmmq.MQRC_NO_MSG_AVAILABLE:
			// Timeout - no message available
			discardHandle()
			return nil, nil, msgHandle, fmt.Errorf("timeout: %w", err)
		case ibmmq.MQRC_TRUNCATED_MSG_FAILED:
			// Message too large for buffer; a failed truncated Get leaves the
			// message in place (and does not advance the browse cursor), so
			// retry with the same options and a buffer of the reported size.
			buffer = make([]byte, datalen)
			md = ibmmq.NewMQMD()
			datalen, err = qObject.Get(md, gmo, buffer)
			if err != nil {
				discardHandle()
				return nil, nil, msgHandle, fmt.Errorf("failed to get message: %w", err)
			}
		default:
			discardHandle()
			return nil, nil, msgHandle, fmt.Errorf("failed to get message: %w", err)
		}
	}

	return md, buffer[:datalen], msgHandle, nil
}
