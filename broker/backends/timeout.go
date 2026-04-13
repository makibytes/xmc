package backends

import "time"

// TimeoutDuration converts a timeout (seconds) and wait flag into a time.Duration.
// If wait is true, returns 24 hours. If timeout <= 0, defaults to 5 seconds.
func TimeoutDuration(timeout float32, wait bool) time.Duration {
	if wait {
		return 24 * time.Hour
	}
	if timeout <= 0 {
		return 5 * time.Second
	}
	return time.Duration(float64(timeout) * float64(time.Second))
}
