//go:build kafka && integration

package kafka

import (
	"context"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	integration "github.com/makibytes/xmc/test/integration"
)

var testBroker string // just "host:port"

func TestMain(m *testing.M) {
	ctx := context.Background()
	broker, err := integration.StartKafka(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start Kafka: %v\n", err)
		os.Exit(1)
	}
	defer broker.Terminate(ctx)
	// Strip "kafka://" prefix
	testBroker = strings.TrimPrefix(broker.URL, "kafka://")
	os.Exit(m.Run())
}

func makeConnArgs() ConnArguments {
	return ConnArguments{Server: testBroker}
}

// TestKafka_TopicPublishSubscribe verifies that a published message is received
// with the correct payload.
func TestKafka_TopicPublishSubscribe(t *testing.T) {
	t.Parallel()
	topic := "test-publish-subscribe"
	payload := []byte("hello kafka")

	adapter, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter: %v", err)
	}
	defer adapter.Close()

	errCh := make(chan error, 1)
	msgCh := make(chan *backends.Message, 1)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		msg, err := adapter.Subscribe(ctx, backends.SubscribeOptions{
			Topic:   topic,
			GroupID: "test-group-basic",
			Timeout: 25,
		})
		if err != nil {
			errCh <- err
			return
		}
		msgCh <- msg
	}()

	time.Sleep(500 * time.Millisecond)

	ctx := context.Background()
	err = adapter.Publish(ctx, backends.PublishOptions{
		Topic:   topic,
		Message: payload,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("Subscribe error: %v", err)
	case msg := <-msgCh:
		if string(msg.Data) != string(payload) {
			t.Errorf("expected payload %q, got %q", payload, msg.Data)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

// TestKafka_TopicPublishSubscribe_Properties verifies that application
// properties survive a publish/subscribe round-trip.
func TestKafka_TopicPublishSubscribe_Properties(t *testing.T) {
	t.Parallel()
	topic := "test-publish-properties"

	adapter, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter: %v", err)
	}
	defer adapter.Close()

	errCh := make(chan error, 1)
	msgCh := make(chan *backends.Message, 1)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		msg, err := adapter.Subscribe(ctx, backends.SubscribeOptions{
			Topic:                     topic,
			GroupID:                   "test-group-props",
			Timeout:                   25,
			WithApplicationProperties: true,
		})
		if err != nil {
			errCh <- err
			return
		}
		msgCh <- msg
	}()

	time.Sleep(500 * time.Millisecond)

	ctx := context.Background()
	err = adapter.Publish(ctx, backends.PublishOptions{
		Topic:   topic,
		Message: []byte("props test"),
		Properties: map[string]any{
			"x-custom": "value1",
			"env":      "testing",
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("Subscribe error: %v", err)
	case msg := <-msgCh:
		if v, ok := msg.Properties["x-custom"]; !ok || v != "value1" {
			t.Errorf("expected property x-custom=value1, got %v", msg.Properties)
		}
		if v, ok := msg.Properties["env"]; !ok || v != "testing" {
			t.Errorf("expected property env=testing, got %v", msg.Properties)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

// TestKafka_TopicPublishSubscribe_Key verifies that publishing with a key
// results in a message being received (key visible in metadata).
func TestKafka_TopicPublishSubscribe_Key(t *testing.T) {
	t.Parallel()
	topic := "test-publish-key"
	messageKey := "partition-key-1"

	adapter, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter: %v", err)
	}
	defer adapter.Close()

	errCh := make(chan error, 1)
	msgCh := make(chan *backends.Message, 1)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		msg, err := adapter.Subscribe(ctx, backends.SubscribeOptions{
			Topic:                   topic,
			GroupID:                 "test-group-key",
			Timeout:                 25,
			WithHeaderAndProperties: true,
		})
		if err != nil {
			errCh <- err
			return
		}
		msgCh <- msg
	}()

	time.Sleep(500 * time.Millisecond)

	ctx := context.Background()
	err = adapter.Publish(ctx, backends.PublishOptions{
		Topic:   topic,
		Message: []byte("keyed message"),
		Key:     messageKey,
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("Subscribe error: %v", err)
	case msg := <-msgCh:
		if string(msg.Data) != "keyed message" {
			t.Errorf("expected payload %q, got %q", "keyed message", msg.Data)
		}
		// Key is exposed in InternalMetadata when WithHeaderAndProperties=true
		if k, ok := msg.InternalMetadata["Key"]; !ok || k != messageKey {
			t.Errorf("expected InternalMetadata[Key]=%q, got %v", messageKey, msg.InternalMetadata)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

// TestKafka_TopicSubscribe_ConsumerGroup verifies that subscribe works with a
// named consumer group.
func TestKafka_TopicSubscribe_ConsumerGroup(t *testing.T) {
	t.Parallel()
	topic := "test-consumer-group"
	groupID := "test-cg-shared"

	adapter, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter: %v", err)
	}
	defer adapter.Close()

	errCh := make(chan error, 1)
	msgCh := make(chan *backends.Message, 1)

	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		msg, err := adapter.Subscribe(ctx, backends.SubscribeOptions{
			Topic:   topic,
			GroupID: groupID,
			Timeout: 25,
		})
		if err != nil {
			errCh <- err
			return
		}
		msgCh <- msg
	}()

	time.Sleep(500 * time.Millisecond)

	ctx := context.Background()
	err = adapter.Publish(ctx, backends.PublishOptions{
		Topic:   topic,
		Message: []byte("group message"),
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("Subscribe error: %v", err)
	case msg := <-msgCh:
		if string(msg.Data) != "group message" {
			t.Errorf("expected %q, got %q", "group message", msg.Data)
		}
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for message")
	}
}

// TestKafka_TopicSubscribe_Timeout verifies that subscribing to an empty topic
// with a short timeout returns an error.
func TestKafka_TopicSubscribe_Timeout(t *testing.T) {
	t.Parallel()
	topic := "test-subscribe-timeout-empty"

	adapter, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter: %v", err)
	}
	defer adapter.Close()

	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()

	_, err = adapter.Subscribe(ctx, backends.SubscribeOptions{
		Topic:   topic,
		GroupID: "test-group-timeout",
		Timeout: 2,
	})
	if err == nil {
		t.Fatal("expected timeout error, got nil")
	}
}
