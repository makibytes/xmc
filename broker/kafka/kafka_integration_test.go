//go:build kafka && integration

package kafka

import (
	"context"
	"fmt"
	"os"
	"regexp"
	"strings"
	"testing"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	integration "github.com/makibytes/xmc/test/integration"
	kafkago "github.com/segmentio/kafka-go"
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
		// Sender set no message ID → back-filled with the record coordinate.
		if want := regexp.MustCompile("^" + topic + `:\d+:\d+$`); !want.MatchString(msg.MessageID) {
			t.Errorf("MessageID: got %q, want back-filled %q", msg.MessageID, want)
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
			Topic:     topic,
			GroupID:   "test-group-props",
			Timeout:   25,
			Verbosity: backends.VerbosityNormal,
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
			Topic:     topic,
			GroupID:   "test-group-key",
			Timeout:   25,
			Verbosity: backends.VerbosityVerbose,
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

// TestKafka_DeleteConsumerGroup verifies that a materialized consumer group
// can be deleted and disappears from ListConsumerGroups, and that deleting a
// group that never existed returns a clear "does not exist" error.
func TestKafka_DeleteConsumerGroup(t *testing.T) {
	t.Parallel()
	topic := "test-delete-consumer-group"
	group := "test-group-to-delete"

	adapter, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter: %v", err)
	}

	// Materialize the group the same way TestKafka_TopicSubscribe_ConsumerGroup
	// does: a real publish/subscribe round-trip.
	errCh := make(chan error, 1)
	doneCh := make(chan struct{})
	go func() {
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		_, err := adapter.Subscribe(ctx, backends.SubscribeOptions{
			Topic:   topic,
			GroupID: group,
			Timeout: 25,
		})
		if err != nil {
			errCh <- err
			return
		}
		close(doneCh)
	}()

	time.Sleep(500 * time.Millisecond)
	if err := adapter.Publish(context.Background(), backends.PublishOptions{
		Topic:   topic,
		Message: []byte("materialize group"),
	}); err != nil {
		t.Fatalf("Publish: %v", err)
	}

	select {
	case err := <-errCh:
		t.Fatalf("Subscribe error: %v", err)
	case <-doneCh:
	case <-time.After(30 * time.Second):
		t.Fatal("timed out waiting for message")
	}

	// While our own reader is still a member, Kafka must refuse to delete the
	// group (requires state "Empty") with a clear message.
	if err := DeleteConsumerGroup(makeConnArgs(), group); err == nil {
		t.Fatal("expected error deleting a group with an active member, got nil")
	} else if !strings.Contains(err.Error(), "not empty") {
		t.Errorf("expected error to mention \"not empty\", got: %v", err)
	}

	// Close the adapter (and thus its cached reader) so the member leaves the
	// group, then it should become deletable.
	if err := adapter.Close(); err != nil {
		t.Fatalf("adapter.Close: %v", err)
	}

	// The coordinator may take a moment to register the member's departure
	// after a graceful leave; retry briefly rather than racing it.
	for i := 0; i < 10; i++ {
		err = DeleteConsumerGroup(makeConnArgs(), group)
		if err == nil {
			break
		}
		time.Sleep(300 * time.Millisecond)
	}
	if err != nil {
		t.Fatalf("DeleteConsumerGroup: %v", err)
	}

	groups, err := ListConsumerGroups(makeConnArgs())
	if err != nil {
		t.Fatalf("ListConsumerGroups: %v", err)
	}
	for _, g := range groups {
		if g.Name == group {
			t.Fatalf("group %s still present after delete: %v", group, groups)
		}
	}

	err = DeleteConsumerGroup(makeConnArgs(), "no-such-group-ever-existed")
	if err == nil {
		t.Fatal("expected error deleting a nonexistent group, got nil")
	}
	if !strings.Contains(err.Error(), "does not exist") {
		t.Errorf("expected error to mention \"does not exist\", got: %v", err)
	}
}

// TestKafka_UpdateTopic_Partitions verifies that UpdateTopic can increase a
// topic's partition count.
func TestKafka_UpdateTopic_Partitions(t *testing.T) {
	t.Parallel()
	topic := "test-update-topic-partitions"

	if err := CreateTopic(makeConnArgs(), topic, 1, 1, nil); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if err := UpdateTopic(makeConnArgs(), topic, 3, nil); err != nil {
		t.Fatalf("UpdateTopic: %v", err)
	}

	// Partition metadata can take a moment to propagate; retry briefly.
	var topics []TopicInfo
	var err error
	for i := 0; i < 5; i++ {
		topics, err = ListTopics(makeConnArgs())
		if err != nil {
			t.Fatalf("ListTopics: %v", err)
		}
		for _, tp := range topics {
			if tp.Name == topic && tp.PartitionCount == 3 {
				return
			}
		}
		time.Sleep(500 * time.Millisecond)
	}
	t.Fatalf("expected %s to have 3 partitions after update, got: %v", topic, topics)
}

// TestKafka_UpdateTopic_Config verifies that UpdateTopic can set a topic-level
// config and that it's visible via DescribeConfigs afterward.
func TestKafka_UpdateTopic_Config(t *testing.T) {
	t.Parallel()
	topic := "test-update-topic-config"

	if err := CreateTopic(makeConnArgs(), topic, 1, 1, nil); err != nil {
		t.Fatalf("CreateTopic: %v", err)
	}

	if err := UpdateTopic(makeConnArgs(), topic, 0, map[string]string{"retention.ms": "3600000"}); err != nil {
		t.Fatalf("UpdateTopic: %v", err)
	}

	client, brokers, err := newAdminClient(makeConnArgs())
	if err != nil {
		t.Fatalf("newAdminClient: %v", err)
	}
	resp, err := client.DescribeConfigs(context.Background(), &kafkago.DescribeConfigsRequest{
		Addr: kafkago.TCP(brokers[0]),
		Resources: []kafkago.DescribeConfigRequestResource{
			{ResourceType: kafkago.ResourceTypeTopic, ResourceName: topic, ConfigNames: []string{"retention.ms"}},
		},
	})
	if err != nil {
		t.Fatalf("DescribeConfigs: %v", err)
	}
	if len(resp.Resources) != 1 {
		t.Fatalf("expected 1 resource in DescribeConfigs response, got %d", len(resp.Resources))
	}
	found := false
	for _, entry := range resp.Resources[0].ConfigEntries {
		if entry.ConfigName == "retention.ms" {
			found = true
			if entry.ConfigValue != "3600000" {
				t.Errorf("expected retention.ms=3600000, got %s", entry.ConfigValue)
			}
		}
	}
	if !found {
		t.Fatalf("retention.ms not found in config entries: %v", resp.Resources[0].ConfigEntries)
	}
}

// TestKafka_TopicStats verifies that TopicStats reports the correct message
// count after publishing a known number of messages.
func TestKafka_TopicStats(t *testing.T) {
	t.Parallel()
	topic := "test-topic-stats"
	const messageCount = 5

	adapter, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter: %v", err)
	}
	defer adapter.Close()

	for i := 0; i < messageCount; i++ {
		if err := adapter.Publish(context.Background(), backends.PublishOptions{
			Topic:   topic,
			Message: []byte(fmt.Sprintf("stats message %d", i)),
		}); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}

	stats, err := TopicStats(makeConnArgs(), topic)
	if err != nil {
		t.Fatalf("TopicStats: %v", err)
	}
	if stats.Name != topic {
		t.Errorf("expected Name=%s, got %s", topic, stats.Name)
	}
	if stats.MessageCount != messageCount {
		t.Errorf("expected MessageCount=%d, got %d", messageCount, stats.MessageCount)
	}
}

