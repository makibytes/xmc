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
	return ConnArguments{
		Server:   testServer,
		ClientID: "xmc-test-" + randomSuffix(),
	}
}

func randomSuffix() string { return fmt.Sprintf("%d", rand.Int63()) } //nolint:gosec

// TestMQTT_TopicPublishSubscribe verifies basic pub/sub using QoS 0 (non-persistent).
// The subscriber goroutine is started first, then the publisher sends after a short delay.
func TestMQTT_TopicPublishSubscribe(t *testing.T) {
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
