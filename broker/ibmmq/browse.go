//go:build ibmmq

package ibmmq

import (
	"context"
	"fmt"

	"github.com/ibm-messaging/mq-golang/v5/ibmmq"
	"github.com/makibytes/xmc/broker/backends"
)

// Browse implements backends.BrowseBackend with MQ's native browse cursor:
// the queue is opened once with MQOO_BROWSE and successive Gets advance via
// MQGMO_BROWSE_FIRST / MQGMO_BROWSE_NEXT. The stateless peek path re-issues
// BROWSE_FIRST on a fresh object every call and therefore always re-reads the
// head, which made "peek -n 0" repeat the first message forever.
func (a *QueueAdapter) Browse(ctx context.Context, opts backends.ReceiveOptions) (backends.Browser, error) {
	mqod := ibmmq.NewMQOD()
	mqod.ObjectType = ibmmq.MQOT_Q
	mqod.ObjectName = opts.Queue

	obj, err := a.qMgr.Open(mqod, ibmmq.MQOO_BROWSE|ibmmq.MQOO_FAIL_IF_QUIESCING)
	if err != nil {
		return nil, fmt.Errorf("failed to open queue for browsing: %w", err)
	}

	return &queueBrowser{
		qMgr:    a.qMgr,
		obj:     obj,
		waitMs:  int32(backends.TimeoutDuration(opts.Timeout, opts.Wait).Milliseconds()),
		verbose: opts.Verbosity >= backends.VerbosityVerbose,
		first:   true,
	}, nil
}

type queueBrowser struct {
	qMgr    ibmmq.MQQueueManager
	obj     ibmmq.MQObject
	waitMs  int32
	verbose bool
	first   bool // next Get uses BROWSE_FIRST; BROWSE_NEXT afterwards
}

func (b *queueBrowser) Next(_ context.Context) (*backends.Message, error) {
	gmo := ibmmq.NewMQGMO()
	gmo.Options = ibmmq.MQGMO_NO_SYNCPOINT | ibmmq.MQGMO_FAIL_IF_QUIESCING | ibmmq.MQGMO_WAIT
	if b.first {
		gmo.Options |= ibmmq.MQGMO_BROWSE_FIRST
	} else {
		gmo.Options |= ibmmq.MQGMO_BROWSE_NEXT
	}
	gmo.WaitInterval = b.waitMs

	cmho := ibmmq.NewMQCMHO()
	msgHandle, err := b.qMgr.CrtMH(cmho)
	if err != nil {
		return nil, fmt.Errorf("failed to create message handle: %w", err)
	}
	defer msgHandle.DltMH(ibmmq.NewMQDMHO())
	gmo.MsgHandle = msgHandle

	md := ibmmq.NewMQMD()
	buffer := make([]byte, 0)

	datalen, err := b.obj.Get(md, gmo, buffer)
	if err != nil {
		mqret, ok := err.(*ibmmq.MQReturn)
		switch {
		case ok && mqret.MQRC == ibmmq.MQRC_NO_MSG_AVAILABLE:
			return nil, backends.ErrNoMessageAvailable
		case ok && mqret.MQRC == ibmmq.MQRC_TRUNCATED_MSG_FAILED:
			// A truncated browse does NOT advance the browse cursor, so
			// retry with the same BROWSE_FIRST/NEXT options and a buffer of
			// the reported size.
			buffer = make([]byte, datalen)
			md = ibmmq.NewMQMD()
			datalen, err = b.obj.Get(md, gmo, buffer)
			if err != nil {
				return nil, fmt.Errorf("failed to browse message: %w", err)
			}
		default:
			return nil, fmt.Errorf("failed to browse message: %w", err)
		}
	}
	b.first = false

	return convertMQMDToBackendMessage(md, buffer[:datalen], msgHandle, b.verbose), nil
}

func (b *queueBrowser) Close() error {
	return b.obj.Close(0)
}
