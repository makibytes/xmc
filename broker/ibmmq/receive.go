//go:build ibmmq

package ibmmq

import (
	"fmt"

	"github.com/ibm-messaging/mq-golang/v5/ibmmq"
	"github.com/makibytes/amc/log"
)

// ReceiveMessage receives a message from an IBM MQ queue
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

	qObject, err := qMgr.Open(mqod, openOptions)
	if err != nil {
		return nil, nil, ibmmq.MQMessageHandle{}, fmt.Errorf("failed to open queue: %w", err)
	}
	defer qObject.Close(0)

	// Create message descriptor and get options
	gmo := ibmmq.NewMQGMO()
	gmo.Options = ibmmq.MQGMO_NO_SYNCPOINT | ibmmq.MQGMO_FAIL_IF_QUIESCING

	if args.Acknowledge {
		gmo.Options |= ibmmq.MQGMO_WAIT
	} else {
		gmo.Options |= ibmmq.MQGMO_BROWSE_FIRST | ibmmq.MQGMO_WAIT
	}

	// Set timeout
	if args.Wait {
		gmo.WaitInterval = ibmmq.MQWI_UNLIMITED
	} else {
		if args.Timeout < 1 {
			gmo.WaitInterval = int32(args.Timeout * 1000) // Convert to milliseconds
		} else {
			gmo.WaitInterval = int32(args.Timeout * 1000)
		}
	}

	md := ibmmq.NewMQMD()
	buffer := make([]byte, 0)

	// Create message handle to retrieve properties
	cmho := ibmmq.NewMQCMHO()
	msgHandle, err := qMgr.CrtMH(cmho)
	if err != nil {
		return nil, nil, msgHandle, fmt.Errorf("failed to create message handle: %w", err)
	}
	defer msgHandle.DltMH(ibmmq.NewMQDMHO())

	gmo.MsgHandle = msgHandle

	// Apply message selector if provided
	if args.Selector != "" {
		gmo.MatchOptions = ibmmq.MQMO_MATCH_MSG_TOKEN
		// IBM MQ selection strings are set via SelectionString on the object descriptor
		log.Verbose("applying selector: %s", args.Selector)
	}

	// Get message
	log.Verbose("📩 receiving message from queue %s...", args.Queue)
	datalen, err := qObject.Get(md, gmo, buffer)
	if err != nil {
		mqret := err.(*ibmmq.MQReturn)
		if mqret.MQRC == ibmmq.MQRC_NO_MSG_AVAILABLE {
			// Timeout - no message available
			return nil, nil, msgHandle, fmt.Errorf("timeout: %w", err)
		}
		if mqret.MQRC == ibmmq.MQRC_TRUNCATED_MSG_FAILED {
			// Message too large for buffer, get actual size and retry
			buffer = make([]byte, datalen)
			md = ibmmq.NewMQMD()
			datalen, err = qObject.Get(md, gmo, buffer)
			if err != nil {
				return nil, nil, msgHandle, fmt.Errorf("failed to get message: %w", err)
			}
		} else {
			return nil, nil, msgHandle, fmt.Errorf("failed to get message: %w", err)
		}
	}

	// Handle the case where buffer was initially empty
	if len(buffer) == 0 && datalen > 0 {
		buffer = make([]byte, datalen)
		// Need to browse/get again with proper buffer size
		md = ibmmq.NewMQMD()
		if !args.Acknowledge {
			gmo.Options = ibmmq.MQGMO_NO_SYNCPOINT | ibmmq.MQGMO_FAIL_IF_QUIESCING | ibmmq.MQGMO_BROWSE_FIRST | ibmmq.MQGMO_WAIT
			gmo.WaitInterval = int32(args.Timeout * 1000)
		}
		datalen, err = qObject.Get(md, gmo, buffer)
		if err != nil {
			return nil, nil, msgHandle, fmt.Errorf("failed to get message with proper buffer: %w", err)
		}
	}

	return md, buffer[:datalen], msgHandle, nil
}
