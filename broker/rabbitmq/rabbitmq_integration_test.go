//go:build rabbitmq && integration

package rabbitmq

import (
	"context"
	"fmt"
	"net/url"
	"os"
	"testing"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/test/integration"
)

var testServer string
var testUser string
var testPassword string
var testMgmtURL string

// testQueues lists all queues used by the integration tests so they can be
// pre-declared via the management API (RabbitMQ 4.x does not auto-create
// queues over AMQP 1.0).
var testQueues = []string{
	"test.send.receive",
	"test.properties",
	"test.peek",
	"test.timeout.empty",
	"test.correlation",
}

func TestMain(m *testing.M) {
	ctx := context.Background()
	broker, err := integration.StartRabbitMQ(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start RabbitMQ: %v\n", err)
		os.Exit(1)
	}
	defer broker.Terminate(ctx)

	testServer, testUser, testPassword = parseRabbitMQURL(broker.URL)
	testMgmtURL = broker.ManagementURL

	err = integration.WaitForBroker(func() error {
		conn := ConnArguments{
			Server:   testServer,
			User:     testUser,
			Password: testPassword,
		}
		c, s, err := Connect(conn)
		if err != nil {
			return err
		}
		s.Close(context.Background())
		c.Close()
		return nil
	}, 30*time.Second)
	if err != nil {
		fmt.Fprintf(os.Stderr, "RabbitMQ not ready: %v\n", err)
		os.Exit(1)
	}

	for _, q := range testQueues {
		if err := integration.DeclareRabbitMQQueue(testMgmtURL, testUser, testPassword, q); err != nil {
			fmt.Fprintf(os.Stderr, "failed to declare queue %s: %v\n", q, err)
			os.Exit(1)
		}
	}

	os.Exit(m.Run())
}

func parseRabbitMQURL(rawURL string) (server, user, password string) {
	u, _ := url.Parse(rawURL)
	user = u.User.Username()
	password, _ = u.User.Password()
	server = fmt.Sprintf("amqp://%s", u.Host)
	return
}

func newConnArgs() ConnArguments {
	return ConnArguments{
		Server:   testServer,
		User:     testUser,
		Password: testPassword,
	}
}

