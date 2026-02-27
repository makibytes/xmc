//go:build artemis && integration

package artemis

import (
	"context"
	"fmt"
	"math/rand"
	"os"
	"testing"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/test/integration"
)

var testServer string
var testUser = "artemis"
var testPass = "artemis"

func TestMain(m *testing.M) {
	ctx := context.Background()
	broker, err := integration.StartArtemis(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start Artemis: %v\n", err)
		os.Exit(1)
	}
	defer broker.Terminate(ctx)
	testServer = broker.URL

	// Wait until Artemis is fully ready (TCP open doesn't mean AMQP is ready)
	err = integration.WaitForBroker(func() error {
		a, err := NewQueueAdapter(makeConnArgs())
		if err != nil {
			return err
		}
		a.Close()
		return nil
	}, 30*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "Artemis not ready: %v\n", err)
		os.Exit(1)
	}

	os.Exit(m.Run())
}

func makeConnArgs() ConnArguments {
	return ConnArguments{
		Server:   testServer,
		User:     testUser,
		Password: testPass,
	}
}

func randomSuffix() string { return fmt.Sprintf("%d", rand.Int63()) }

// --- Queue tests ---

func TestArtemis_QueueSendReceive(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip()
	}
	queue := "xmc-test-queue-" + randomSuffix()
	connArgs := makeConnArgs()

	sender, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer sender.Close()

	err = sender.Send(context.Background(), backends.SendOptions{
		Queue:   queue,
		Message: []byte("hello-queue"),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	receiver, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter (receiver): %v", err)
	}
	defer receiver.Close()

	msg, err := receiver.Receive(context.Background(), backends.ReceiveOptions{
		Queue:       queue,
		Acknowledge: true,
		Timeout:     5,
	})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(msg.Data) != "hello-queue" {
		t.Errorf("expected %q, got %q", "hello-queue", string(msg.Data))
	}
}

func TestArtemis_QueueSendReceive_Properties(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip()
	}
	queue := "xmc-test-queue-" + randomSuffix()
	connArgs := makeConnArgs()

	sender, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer sender.Close()

	props := map[string]any{
		"color": "blue",
		"count": int32(42),
	}
	err = sender.Send(context.Background(), backends.SendOptions{
		Queue:      queue,
		Message:    []byte("props-message"),
		Properties: props,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	receiver, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter (receiver): %v", err)
	}
	defer receiver.Close()

	msg, err := receiver.Receive(context.Background(), backends.ReceiveOptions{
		Queue:                     queue,
		Acknowledge:               true,
		Timeout:                   5,
		WithApplicationProperties: true,
	})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if msg.Properties == nil {
		t.Fatal("expected Properties map, got nil")
	}
	if msg.Properties["color"] != "blue" {
		t.Errorf("expected color=%q, got %v", "blue", msg.Properties["color"])
	}
}

func TestArtemis_QueuePeek(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip()
	}
	queue := "xmc-test-queue-" + randomSuffix()
	connArgs := makeConnArgs()

	sender, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer sender.Close()

	err = sender.Send(context.Background(), backends.SendOptions{
		Queue:   queue,
		Message: []byte("peek-me"),
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	// First peek (non-destructive)
	peeker, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter (peeker): %v", err)
	}
	defer peeker.Close()

	msg, err := peeker.Receive(context.Background(), backends.ReceiveOptions{
		Queue:       queue,
		Acknowledge: false,
		Timeout:     5,
	})
	if err != nil {
		t.Fatalf("Peek (first): %v", err)
	}
	if string(msg.Data) != "peek-me" {
		t.Errorf("expected %q, got %q", "peek-me", string(msg.Data))
	}

	// Second peek should still find the message
	peeker2, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter (peeker2): %v", err)
	}
	defer peeker2.Close()

	msg2, err := peeker2.Receive(context.Background(), backends.ReceiveOptions{
		Queue:       queue,
		Acknowledge: false,
		Timeout:     5,
	})
	if err != nil {
		t.Fatalf("Peek (second): %v", err)
	}
	if string(msg2.Data) != "peek-me" {
		t.Errorf("message gone after peek; expected %q, got %q", "peek-me", string(msg2.Data))
	}
}

