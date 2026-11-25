package cmd

import (
	"context"
	"os"
	"os/signal"
	"syscall"
)

// interruptContext returns a context that is cancelled when the process receives
// an interrupt (Ctrl-C) or termination signal. Long-running commands such as
// reply and move use it so they shut down cleanly — letting the deferred
// adapter Close run — instead of being hard-killed mid-operation.
//
// The returned context is cancelled when either parent is cancelled OR the
// signal arrives. A nil parent means context.Background().
//
// The returned CancelFunc must be called (typically via defer) to release the
// signal handler.
func interruptContext(parent context.Context) (context.Context, context.CancelFunc) {
	if parent == nil {
		parent = context.Background()
	}
	return signal.NotifyContext(parent, os.Interrupt, syscall.SIGTERM)
}
