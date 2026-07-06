//go:build redis && integration

package redis

import (
	"context"
	"errors"
	"fmt"
	"os"
	"testing"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/test/integration"
)

var testServer string

func TestMain(m *testing.M) {
	ctx := context.Background()
	broker, err := integration.StartRedis(ctx)
	if err != nil {
		fmt.Fprintf(os.Stderr, "failed to start Redis: %v\n", err)
		os.Exit(1)
	}
	defer broker.Terminate(ctx)
	testServer = broker.URL
	os.Exit(m.Run())
}

func makeConnArgs() ConnArguments {
	return ConnArguments{Server: testServer}
}

// TestRedis_QueueSendReceive verifies a send/receive roundtrip with metadata.
func TestRedis_QueueSendReceive(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	queue := "xmc:queue:test.send.receive"
	payload := []byte("hello redis")

	adapter, err := NewQueueAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer adapter.Close()

	if err := adapter.Send(ctx, backends.SendOptions{
		Queue:         queue,
		Message:       payload,
		CorrelationID: "corr-1",
		ContentType:   "text/plain",
		Properties:    map[string]any{"colour": "red"},
	}); err != nil {
		t.Fatalf("Send: %v", err)
	}

	msg, err := adapter.Receive(ctx, backends.ReceiveOptions{
		Queue:       queue,
		Acknowledge: true,
		Timeout:     5,
	})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if string(msg.Data) != string(payload) {
		t.Errorf("payload: got %q, want %q", msg.Data, payload)
	}
	if msg.CorrelationID != "corr-1" {
		t.Errorf("CorrelationID: got %q, want corr-1", msg.CorrelationID)
	}
	if msg.Properties["colour"] != "red" {
		t.Errorf("colour property: got %v, want red", msg.Properties["colour"])
	}
	// Sender set no message ID → back-filled with the stream entry ID.
	if msg.MessageID == "" || msg.MessageID != msg.InternalMetadata["stream-id"] {
		t.Errorf("MessageID: got %q, want back-filled stream ID %v", msg.MessageID, msg.InternalMetadata["stream-id"])
	}

	// A sender-set message ID must win over the back-fill.
	if err := adapter.Send(ctx, backends.SendOptions{
		Queue:     queue,
		Message:   payload,
		MessageID: "explicit-id",
	}); err != nil {
		t.Fatalf("Send with MessageID: %v", err)
	}
	msg, err = adapter.Receive(ctx, backends.ReceiveOptions{
		Queue:       queue,
		Acknowledge: true,
		Timeout:     5,
	})
	if err != nil {
		t.Fatalf("Receive: %v", err)
	}
	if msg.MessageID != "explicit-id" {
		t.Errorf("MessageID: got %q, want explicit-id (sender-set must not be overwritten)", msg.MessageID)
	}
}

// TestRedis_QueuePeekAndBrowse verifies that peek does not consume and that the
// browse cursor advances entry by entry.
func TestRedis_QueuePeekAndBrowse(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	queue := "xmc:queue:test.peek"

	adapter, err := NewQueueAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewQueueAdapter: %v", err)
	}
	defer adapter.Close()

	for i := 0; i < 3; i++ {
		if err := adapter.Send(ctx, backends.SendOptions{
			Queue:   queue,
			Message: []byte(fmt.Sprintf("msg %d", i)),
		}); err != nil {
			t.Fatalf("Send %d: %v", i, err)
		}
	}

	// Stateless peek returns the head without consuming.
	msg, err := adapter.Receive(ctx, backends.ReceiveOptions{Queue: queue, Acknowledge: false})
	if err != nil {
		t.Fatalf("peek: %v", err)
	}
	if string(msg.Data) != "msg 0" {
		t.Errorf("peek: got %q, want %q", msg.Data, "msg 0")
	}

	// Browse walks all entries exactly once.
	browser, err := adapter.Browse(ctx, backends.ReceiveOptions{Queue: queue})
	if err != nil {
		t.Fatalf("Browse: %v", err)
	}
	defer browser.Close()
	for i := 0; i < 3; i++ {
		msg, err := browser.Next(ctx)
		if err != nil {
			t.Fatalf("Next %d: %v", i, err)
		}
		if want := fmt.Sprintf("msg %d", i); string(msg.Data) != want {
			t.Errorf("browse %d: got %q, want %q", i, msg.Data, want)
		}
	}
	if _, err := browser.Next(ctx); !errors.Is(err, backends.ErrNoMessageAvailable) {
		t.Errorf("expected ErrNoMessageAvailable at end of browse, got %v", err)
	}

	// All three messages must still be receivable (peek/browse were non-destructive).
	for i := 0; i < 3; i++ {
		if _, err := adapter.Receive(ctx, backends.ReceiveOptions{
			Queue: queue, Acknowledge: true, Timeout: 5,
		}); err != nil {
			t.Fatalf("Receive %d after browse: %v", i, err)
		}
	}
}

