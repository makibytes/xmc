//go:build integration

package integration

import (
	"fmt"
	"net/http"
	"strings"
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

// DeclareRabbitMQQueue creates a classic queue via the RabbitMQ Management API.
func DeclareRabbitMQQueue(mgmtURL, user, password, queue string) error {
	url := fmt.Sprintf("%s/api/queues/%%2F/%s", mgmtURL, queue)
	req, err := http.NewRequest(http.MethodPut, url, strings.NewReader(`{"durable":true}`))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(user, password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("declare queue %s: HTTP %d", queue, resp.StatusCode)
	}
	return nil
}

// BindRabbitMQQueue binds a queue to an exchange with a routing key via the
// RabbitMQ Management API.
func BindRabbitMQQueue(mgmtURL, user, password, queue, exchange, routingKey string) error {
	url := fmt.Sprintf("%s/api/bindings/%%2F/e/%s/q/%s", mgmtURL, exchange, queue)
	body := fmt.Sprintf(`{"routing_key":%q}`, routingKey)
	req, err := http.NewRequest(http.MethodPost, url, strings.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.SetBasicAuth(user, password)
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return err
	}
	resp.Body.Close()
	if resp.StatusCode >= 300 {
		return fmt.Errorf("bind queue %s to %s/%s: HTTP %d", queue, exchange, routingKey, resp.StatusCode)
	}
	return nil
}