func TestArtemis_QueueReceive_Timeout(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip()
	}
	queue := "xmc-test-empty-" + randomSuffix()
	connArgs := makeConnArgs()

	receiver, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer receiver.Close()

	msg, err := receiver.Receive(context.Background(), backends.ReceiveOptions{
		Queue:       queue,
		Acknowledge: true,
		Timeout:     1,
		Wait:        false,
	})
	if msg != nil {
		t.Errorf("expected nil message from empty queue, got: %v", msg)
	}
	if err == nil {
		t.Error("expected an error from empty-queue receive, got nil")
	}
}

func TestArtemis_QueueSendReceive_MessageID(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip()
	}
	queue := "xmc-test-queue-" + randomSuffix()
	msgID := "my-message-id-" + randomSuffix()
	connArgs := makeConnArgs()

	sender, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer sender.Close()

	err = sender.Send(context.Background(), backends.SendOptions{
		Queue:     queue,
		Message:   []byte("id-test"),
		MessageID: msgID,
	})
	if err != nil {
		t.Fatalf("Send: %v", err)
	}

	receiver, err := NewQueueAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewQueueAdapter (receiver): %v", err)
	}
	defer receiver.Close()

	msg, err := receiver.Receive(context.Background(), backends.ReceiveOptions{
		Queue:                   queue,
		Acknowledge:             true,
		Timeout:                 5,
		WithHeaderAndProperties: true,
	})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if msg.MessageID != msgID {
		t.Errorf("expected MessageID=%q, got %q", msgID, msg.MessageID)
	}
}

// --- Topic tests ---

func TestArtemis_TopicPublishSubscribe(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip()
	}
	topic := "xmc-test-topic-" + randomSuffix()
	connArgs := makeConnArgs()

	msgCh := make(chan *backends.Message, 1)
	errCh := make(chan error, 1)
	go func() {
		adapter, err := NewTopicAdapter(connArgs)
		if err != nil {
			errCh <- err
			return
		}
		defer adapter.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		msg, err := adapter.Subscribe(ctx, backends.SubscribeOptions{
			Topic:   topic,
			Timeout: 10,
			Wait:    false,
		})
		if err != nil {
			errCh <- err
			return
		}
		msgCh <- msg
	}()

	// Give subscriber time to attach
	time.Sleep(500 * time.Millisecond)

	pub, err := NewTopicAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewTopicAdapter (publisher): %v", err)
	}
	defer pub.Close()

	err = pub.Publish(context.Background(), backends.PublishOptions{
		Topic:   topic,
		Message: []byte("hello-topic"),
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case msg := <-msgCh:
		if string(msg.Data) != "hello-topic" {
			t.Errorf("expected %q, got %q", "hello-topic", string(msg.Data))
		}
	case err := <-errCh:
		t.Fatalf("subscribe error: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for subscribed message")
	}
}

func TestArtemis_TopicPublishSubscribe_Properties(t *testing.T) {
	t.Parallel()
	if testing.Short() {
		t.Skip()
	}
	topic := "xmc-test-topic-" + randomSuffix()
	connArgs := makeConnArgs()

	msgCh := make(chan *backends.Message, 1)
	errCh := make(chan error, 1)
	go func() {
		adapter, err := NewTopicAdapter(connArgs)
		if err != nil {
			errCh <- err
			return
		}
		defer adapter.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cancel()
		msg, err := adapter.Subscribe(ctx, backends.SubscribeOptions{
			Topic:                     topic,
			Timeout:                   10,
			Wait:                      false,
			WithApplicationProperties: true,
		})
		if err != nil {
			errCh <- err
			return
		}
		msgCh <- msg
	}()

	time.Sleep(500 * time.Millisecond)

	pub, err := NewTopicAdapter(connArgs)
	if err != nil {
		t.Fatalf("NewTopicAdapter (publisher): %v", err)
	}
	defer pub.Close()

	err = pub.Publish(context.Background(), backends.PublishOptions{
		Topic:   topic,
		Message: []byte("props-topic"),
		Properties: map[string]any{
			"env": "test",
		},
	})
	if err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case msg := <-msgCh:
		if msg.Properties == nil {
			t.Fatal("expected Properties map, got nil")
		}
		if msg.Properties["env"] != "test" {
			t.Errorf("expected env=%q, got %v", "test", msg.Properties["env"])
		}
	case err := <-errCh:
		t.Fatalf("subscribe error: %v", err)
	case <-time.After(15 * time.Second):
		t.Fatal("timeout waiting for subscribed message")
	}
}
