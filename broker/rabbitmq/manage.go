//go:build rabbitmq

package rabbitmq

import (
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"

	"github.com/makibytes/xmc/log"
)

// ManagementArgs holds parameters for RabbitMQ management operations
type ManagementArgs struct {
	Server   string
	User     string
	Password string
}

// managementURL converts the AMQP server URL to RabbitMQ Management API URL
func managementURL(amqpServer string) (string, error) {
	u, err := url.Parse(amqpServer)
	if err != nil {
		return "", err
	}
	host := u.Hostname()
	// RabbitMQ management API defaults to port 15672
	return fmt.Sprintf("http://%s:15672/api", host), nil
}

func managementGet(baseURL, path, user, password string) ([]byte, error) {
	fullURL := baseURL + path
	log.Verbose("requesting %s", fullURL)

	req, err := http.NewRequest("GET", fullURL, nil)
	if err != nil {
		return nil, err
	}
	if user != "" {
		req.SetBasicAuth(user, password)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("management API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode != 200 {
		return nil, fmt.Errorf("management API returned status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

func managementDelete(baseURL, path, user, password string) ([]byte, error) {
	fullURL := baseURL + path
	log.Verbose("requesting DELETE %s", fullURL)

	req, err := http.NewRequest("DELETE", fullURL, nil)
	if err != nil {
		return nil, err
	}
	if user != "" {
		req.SetBasicAuth(user, password)
	}

	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("management API request failed: %w", err)
	}
	defer resp.Body.Close()

	body, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("management API returned status %d: %s", resp.StatusCode, string(body))
	}

	return body, nil
}

// QueueInfo holds queue information from the RabbitMQ management API
type QueueInfo struct {
	Name          string
	MessageCount  int64
	ConsumerCount int
	Vhost         string
}

// QueueStats holds detailed queue statistics
type QueueStats struct {
	Name          string
	MessageCount  int64
	ConsumerCount int
	EnqueueCount  int64
	DequeueCount  int64
}

// ListQueues lists all queues via the RabbitMQ Management API
func ListQueues(args ManagementArgs) ([]QueueInfo, error) {
	base, err := managementURL(args.Server)
	if err != nil {
		return nil, err
	}

	body, err := managementGet(base, "/queues", args.User, args.Password)
	if err != nil {
		return nil, err
	}

	var rawQueues []struct {
		Name      string `json:"name"`
		Messages  int64  `json:"messages"`
		Consumers int    `json:"consumers"`
		Vhost     string `json:"vhost"`
	}
	if err := json.Unmarshal(body, &rawQueues); err != nil {
		return nil, fmt.Errorf("failed to parse queue list: %w", err)
	}

	var queues []QueueInfo
	for _, q := range rawQueues {
		queues = append(queues, QueueInfo{
			Name:          q.Name,
			MessageCount:  q.Messages,
			ConsumerCount: q.Consumers,
			Vhost:         q.Vhost,
		})
	}

	return queues, nil
}

// PurgeQueue removes all messages from a queue via the RabbitMQ Management API
func PurgeQueue(args ManagementArgs, queue string) error {
	base, err := managementURL(args.Server)
	if err != nil {
		return err
	}

	_, err = managementDelete(base, fmt.Sprintf("/queues/%%2F/%s/contents", url.PathEscape(queue)), args.User, args.Password)
	return err
}

// GetQueueStats returns detailed statistics for a queue
func GetQueueStats(args ManagementArgs, queue string) (*QueueStats, error) {
	base, err := managementURL(args.Server)
	if err != nil {
		return nil, err
	}

	body, err := managementGet(base, fmt.Sprintf("/queues/%%2F/%s", url.PathEscape(queue)), args.User, args.Password)
	if err != nil {
		return nil, err
	}

	var raw struct {
		Name                string `json:"name"`
		Messages            int64  `json:"messages"`
		Consumers           int    `json:"consumers"`
		MessagesPublished   int64  `json:"message_stats.publish"`
		MessagesDelivered   int64  `json:"message_stats.deliver_get"`
		MessageStatsPublish struct {
			Total int64 `json:"total"`
		} `json:"message_stats"`
	}
	if err := json.Unmarshal(body, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse queue stats: %w", err)
	}

	return &QueueStats{
		Name:          raw.Name,
		MessageCount:  raw.Messages,
		ConsumerCount: raw.Consumers,
	}, nil
}
