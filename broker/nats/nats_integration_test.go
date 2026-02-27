//go:build nats && integration

package nats

import (
	"context"
	"fmt"
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
	broker, err := integration.StartNATS(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start NATS: %v\n", err)
		os.Exit(1)
	}
	defer broker.Terminate(ctx)
	testServer = broker.URL
	os.Exit(m.Run())
}

func makeConnArgs() ConnArguments {
	return ConnArguments{Server: testServer}
}

// randomSuffix returns a short unique suffix based on current time to avoid stream name conflicts.
func randomSuffix() string {
	return fmt.Sprintf("%d", time.Now().UnixNano())
}

// --- Queue tests (JetStream) ---

func TestNATS_QueueSendReceive(t *testing.T) {
	t.Parallel()
	queue := "test-send-receive-" + randomSuffix()
	ctx := context.Background()

	adapter, err := NewQueueAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer adapter.Close()

	payload := []byte("hello nats")
	if err := adapter.Send(ctx, backends.SendOptions{Queue: queue, Message: payload}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msg, err := adapter.Receive(ctx, backends.ReceiveOptions{Queue: queue, Acknowledge: true, Timeout: 5})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(msg.Data) != string(payload) {
		t.Fatalf("expected %q, got %q", payload, msg.Data)
	}
}

func TestNATS_QueuePeek(t *testing.T) {
	t.Parallel()
	queue := "test-peek-" + randomSuffix()
	ctx := context.Background()

	adapter, err := NewQueueAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer adapter.Close()

	payload := []byte("peek me")
	if err := adapter.Send(ctx, backends.SendOptions{Queue: queue, Message: payload}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Peek (Acknowledge=false → Nak, message stays)
	peeked, err := adapter.Receive(ctx, backends.ReceiveOptions{Queue: queue, Acknowledge: false, Timeout: 5})
	if err != nil {
		t.Fatalf("Peek Receive: %v", err)
	}
	if string(peeked.Data) != string(payload) {
		t.Fatalf("peek: expected %q, got %q", payload, peeked.Data)
	}

	// Give NATS a moment to redeliver the nack'd message
	time.Sleep(500 * time.Millisecond)

	// Message should still be available for a destructive receive
	msg, err := adapter.Receive(ctx, backends.ReceiveOptions{Queue: queue, Acknowledge: true, Timeout: 5})
	if err != nil {
		t.Fatalf("Receive after peek: %v", err)
	}
	if string(msg.Data) != string(payload) {
		t.Fatalf("after peek: expected %q, got %q", payload, msg.Data)
	}
}

func TestNATS_QueueSendReceive_Properties(t *testing.T) {
	t.Parallel()
	queue := "test-props-" + randomSuffix()
	ctx := context.Background()

	adapter, err := NewQueueAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer adapter.Close()

	props := map[string]any{
		"x-custom-key": "my-value",
		"priority":     "high",
	}
	if err := adapter.Send(ctx, backends.SendOptions{
		Queue:      queue,
		Message:    []byte("props test"),
		Properties: props,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msg, err := adapter.Receive(ctx, backends.ReceiveOptions{Queue: queue, Acknowledge: true, Timeout: 5})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}

	for k, want := range props {
		got, ok := msg.Properties[k]
		if !ok {
			t.Errorf("property %q missing from received message", k)
			continue
		}
		if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
			t.Errorf("property %q: expected %v, got %v", k, want, got)
		}
	}
}

func TestNATS_QueueReceive_Timeout(t *testing.T) {
	t.Parallel()
	queue := "test-timeout-" + randomSuffix()
	ctx := context.Background()

	adapter, err := NewQueueAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer adapter.Close()

	_, err = adapter.Receive(ctx, backends.ReceiveOptions{Queue: queue, Acknowledge: true, Timeout: 2})
	if err == nil {
		t.Fatal("expected error on empty queue, got nil")
	}
	if !strings.Contains(err.Error(), "no message available") {
		t.Fatalf("expected 'no message available', got: %v", err)
	}
}

func TestNATS_QueueSend_Multiple(t *testing.T) {
	t.Parallel()
	queue := "test-multi-" + randomSuffix()
	ctx := context.Background()

	adapter, err := NewQueueAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer adapter.Close()

	payloads := []string{"first", "second", "third"}
	for _, p := range payloads {
		if err := adapter.Send(ctx, backends.SendOptions{Queue: queue, Message: []byte(p)}); err != nil {
			t.Fatalf("Send %q: %v", p, err)
		}
	}

	for i, want := range payloads {
		msg, err := adapter.Receive(ctx, backends.ReceiveOptions{Queue: queue, Acknowledge: true, Timeout: 5})
		if err != nil {
			t.Fatalf("Receive[%d]: %v", i, err)
		}
		if string(msg.Data) != want {
			t.Errorf("message[%d]: expected %q, got %q", i, want, msg.Data)
		}
	}
}

// --- Topic tests (core NATS pub/sub) ---

func TestNATS_TopicPublishSubscribe(t *testing.T) {
	t.Parallel()
	topic := "test.topic." + randomSuffix()
	ctx := context.Background()

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
		msg, err := subAdapter.Subscribe(ctx, backends.SubscribeOptions{Topic: topic, Timeout: 10})
		ch <- result{msg, err}
	}()

	// Give subscriber time to register
	time.Sleep(200 * time.Millisecond)

	payload := []byte("hello topic")
	if err := pubAdapter.Publish(ctx, backends.PublishOptions{Topic: topic, Message: payload}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case r := <-ch:
		if r.err != nil {
			t.Fatalf("Subscribe: %v", r.err)
		}
		if string(r.msg.Data) != string(payload) {
			t.Fatalf("expected %q, got %q", payload, r.msg.Data)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for subscribed message")
	}
}

func TestNATS_TopicPublishSubscribe_QueueGroup(t *testing.T) {
	t.Parallel()
	topic := "test.topic.group." + randomSuffix()
	group := "test-group"
	ctx := context.Background()

	sub1, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter 1: %v", err)
	}
	defer sub1.Close()

	sub2, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter 2: %v", err)
	}
	defer sub2.Close()

	pub, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter pub: %v", err)
	}
	defer pub.Close()

	ch1 := make(chan *backends.Message, 1)
	ch2 := make(chan *backends.Message, 1)

	go func() {
		msg, err := sub1.Subscribe(ctx, backends.SubscribeOptions{Topic: topic, GroupID: group, Timeout: 10})
		if err == nil {
			ch1 <- msg
		}
	}()
	go func() {
		msg, err := sub2.Subscribe(ctx, backends.SubscribeOptions{Topic: topic, GroupID: group, Timeout: 10})
		if err == nil {
			ch2 <- msg
		}
	}()

	// Give both subscribers time to register
	time.Sleep(200 * time.Millisecond)

	if err := pub.Publish(ctx, backends.PublishOptions{Topic: topic, Message: []byte("queue group msg")}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	var received *backends.Message
	select {
	case msg := <-ch1:
		received = msg
	case msg := <-ch2:
		received = msg
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for queue group message")
	}

	if string(received.Data) != "queue group msg" {
		t.Fatalf("expected %q, got %q", "queue group msg", received.Data)
	}

	// Verify only ONE subscriber received the message
	select {
	case <-ch1:
		t.Fatal("both subscribers received the message; expected exactly one")
	case <-ch2:
		t.Fatal("both subscribers received the message; expected exactly one")
	case <-time.After(500 * time.Millisecond):
		// expected: no second delivery
	}
}
