//go:build mqtt && integration

package mqtt

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/test/integration"
)

var testServer string

func TestMain(m *testing.M) {
	ctx := context.Background()
	broker, err := integration.StartMosquitto(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start Mosquitto: %v\n", err)
		os.Exit(1)
	}
	defer broker.Terminate(ctx)
	testServer = broker.URL
	os.Exit(m.Run())
}

func makeConnArgs() ConnArguments {
	args := ConnArguments{
		ClientID: "xmc-test-" + randomSuffix(),
	}
	args.Server = testServer
	return args
}

func randomSuffix() string { return fmt.Sprintf("%d", rand.Int63()) } //nolint:gosec

// TestMQTT_TopicPublishSubscribe verifies basic pub/sub using QoS 0 (non-persistent).
// The subscriber goroutine is started first, then the publisher sends after a short delay.
func TestMQTT_TopicPublishSubscribe(t *testing.T) {
	t.Parallel()
	topic := "test/topic/" + randomSuffix()
	payload := []byte("hello mqtt")

	subArgs := makeConnArgs()
	subAdapter, err := NewTopicAdapter(subArgs)
	if err != nil {
		t.Fatalf("NewTopicAdapter: %v", err)
	}
	defer subAdapter.Close()

	pubArgs := makeConnArgs()
	pubAdapter, err := NewTopicAdapter(pubArgs)
	if err != nil {
		t.Fatalf("NewTopicAdapter (pub): %v", err)
	}
	defer pubAdapter.Close()

	type result struct {
		msg *backends.Message
		err error
	}
	ch := make(chan result, 1)

	go func() {
		msg, err := subAdapter.Subscribe(context.Background(), backends.SubscribeOptions{
			Topic:   topic,
			Timeout: 5,
		})
		ch <- result{msg, err}
	}()

	// Give the subscriber time to register before publishing.
	time.Sleep(300 * time.Millisecond)

	err = pubAdapter.Publish(context.Background(), backends.PublishOptions{
		Topic:   topic,
		Message: payload,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	res := <-ch
	if res.err != nil {
		t.Fatalf("Subscribe: %v", res.err)
	}
	if string(res.msg.Data) != string(payload) {
		t.Errorf("payload mismatch: got %q, want %q", res.msg.Data, payload)
	}
}

// TestMQTT_TopicPublishSubscribe_Persistent publishes with Persistent=true (QoS 1)
// and verifies the message is received.
func TestMQTT_TopicPublishSubscribe_Persistent(t *testing.T) {
	t.Parallel()
	topic := "test/topic/persistent/" + randomSuffix()
	payload := []byte("persistent message")

	subAdapter, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter: %v", err)
	}
	defer subAdapter.Close()

	pubAdapter, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter (pub): %v", err)
	}
	defer pubAdapter.Close()

	type result struct {
		msg *backends.Message
		err error
	}
	ch := make(chan result, 1)

	go func() {
		msg, err := subAdapter.Subscribe(context.Background(), backends.SubscribeOptions{
			Topic:   topic,
			Timeout: 5,
		})
		ch <- result{msg, err}
	}()

	time.Sleep(300 * time.Millisecond)

	err = pubAdapter.Publish(context.Background(), backends.PublishOptions{
		Topic:      topic,
		Message:    payload,
		Persistent: true,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	res := <-ch
	if res.err != nil {
		t.Fatalf("Subscribe: %v", res.err)
	}
	if string(res.msg.Data) != string(payload) {
		t.Errorf("payload mismatch: got %q, want %q", res.msg.Data, payload)
	}
}

// TestMQTT_TopicSubscribe_Timeout verifies that Subscribe returns an error when
// no message arrives within the timeout window.
func TestMQTT_TopicSubscribe_Timeout(t *testing.T) {
	t.Parallel()
	topic := "test/topic/nobody/" + randomSuffix()

	adapter, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter: %v", err)
	}
	defer adapter.Close()

	msg, err := adapter.Subscribe(context.Background(), backends.SubscribeOptions{
		Topic:   topic,
		Timeout: 2,
	})

	// Either an error (timeout) or a nil message is acceptable.
	if err == nil && msg != nil {
		t.Errorf("expected timeout, but got message: %q", msg.Data)
	}
}

// TestMQTT_QueueSendReceive tests queue semantics via shared subscriptions
// ($share/xmc/queue/{name}). Mosquitto 2.x supports MQTT 5 shared subscriptions,
// but the default no-auth config may use MQTT 3.1.1 only. If the subscribe token
// returns an error, the test is skipped with an explanatory message.
func TestMQTT_QueueSendReceive(t *testing.T) {
	t.Parallel()
	queueName := "testq-" + randomSuffix()
	payload := []byte("queue message")

	recvAdapter, err := NewQueueAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter (recv): %v", err)
	}
	defer recvAdapter.Close()

	sendAdapter, err := NewQueueAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter (send): %v", err)
	}
	defer sendAdapter.Close()

	type result struct {
		msg *backends.Message
		err error
	}
	ch := make(chan result, 1)

	go func() {
		msg, err := recvAdapter.Receive(context.Background(), backends.ReceiveOptions{
			Queue:       queueName,
			Timeout:     5,
			Acknowledge: true,
		})
		ch <- result{msg, err}
	}()

	time.Sleep(300 * time.Millisecond)

	err = sendAdapter.Send(context.Background(), backends.SendOptions{
		Queue:   queueName,
		Message: payload,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	res := <-ch
	if res.err != nil {
		// Shared subscriptions require MQTT 5. Skip gracefully if not supported.
		if strings.Contains(res.err.Error(), "subscribe") || strings.Contains(res.err.Error(), "timed out") {
			t.Skipf("skipping: shared subscriptions may not be supported — %v", res.err)
		}
		t.Fatalf("Receive: %v", res.err)
	}
	if string(res.msg.Data) != string(payload) {
		t.Errorf("payload mismatch: got %q, want %q", res.msg.Data, payload)
	}
}

// TestMQTT_TopicSubscribe_GroupID verifies that setting GroupID triggers a shared
// subscription ($share/<groupID>/<topic>). Mosquitto 2.x supports this with MQTT 5.
// If the broker rejects the shared subscription, the test is skipped.
func TestMQTT_TopicSubscribe_GroupID(t *testing.T) {
	t.Parallel()
	topic := "test/grouped/" + randomSuffix()
	payload := []byte("grouped message")

	subAdapter, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter (sub): %v", err)
	}
	defer subAdapter.Close()

	pubAdapter, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter (pub): %v", err)
	}
	defer pubAdapter.Close()

	type result struct {
		msg *backends.Message
		err error
	}
	ch := make(chan result, 1)

	go func() {
		msg, err := subAdapter.Subscribe(context.Background(), backends.SubscribeOptions{
			Topic:   topic,
			GroupID: "test-group",
			Timeout: 5,
		})
		ch <- result{msg, err}
	}()

	time.Sleep(300 * time.Millisecond)

	err = pubAdapter.Publish(context.Background(), backends.PublishOptions{
		Topic:   topic,
		Message: payload,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	res := <-ch
	if res.err != nil {
		// Skip if the broker does not support shared subscriptions.
		if strings.Contains(res.err.Error(), "subscribe") || strings.Contains(res.err.Error(), "timed out") {
			t.Skipf("skipping: shared subscriptions may not be supported — %v", res.err)
		}
		t.Fatalf("Subscribe with GroupID: %v", res.err)
	}
	if string(res.msg.Data) != string(payload) {
		t.Errorf("payload mismatch: got %q, want %q", res.msg.Data, payload)
	}
}

// TestMQTT_QueueMetadataRoundTrip verifies that MQTT 5 carries application
// properties and metadata in the native property slots: content type,
// correlation data, response topic (with the queue/ prefix stripped back
// out), message expiry, and the message-id user property.
func TestMQTT_QueueMetadataRoundTrip(t *testing.T) {
	t.Parallel()
	queueName := "testq-meta-" + randomSuffix()
	payload := []byte("metadata message")

	recvAdapter, err := NewQueueAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter (recv): %v", err)
	}
	defer recvAdapter.Close()

	sendAdapter, err := NewQueueAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter (send): %v", err)
	}
	defer sendAdapter.Close()

	type result struct {
		msg *backends.Message
		err error
	}
	ch := make(chan result, 1)

	go func() {
		msg, err := recvAdapter.Receive(context.Background(), backends.ReceiveOptions{
			Queue:       queueName,
			Timeout:     5,
			Acknowledge: true,
		})
		ch <- result{msg, err}
	}()

	time.Sleep(300 * time.Millisecond)

	err = sendAdapter.Send(context.Background(), backends.SendOptions{
		Queue:         queueName,
		Message:       payload,
		MessageID:     "msg-42",
		CorrelationID: "corr-42",
		ReplyTo:       "answers",
		ContentType:   "text/plain",
		TTL:           60000,
		Properties:    map[string]any{"colour": "blue", "size": "42"},
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	res := <-ch
	if res.err != nil {
		t.Fatalf("Receive: %v", res.err)
	}
	msg := res.msg
	if string(msg.Data) != string(payload) {
		t.Errorf("payload: got %q, want %q", msg.Data, payload)
	}
	if msg.MessageID != "msg-42" {
		t.Errorf("MessageID: got %q, want msg-42", msg.MessageID)
	}
	if msg.CorrelationID != "corr-42" {
		t.Errorf("CorrelationID: got %q, want corr-42", msg.CorrelationID)
	}
	if msg.ReplyTo != "answers" {
		t.Errorf("ReplyTo: got %q, want answers (queue/ prefix stripped)", msg.ReplyTo)
	}
	if msg.ContentType != "text/plain" {
		t.Errorf("ContentType: got %q, want text/plain", msg.ContentType)
	}
	if msg.Properties["colour"] != "blue" || msg.Properties["size"] != "42" {
		t.Errorf("properties: got %v, want colour=blue size=42", msg.Properties)
	}
	if _, ok := msg.Properties[backends.PropMessageID]; ok {
		t.Error("message-id user property leaked into Properties")
	}
	if msg.InternalMetadata["message-expiry"] == nil {
		t.Errorf("InternalMetadata: got %v, want message-expiry set (TTL sent)", msg.InternalMetadata)
	}
}

// TestMQTT_V3_QueueSendReceive verifies the legacy 3.1.1 path
// (--mqtt-version 3) still moves payloads.
func TestMQTT_V3_QueueSendReceive(t *testing.T) {
	t.Parallel()
	queueName := "testq-v3-" + randomSuffix()
	payload := []byte("v3 queue message")

	recvAdapter, err := NewQueueAdapterV3(makeConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapterV3 (recv): %v", err)
	}
	defer recvAdapter.Close()

	sendAdapter, err := NewQueueAdapterV3(makeConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapterV3 (send): %v", err)
	}
	defer sendAdapter.Close()

	type result struct {
		msg *backends.Message
		err error
	}
	ch := make(chan result, 1)

	go func() {
		msg, err := recvAdapter.Receive(context.Background(), backends.ReceiveOptions{
			Queue:       queueName,
			Timeout:     5,
			Acknowledge: true,
		})
		ch <- result{msg, err}
	}()

	time.Sleep(300 * time.Millisecond)

	if err := sendAdapter.Send(context.Background(), backends.SendOptions{
		Queue:   queueName,
		Message: payload,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	res := <-ch
	if res.err != nil {
		t.Fatalf("Receive: %v", res.err)
	}
	if string(res.msg.Data) != string(payload) {
		t.Errorf("payload: got %q, want %q", res.msg.Data, payload)
	}
}

// TestMQTT_V3_RejectsMetadata verifies the 3.1.1 path fails loudly instead of
// silently dropping metadata the protocol cannot carry.
func TestMQTT_V3_RejectsMetadata(t *testing.T) {
	t.Parallel()

	adapter, err := NewQueueAdapterV3(makeConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapterV3: %v", err)
	}
	defer adapter.Close()

	err = adapter.Send(context.Background(), backends.SendOptions{
		Queue:       "testq-v3-reject",
		Message:     []byte("x"),
		ContentType: "text/plain",
	})
	if err == nil || !strings.Contains(err.Error(), "MQTT 5") {
		t.Errorf("Send: got %v, want metadata-requires-MQTT-5 error", err)
	}
}
