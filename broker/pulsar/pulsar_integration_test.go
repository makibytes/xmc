//go:build pulsar && integration

package pulsar

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
	broker, err := integration.StartPulsar(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start Pulsar: %v\n", err)
		os.Exit(1)
	}
	defer broker.Terminate(ctx)
	testServer = broker.URL
	os.Exit(m.Run())
}

func makeConnArgs() ConnArguments {
	return ConnArguments{Server: testServer}
}

func randomSuffix() string { return fmt.Sprintf("%d", rand.Int63()) }

// newQueueAdapter creates a QueueAdapter with retry for slow Pulsar startup.
func newQueueAdapter(t *testing.T) *QueueAdapter {
	t.Helper()
	var a *QueueAdapter
	err := integration.WaitForBroker(func() error {
		var e error
		a, e = NewQueueAdapter(makeConnArgs())
		return e
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	return a
}

// newTopicAdapter creates a TopicAdapter with retry for slow Pulsar startup.
func newTopicAdapter(t *testing.T) *TopicAdapter {
	t.Helper()
	var a *TopicAdapter
	err := integration.WaitForBroker(func() error {
		var e error
		a, e = NewTopicAdapter(makeConnArgs())
		return e
	}, 30*time.Second)
	if err != nil {
		t.Fatalf("NewTopicAdapter: %v", err)
	}
	return a
}

// --- Queue tests (Shared subscription) ---

func TestPulsar_QueueSendReceive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	queue := "test-queue-" + randomSuffix()
	payload := []byte("hello pulsar")

	a := newQueueAdapter(t)
	defer a.Close()

	if err := a.Send(ctx, backends.SendOptions{Queue: queue, Message: payload}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msg, err := a.Receive(ctx, backends.ReceiveOptions{Queue: queue, Timeout: 10, Acknowledge: true})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(msg.Data) != string(payload) {
		t.Errorf("payload: got %q, want %q", msg.Data, payload)
	}
}

func TestPulsar_QueuePeek(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	queue := "test-queue-peek-" + randomSuffix()
	payload := []byte("peek me")

	a := newQueueAdapter(t)
	defer a.Close()

	if err := a.Send(ctx, backends.SendOptions{Queue: queue, Message: payload}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Peek (nack — message stays available)
	peeked, err := a.Receive(ctx, backends.ReceiveOptions{Queue: queue, Timeout: 10, Acknowledge: false})
	if err != nil {
		t.Fatalf("Peek Receive: %v", err)
	}
	if string(peeked.Data) != string(payload) {
		t.Errorf("peek payload: got %q, want %q", peeked.Data, payload)
	}

	// Receive again — message should still be available after nack
	msg, err := a.Receive(ctx, backends.ReceiveOptions{Queue: queue, Timeout: 10, Acknowledge: true})
	if err != nil {
		t.Fatalf("second Receive after peek: %v", err)
	}
	if string(msg.Data) != string(payload) {
		t.Errorf("second receive payload: got %q, want %q", msg.Data, payload)
	}
}

func TestPulsar_QueueSendReceive_Properties(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	queue := "test-queue-props-" + randomSuffix()

	a := newQueueAdapter(t)
	defer a.Close()

	props := map[string]any{"env": "test", "version": "42"}
	if err := a.Send(ctx, backends.SendOptions{
		Queue:      queue,
		Message:    []byte("with properties"),
		Properties: props,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msg, err := a.Receive(ctx, backends.ReceiveOptions{Queue: queue, Timeout: 10, Acknowledge: true})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	for k, want := range props {
		got, ok := msg.Properties[k]
		if !ok {
			t.Errorf("property %q missing", k)
			continue
		}
		if fmt.Sprintf("%v", got) != fmt.Sprintf("%v", want) {
			t.Errorf("property %q: got %v, want %v", k, got, want)
		}
	}
}

func TestPulsar_QueueReceive_Timeout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	queue := "test-queue-timeout-" + randomSuffix()

	a := newQueueAdapter(t)
	defer a.Close()

	_, err := a.Receive(ctx, backends.ReceiveOptions{Queue: queue, Timeout: 2})
	if err == nil {
		t.Fatal("expected error on empty queue receive, got nil")
	}
	if !strings.Contains(err.Error(), "no message available") {
		t.Errorf("unexpected error: %v", err)
	}
}

func TestPulsar_QueueSendReceive_CorrelationID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	queue := "test-queue-correlid-" + randomSuffix()
	corrID := "corr-" + randomSuffix()

	a := newQueueAdapter(t)
	defer a.Close()

	if err := a.Send(ctx, backends.SendOptions{
		Queue:         queue,
		Message:       []byte("correlated"),
		CorrelationID: corrID,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msg, err := a.Receive(ctx, backends.ReceiveOptions{Queue: queue, Timeout: 10, Acknowledge: true})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if msg.CorrelationID != corrID {
		t.Errorf("CorrelationID: got %q, want %q", msg.CorrelationID, corrID)
	}
}

// --- Topic tests (Exclusive subscription) ---

func TestPulsar_TopicPublishSubscribe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	topic := "persistent://public/default/test-topic-" + randomSuffix()
	payload := []byte("hello topic")

	subAdapter := newTopicAdapter(t)
	defer subAdapter.Close()

	pubAdapter := newTopicAdapter(t)
	defer pubAdapter.Close()

	type result struct {
		msg *backends.Message
		err error
	}
	ch := make(chan result, 1)

	go func() {
		msg, err := subAdapter.Subscribe(ctx, backends.SubscribeOptions{Topic: topic, Timeout: 15})
		ch <- result{msg, err}
	}()

	// Give subscriber time to attach before publishing
	time.Sleep(300 * time.Millisecond)

	if err := pubAdapter.Publish(ctx, backends.PublishOptions{Topic: topic, Message: payload}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	res := <-ch
	if res.err != nil {
		t.Fatalf("Subscribe: %v", res.err)
	}
	if string(res.msg.Data) != string(payload) {
		t.Errorf("payload: got %q, want %q", res.msg.Data, payload)
	}
}

func TestPulsar_TopicPublishSubscribe_Key(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	topic := "persistent://public/default/test-topic-key-" + randomSuffix()
	payload := []byte("keyed message")
	key := "partition-key-" + randomSuffix()

	subAdapter := newTopicAdapter(t)
	defer subAdapter.Close()

	pubAdapter := newTopicAdapter(t)
	defer pubAdapter.Close()

	type result struct {
		msg *backends.Message
		err error
	}
	ch := make(chan result, 1)

	go func() {
		msg, err := subAdapter.Subscribe(ctx, backends.SubscribeOptions{Topic: topic, Timeout: 15})
		ch <- result{msg, err}
	}()

	time.Sleep(300 * time.Millisecond)

	if err := pubAdapter.Publish(ctx, backends.PublishOptions{
		Topic:   topic,
		Message: payload,
		Key:     key,
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	res := <-ch
	if res.err != nil {
		t.Fatalf("Subscribe: %v", res.err)
	}
	if string(res.msg.Data) != string(payload) {
		t.Errorf("payload: got %q, want %q", res.msg.Data, payload)
	}
	if res.msg.MessageID != key {
		t.Errorf("Key (MessageID): got %q, want %q", res.msg.MessageID, key)
	}
}

func TestPulsar_TopicSubscribe_GroupID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	topic := "persistent://public/default/test-topic-group-" + randomSuffix()
	groupID := "my-group-" + randomSuffix()
	payload := []byte("shared subscription message")

	subAdapter := newTopicAdapter(t)
	defer subAdapter.Close()

	pubAdapter := newTopicAdapter(t)
	defer pubAdapter.Close()

	type result struct {
		msg *backends.Message
		err error
	}
	ch := make(chan result, 1)

	// GroupID triggers Shared subscription type in topic_adapter.go
	go func() {
		msg, err := subAdapter.Subscribe(ctx, backends.SubscribeOptions{
			Topic:   topic,
			GroupID: groupID,
			Timeout: 15,
		})
		ch <- result{msg, err}
	}()

	time.Sleep(300 * time.Millisecond)

	if err := pubAdapter.Publish(ctx, backends.PublishOptions{Topic: topic, Message: payload}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	res := <-ch
	if res.err != nil {
		t.Fatalf("Subscribe with GroupID: %v", res.err)
	}
	if string(res.msg.Data) != string(payload) {
		t.Errorf("payload: got %q, want %q", res.msg.Data, payload)
	}
}
