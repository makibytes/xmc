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
// The returned CancelFunc must be called (typically via defer) to release the
// signal handler.
func interruptContext() (context.Context, context.CancelFunc) {
	return signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
}
