//go:build ibmmq

package ibmmq

import (
	"fmt"

	"github.com/ibm-messaging/mq-golang/v5/ibmmq"
	"github.com/makibytes/xmc/log"
)

// SendMessage sends a message to an IBM MQ queue
func SendMessage(qMgr ibmmq.MQQueueManager, args SendArguments) error {
	// Open queue for output
	mqod := ibmmq.NewMQOD()
	mqod.ObjectType = ibmmq.MQOT_Q
	mqod.ObjectName = args.Queue

	openOptions := ibmmq.MQOO_OUTPUT | ibmmq.MQOO_FAIL_IF_QUIESCING

	log.Verbose("📤 opening queue %s for sending...", args.Queue)
	qObject, err := qMgr.Open(mqod, openOptions)
	if err != nil {
		return fmt.Errorf("failed to open queue: %w", err)
	}
	defer qObject.Close(0)

	// Create message descriptor
	pmo := ibmmq.NewMQPMO()
	pmo.Options = ibmmq.MQPMO_NO_SYNCPOINT

	md := ibmmq.NewMQMD()
	md.Format = ibmmq.MQFMT_STRING

	// Set message properties
	if args.MessageID != "" {
		copy(md.MsgId[:], []byte(args.MessageID))
	}
	if args.CorrelationID != "" {
		copy(md.CorrelId[:], []byte(args.CorrelationID))
	}
	if args.ReplyTo != "" {
		md.ReplyToQ = args.ReplyTo
	}

	// Set priority (0-9, default 4)
	if args.Priority >= 0 && args.Priority <= 9 {
		md.Priority = int32(args.Priority)
	} else {
		md.Priority = 4
	}

	// Set TTL (MQMD.Expiry is in tenths of a second)
	if args.TTL > 0 {
		md.Expiry = int32(args.TTL / 100) // Convert ms to tenths of a second
		if md.Expiry < 1 {
			md.Expiry = 1 // minimum 0.1 seconds
		}
	}

	// Set persistence
	if args.Persistence > 0 {
		md.Persistence = int32(ibmmq.MQPER_PERSISTENT)
	} else {
		md.Persistence = int32(ibmmq.MQPER_NOT_PERSISTENT)
	}

	// Create a buffer for the message with properties
	buffer := args.Message

	// If we have properties, we need to use message handle (MQRFH2)
	if len(args.Properties) > 0 {
		log.Verbose("📦 adding %d properties to message...", len(args.Properties))

		// Create message handle for properties
		cmho := ibmmq.NewMQCMHO()
		msgHandle, err := qMgr.CrtMH(cmho)
		if err != nil {
			return fmt.Errorf("failed to create message handle: %w", err)
		}
		defer msgHandle.DltMH(ibmmq.NewMQDMHO())

		// Set properties using message handle
		smpo := ibmmq.NewMQSMPO()
		pd := ibmmq.NewMQPD()
		for key, value := range args.Properties {
			err = msgHandle.SetMP(smpo, key, pd, value)
			if err != nil {
				log.Verbose("⚠️  failed to set property %s: %v", key, err)
			}
		}

		// Use message handle in put options
		pmo.OriginalMsgHandle = msgHandle
	}

	// Put message
	log.Verbose("💌 sending message to queue %s...", args.Queue)
	err = qObject.Put(md, pmo, buffer)
	if err != nil {
		return fmt.Errorf("failed to put message: %w", err)
	}

	return nil
}
