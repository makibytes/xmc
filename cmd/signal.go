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
// An optional parent context may be supplied; the returned context is cancelled
// when either the parent is cancelled OR the signal arrives. Pass nil (or omit)
// to use context.Background() as the parent.
//
// The returned CancelFunc must be called (typically via defer) to release the
// signal handler.
func interruptContext(parents ...context.Context) (context.Context, context.CancelFunc) {
	p := context.Background()
	if len(parents) > 0 && parents[0] != nil {
		p = parents[0]
	}
	return signal.NotifyContext(p, os.Interrupt, syscall.SIGTERM)
}