// TestRedis_TopicSubscribeIndependent_NoGapBetweenPolls verifies that a
// group-less subscriber does not lose entries published between two polls
// (the "$" re-evaluation gap).
func TestRedis_TopicSubscribeIndependent_NoGapBetweenPolls(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	topic := "xmc:topic:test.gap"

	sub, err := NewTopicAdapter(makeConnArgs(), 0)
	if err != nil {
		t.Fatalf("NewTopicAdapter: %v", err)
	}
	defer sub.Close()

	pub, err := NewTopicAdapter(makeConnArgs(), 0)
	if err != nil {
		t.Fatalf("NewTopicAdapter (pub): %v", err)
	}
	defer pub.Close()

	// First poll: nothing there yet; this must pin the start position.
	if _, err := sub.Subscribe(ctx, backends.SubscribeOptions{
		Topic: topic, Timeout: 0.2,
	}); !errors.Is(err, backends.ErrNoMessageAvailable) {
		t.Fatalf("expected no message on first poll, got err=%v", err)
	}

	// Publish while the subscriber is between polls.
	if err := pub.Publish(ctx, backends.PublishOptions{Topic: topic, Message: []byte("in the gap")}); err != nil {
		t.Fatalf("Publish: %v", err)
	}
	time.Sleep(100 * time.Millisecond)

	msg, err := sub.Subscribe(ctx, backends.SubscribeOptions{Topic: topic, Timeout: 5})
	if err != nil {
		t.Fatalf("Subscribe after gap: %v", err)
	}
	if string(msg.Data) != "in the gap" {
		t.Errorf("got %q, want %q", msg.Data, "in the gap")
	}
}

// TestRedis_TopicSubscribeGroup verifies group-based subscription (competing
// consumers) delivers and acknowledges.
func TestRedis_TopicSubscribeGroup(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	topic := "xmc:topic:test.group"

	sub, err := NewTopicAdapter(makeConnArgs(), 0)
	if err != nil {
		t.Fatalf("NewTopicAdapter: %v", err)
	}
	defer sub.Close()

	// Create the group cursor first, then publish.
	if _, err := sub.Subscribe(ctx, backends.SubscribeOptions{
		Topic: topic, GroupID: "grp-1", Timeout: 0.2,
	}); !errors.Is(err, backends.ErrNoMessageAvailable) {
		t.Fatalf("expected no message on group setup poll, got err=%v", err)
	}

	pub, err := NewTopicAdapter(makeConnArgs(), 0)
	if err != nil {
		t.Fatalf("NewTopicAdapter (pub): %v", err)
	}
	defer pub.Close()

	if err := pub.Publish(ctx, backends.PublishOptions{Topic: topic, Message: []byte("group msg")}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	msg, err := sub.Subscribe(ctx, backends.SubscribeOptions{Topic: topic, GroupID: "grp-1", Timeout: 5})
	if err != nil {
		t.Fatalf("Subscribe: %v", err)
	}
	if string(msg.Data) != "group msg" {
		t.Errorf("got %q, want %q", msg.Data, "group msg")
	}
}
