//go:build integration

package integration

import (
	"fmt"
	"time"
)

// WaitForBroker retries check until it returns nil or timeout is reached.
// Useful when a broker's TCP port is open but the service isn't fully ready.
func WaitForBroker(check func() error, timeout time.Duration) error {
	deadline := time.Now().Add(timeout)
	var lastErr error
	for time.Now().Before(deadline) {
		if lastErr = check(); lastErr == nil {
			return nil
		}
		time.Sleep(500 * time.Millisecond)
	}
	return fmt.Errorf("broker not ready after %s: %w", timeout, lastErr)
}