// TestRabbitMQ_QueueSendReceive verifies a basic text message roundtrip via a queue.
func TestRabbitMQ_QueueSendReceive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	queue := "test.send.receive"
	payload := []byte("hello rabbitmq")

	sender, err := NewQueueAdapter(newConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer sender.Close()

	if err := sender.Send(ctx, backends.SendOptions{Queue: queue, Message: payload}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	receiver, err := NewQueueAdapter(newConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter (receiver): %v", err)
	}
	defer receiver.Close()

	msg, err := receiver.Receive(ctx, backends.ReceiveOptions{
		Queue:       queue,
		Acknowledge: true,
		Timeout:     5,
	})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(msg.Data) != string(payload) {
		t.Errorf("expected %q, got %q", payload, msg.Data)
	}
}

// TestRabbitMQ_QueueSendReceive_Properties verifies application properties are preserved.
func TestRabbitMQ_QueueSendReceive_Properties(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	queue := "test.properties"
	payload := []byte("props message")
	props := map[string]any{
		"colour": "blue",
		"count":  int32(42),
	}

	sender, err := NewQueueAdapter(newConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer sender.Close()

	if err := sender.Send(ctx, backends.SendOptions{
		Queue:      queue,
		Message:    payload,
		Properties: props,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	receiver, err := NewQueueAdapter(newConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter (receiver): %v", err)
	}
	defer receiver.Close()

	msg, err := receiver.Receive(ctx, backends.ReceiveOptions{
		Queue:       queue,
		Acknowledge: true,
		Timeout:     5,
		Verbosity:   backends.VerbosityNormal,
	})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if msg.Properties == nil {
		t.Fatal("expected Properties to be non-nil")
	}
	if msg.Properties["colour"] != "blue" {
		t.Errorf("expected colour=blue, got %v", msg.Properties["colour"])
	}
}

// TestRabbitMQ_QueuePeek verifies that peeking does not consume the message.
func TestRabbitMQ_QueuePeek(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	queue := "test.peek"
	payload := []byte("peek me")

	sender, err := NewQueueAdapter(newConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer sender.Close()

	if err := sender.Send(ctx, backends.SendOptions{Queue: queue, Message: payload}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	// Peek (Acknowledge: false) — message should remain on the queue.
	peeker, err := NewQueueAdapter(newConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter (peeker): %v", err)
	}
	defer peeker.Close()

	msg, err := peeker.Receive(ctx, backends.ReceiveOptions{
		Queue:       queue,
		Acknowledge: false,
		Timeout:     5,
	})
	if err != nil {
		t.Fatalf("Peek Receive: %v", err)
	}
	if string(msg.Data) != string(payload) {
		t.Errorf("expected %q, got %q", payload, msg.Data)
	}

	// Consume it for cleanup.
	consumer, err := NewQueueAdapter(newConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter (consumer): %v", err)
	}
	defer consumer.Close()

	_, err = consumer.Receive(ctx, backends.ReceiveOptions{
		Queue:       queue,
		Acknowledge: true,
		Timeout:     5,
	})
	if err != nil {
		t.Fatalf("cleanup Receive: %v", err)
	}
}

// TestRabbitMQ_QueueReceive_Timeout verifies that receiving from an empty queue returns an error.
func TestRabbitMQ_QueueReceive_Timeout(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	queue := "test.timeout.empty"

	receiver, err := NewQueueAdapter(newConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer receiver.Close()

	msg, err := receiver.Receive(ctx, backends.ReceiveOptions{
		Queue:       queue,
		Acknowledge: true,
		Timeout:     1,
	})
	if err == nil && msg != nil {
		t.Error("expected error or nil message on empty queue, got a message")
	}
}

// TestRabbitMQ_QueueSendReceive_CorrelationID verifies that CorrelationID is preserved.
func TestRabbitMQ_QueueSendReceive_CorrelationID(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	queue := "test.correlation"
	payload := []byte("corr message")
	corrID := "test-corr-id-123"

	sender, err := NewQueueAdapter(newConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer sender.Close()

	if err := sender.Send(ctx, backends.SendOptions{
		Queue:         queue,
		Message:       payload,
		CorrelationID: corrID,
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	receiver, err := NewQueueAdapter(newConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter (receiver): %v", err)
	}
	defer receiver.Close()

	msg, err := receiver.Receive(ctx, backends.ReceiveOptions{
		Queue:       queue,
		Acknowledge: true,
		Timeout:     5,
	})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if msg.CorrelationID != corrID {
		t.Errorf("expected CorrelationID=%q, got %q", corrID, msg.CorrelationID)
	}
}

// TestRabbitMQ_TopicPublishSubscribe verifies publish and subscribe via the amq.topic exchange.
// RabbitMQ 4.x requires a queue bound to the exchange for AMQP 1.0 subscriptions.
func TestRabbitMQ_TopicPublishSubscribe(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	topic := "test.topic.pubsub"
	subQueue := "test.topic.pubsub.sub"
	payload := []byte("topic message")

	if err := integration.DeclareRabbitMQQueue(testMgmtURL, testUser, testPassword, subQueue); err != nil {
		t.Fatalf("declare subscriber queue: %v", err)
	}
	if err := integration.BindRabbitMQQueue(testMgmtURL, testUser, testPassword, subQueue, "amq.topic", topic); err != nil {
		t.Fatalf("bind subscriber queue: %v", err)
	}

	// Start the subscriber on the bound queue.
	receiver, err := NewQueueAdapter(newConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter (subscriber): %v", err)
	}
	defer receiver.Close()

	type result struct {
		msg *backends.Message
		err error
	}
	ch := make(chan result, 1)

	go func() {
		msg, err := receiver.Receive(ctx, backends.ReceiveOptions{
			Queue:       subQueue,
			Acknowledge: true,
			Timeout:     10,
			Wait:        true,
		})
		ch <- result{msg, err}
	}()

	// Give subscriber time to set up before publishing.
	time.Sleep(500 * time.Millisecond)

	publisher, err := NewTopicAdapter(newConnArgs(), "amq.topic")
	if err != nil {
		t.Fatalf("NewTopicAdapter (publisher): %v", err)
	}
	defer publisher.Close()

	if err := publisher.Publish(ctx, backends.PublishOptions{
		Topic:   topic,
		Message: payload,
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case res := <-ch:
		if res.err != nil {
			t.Fatalf("Subscribe: %v", res.err)
		}
		if string(res.msg.Data) != string(payload) {
			t.Errorf("expected %q, got %q", payload, res.msg.Data)
		}
	case <-time.After(10 * time.Second):
		t.Fatal("timeout waiting for subscribed message")
	}
}