// TestKafka_PartitionOffsetRead verifies single-partition reads with an
// explicit --offset: a partition reader must honour a numeric offset and the
// earliest sentinel (kafka-go only supports these via Reader.SetOffset, not
// ReaderConfig.StartOffset).
func TestKafka_PartitionOffsetRead(t *testing.T) {
	t.Parallel()
	topic := "test-partition-offset"
	const messageCount = 3

	publisher, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter: %v", err)
	}
	defer publisher.Close()

	ctx := context.Background()
	for i := 0; i < messageCount; i++ {
		if err := publisher.Publish(ctx, backends.PublishOptions{
			Topic:   topic,
			Message: []byte(fmt.Sprintf("offset message %d", i)),
		}); err != nil {
			t.Fatalf("Publish %d: %v", i, err)
		}
	}

	// Read from offset 1 on partition 0 (single-partition topic by default).
	reader, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter (reader): %v", err)
	}
	defer reader.Close()

	msg, err := reader.Subscribe(ctx, backends.SubscribeOptions{
		Topic:   topic,
		Timeout: 15,
		Extra:   map[string]string{"partition": "0", "offset": "1"},
	})
	if err != nil {
		t.Fatalf("Subscribe (offset 1): %v", err)
	}
	if string(msg.Data) != "offset message 1" {
		t.Errorf("expected message at offset 1, got %q", msg.Data)
	}

	// earliest must position at offset 0.
	earliestReader, err := NewTopicAdapter(makeConnArgs())
	if err != nil {
		t.Fatalf("NewTopicAdapter (earliest): %v", err)
	}
	defer earliestReader.Close()

	msg, err = earliestReader.Subscribe(ctx, backends.SubscribeOptions{
		Topic:   topic,
		Timeout: 15,
		Extra:   map[string]string{"partition": "0", "offset": "earliest"},
	})
	if err != nil {
		t.Fatalf("Subscribe (earliest): %v", err)
	}
	if string(msg.Data) != "offset message 0" {
		t.Errorf("expected message at offset 0, got %q", msg.Data)
	}

	// --offset without --partition must be rejected.
	if _, err := earliestReader.Subscribe(ctx, backends.SubscribeOptions{
		Topic:   topic,
		Timeout: 2,
		Extra:   map[string]string{"offset": "earliest"},
	}); err == nil {
		t.Error("expected error for --offset without --partition")
	}
}
