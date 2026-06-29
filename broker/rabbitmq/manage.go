//go:build rabbitmq

package rabbitmq

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
)

var mgmtHTTPClient = &http.Client{Timeout: 10 * time.Second}

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

	resp, err := mgmtHTTPClient.Do(req)
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

	resp, err := mgmtHTTPClient.Do(req)
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

func managementPut(baseURL, path, user, password string, body []byte) error {
	fullURL := baseURL + path
	log.Verbose("requesting PUT %s", fullURL)

	req, err := http.NewRequest("PUT", fullURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := mgmtHTTPClient.Do(req)
	if err != nil {
		return fmt.Errorf("management API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, _ := io.ReadAll(resp.Body)
	if resp.StatusCode >= 300 {
		return fmt.Errorf("management API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return nil
}

func managementPost(baseURL, path, user, password string, body []byte) ([]byte, error) {
	fullURL := baseURL + path
	log.Verbose("requesting POST %s", fullURL)

	req, err := http.NewRequest("POST", fullURL, bytes.NewReader(body))
	if err != nil {
		return nil, err
	}
	if user != "" {
		req.SetBasicAuth(user, password)
	}
	req.Header.Set("Content-Type", "application/json")

	resp, err := mgmtHTTPClient.Do(req)
	if err != nil {
		return nil, fmt.Errorf("management API request failed: %w", err)
	}
	defer resp.Body.Close()

	respBody, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}

	if resp.StatusCode >= 300 {
		return nil, fmt.Errorf("management API returned status %d: %s", resp.StatusCode, string(respBody))
	}

	return respBody, nil
}

// CreateQueue creates a queue via the RabbitMQ Management API.
func CreateQueue(args ManagementArgs, queue string) error {
	base, err := managementURL(args.Server)
	if err != nil {
		return err
	}

	body, _ := json.Marshal(map[string]any{"durable": true})
	return managementPut(base, fmt.Sprintf("/queues/%%2F/%s", url.PathEscape(queue)), args.User, args.Password, body)
}

// DeleteQueue deletes a queue via the RabbitMQ Management API.
func DeleteQueue(args ManagementArgs, queue string) error {
	base, err := managementURL(args.Server)
	if err != nil {
		return err
	}

	_, err = managementDelete(base, fmt.Sprintf("/queues/%%2F/%s", url.PathEscape(queue)), args.User, args.Password)
	return err
}

// CreateExchange creates an exchange via the RabbitMQ Management API.
func CreateExchange(args ManagementArgs, exchange, exchangeType string) error {
	base, err := managementURL(args.Server)
	if err != nil {
		return err
	}

	body, _ := json.Marshal(map[string]any{
		"type":    exchangeType,
		"durable": true,
	})
	return managementPut(base, fmt.Sprintf("/exchanges/%%2F/%s", url.PathEscape(exchange)), args.User, args.Password, body)
}

// DeleteExchange deletes an exchange via the RabbitMQ Management API.
func DeleteExchange(args ManagementArgs, exchange string) error {
	base, err := managementURL(args.Server)
	if err != nil {
		return err
	}

	_, err = managementDelete(base, fmt.Sprintf("/exchanges/%%2F/%s", url.PathEscape(exchange)), args.User, args.Password)
	return err
}

// BindQueue binds a queue to an exchange via the RabbitMQ Management API.
func BindQueue(args ManagementArgs, queue, exchange, routingKey string) error {
	base, err := managementURL(args.Server)
	if err != nil {
		return err
	}

	body, _ := json.Marshal(map[string]any{
		"routing_key": routingKey,
	})
	_, err = managementPost(base, fmt.Sprintf("/bindings/%%2F/e/%s/q/%s", url.PathEscape(exchange), url.PathEscape(queue)), args.User, args.Password, body)
	return err
}

// UnbindQueue removes a binding between a queue and an exchange via the
// RabbitMQ Management API.
func UnbindQueue(args ManagementArgs, queue, exchange, routingKey string) error {
	base, err := managementURL(args.Server)
	if err != nil {
		return err
	}

	propKey := routingKey
	if propKey == "" {
		propKey = "~"
	}
	_, err = managementDelete(base, fmt.Sprintf("/bindings/%%2F/e/%s/q/%s/%s", url.PathEscape(exchange), url.PathEscape(queue), url.PathEscape(propKey)), args.User, args.Password)
	return err
}

// ListExchanges lists all exchanges and their bindings via the RabbitMQ
// Management API. Each exchange is returned as an ObjectNode; if the exchange
// has bindings, they appear as Children with the bound queue name and routing
// key.
func ListExchanges(args ManagementArgs) ([]backends.ObjectNode, error) {
	base, err := managementURL(args.Server)
	if err != nil {
		return nil, err
	}

	// Fetch exchanges.
	body, err := managementGet(base, "/exchanges/%2F", args.User, args.Password)
	if err != nil {
		return nil, err
	}

	var rawExchanges []struct {
		Name string `json:"name"`
		Type string `json:"type"`
	}
	if err := json.Unmarshal(body, &rawExchanges); err != nil {
		return nil, fmt.Errorf("failed to parse exchange list: %w", err)
	}

	// Fetch all bindings (source → destination).
	bindBody, err := managementGet(base, "/bindings/%2F", args.User, args.Password)
	if err != nil {
		return nil, err
	}

	var rawBindings []struct {
		Source          string `json:"source"`
		Destination     string `json:"destination"`
		DestinationType string `json:"destination_type"`
		RoutingKey      string `json:"routing_key"`
	}
	if err := json.Unmarshal(bindBody, &rawBindings); err != nil {
		return nil, fmt.Errorf("failed to parse bindings: %w", err)
	}

	// Group bindings by source exchange.
	bindMap := make(map[string][]backends.ObjectNode)
	for _, b := range rawBindings {
		if b.Source == "" {
			continue // default exchange binding
		}
		child := backends.ObjectNode{
			Name: b.Destination,
			Kind: b.DestinationType,
		}
		if b.RoutingKey != "" {
			child.Kind = fmt.Sprintf("%s key=%s", b.DestinationType, b.RoutingKey)
		}
		bindMap[b.Source] = append(bindMap[b.Source], child)
	}

	var exchanges []backends.ObjectNode
	for _, e := range rawExchanges {
		if e.Name == "" {
			continue // skip the default exchange
		}
		node := backends.ObjectNode{
			Name:     e.Name,
			Kind:     e.Type,
			Children: bindMap[e.Name],
		}
		exchanges = append(exchanges, node)
	}

	return exchanges, nil
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

// ---------- Non-destructive peek via Management API ----------

// mgmtMessage mirrors the JSON shape of one entry returned by
// POST /api/queues/%2F/{queue}/get.
type mgmtMessage struct {
	Payload         string `json:"payload"`
	PayloadEncoding string `json:"payload_encoding"`
	Properties      struct {
		MessageID     string                 `json:"message_id"`
		CorrelationID string                 `json:"correlation_id"`
		ReplyTo       string                 `json:"reply_to"`
		ContentType   string                 `json:"content_type"`
		Priority      uint8                  `json:"priority"`
		Headers       map[string]interface{} `json:"headers"`
	} `json:"properties"`
}

// peekQueueMessages fetches up to count messages from the named queue using the
// RabbitMQ Management API with ackmode=ack_requeue_true (non-destructive peek).
// The queue address may use the AMQP 1.0 v2 prefix "/queues/<name>".
func peekQueueMessages(args ManagementArgs, queueAddr string, count int) ([]backends.Message, error) {
	name := strings.TrimPrefix(queueAddr, "/queues/")
	base, err := managementURL(args.Server)
	if err != nil {
		return nil, err
	}
	reqBody := []byte(fmt.Sprintf(
		`{"count":%d,"ackmode":"ack_requeue_true","encoding":"auto","truncate":50000}`, count,
	))
	respBody, err := managementPost(base,
		fmt.Sprintf("/queues/%%2F/%s/get", url.PathEscape(name)),
		args.User, args.Password, reqBody,
	)
	if err != nil {
		return nil, err
	}
	var raw []mgmtMessage
	if err := json.Unmarshal(respBody, &raw); err != nil {
		return nil, fmt.Errorf("failed to parse messages: %w", err)
	}
	msgs := make([]backends.Message, 0, len(raw))
	for _, m := range raw {
		data := []byte(m.Payload)
		if m.PayloadEncoding == "base64" {
			if dec, err2 := base64.StdEncoding.DecodeString(m.Payload); err2 == nil {
				data = dec
			}
		}
		props := make(map[string]any, len(m.Properties.Headers))
		for k, v := range m.Properties.Headers {
			props[k] = v
		}
		msgs = append(msgs, backends.Message{
			Data:          data,
			MessageID:     m.Properties.MessageID,
			CorrelationID: m.Properties.CorrelationID,
			ReplyTo:       m.Properties.ReplyTo,
			ContentType:   m.Properties.ContentType,
			Priority:      int(m.Properties.Priority),
			Properties:    props,
		})
	}
	return msgs, nil
}

// mgmtBrowser iterates over a pre-fetched slice of messages in memory.
// It implements backends.Browser so it can be returned from QueueAdapter.Browse.
type mgmtBrowser struct {
	messages []backends.Message
	idx      int
}

func (b *mgmtBrowser) Next(ctx context.Context) (*backends.Message, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	if b.idx >= len(b.messages) {
		return nil, backends.ErrNoMessageAvailable
	}
	msg := b.messages[b.idx]
	b.idx++
	return &msg, nil
}

func (b *mgmtBrowser) Close() error { return nil }
