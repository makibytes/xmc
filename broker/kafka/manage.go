//go:build kafka

package kafka

import (
	"context"
	"fmt"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	kafkago "github.com/segmentio/kafka-go"
)

// TopicInfo holds topic information
type TopicInfo struct {
	Name           string
	PartitionCount int
}

// CreateTopic creates a topic on the Kafka cluster.
func CreateTopic(connArgs ConnArguments, topic string, partitions, replicationFactor int, configs map[string]string) error {
	brokers, tlsConfig, err := parseKafkaURL(connArgs.Server, connArgs.TLS)
	if err != nil {
		return err
	}

	dialer := buildDialer(connArgs, tlsConfig)
	if dialer == nil {
		dialer = &kafkago.Dialer{}
	}

	log.Verbose("connecting to %s to create topic %s...", brokers[0], topic)
	conn, err := dialer.DialContext(context.Background(), "tcp", brokers[0])
	if err != nil {
		return fmt.Errorf("failed to connect to Kafka: %w", err)
	}
	defer conn.Close()

	topicConfig := kafkago.TopicConfig{
		Topic:             topic,
		NumPartitions:     partitions,
		ReplicationFactor: replicationFactor,
	}
	for k, v := range configs {
		topicConfig.ConfigEntries = append(topicConfig.ConfigEntries, kafkago.ConfigEntry{
			ConfigName:  k,
			ConfigValue: v,
		})
	}

	return conn.CreateTopics(topicConfig)
}

// DeleteTopic deletes a topic from the Kafka cluster.
func DeleteTopic(connArgs ConnArguments, topic string) error {
	brokers, tlsConfig, err := parseKafkaURL(connArgs.Server, connArgs.TLS)
	if err != nil {
		return err
	}

	dialer := buildDialer(connArgs, tlsConfig)
	if dialer == nil {
		dialer = &kafkago.Dialer{}
	}

	log.Verbose("connecting to %s to delete topic %s...", brokers[0], topic)
	conn, err := dialer.DialContext(context.Background(), "tcp", brokers[0])
	if err != nil {
		return fmt.Errorf("failed to connect to Kafka: %w", err)
	}
	defer conn.Close()

	return conn.DeleteTopics(topic)
}

// newAdminClient builds a kafka-go admin Client for group/config-level admin
// calls (ListGroups, DeleteGroups, CreatePartitions, IncrementalAlterConfigs),
// mirroring ListConsumerGroups's existing transport/SASL setup.
func newAdminClient(connArgs ConnArguments) (*kafkago.Client, []string, error) {
	brokers, tlsConfig, err := parseKafkaURL(connArgs.Server, connArgs.TLS)
	if err != nil {
		return nil, nil, err
	}

	transport := &kafkago.Transport{TLS: tlsConfig}
	if sasl := getSASLMechanism(connArgs.User, connArgs.Password); sasl != nil {
		transport.SASL = sasl
	}

	client := &kafkago.Client{
		Addr:      kafkago.TCP(brokers...),
		Transport: transport,
	}
	return client, brokers, nil
}

// DeleteConsumerGroup deletes a consumer group from the Kafka cluster. Kafka
// requires the group to have no active members (state "Empty"); kafka-go's
// own Error type already renders a clear message for the two ways this can
// fail ("the group is not empty" / "the group ID does not exist"), so the
// per-group error is returned as-is beyond the usual DNS hint.
func DeleteConsumerGroup(connArgs ConnArguments, group string) error {
	client, brokers, err := newAdminClient(connArgs)
	if err != nil {
		return err
	}

	log.Verbose("deleting consumer group %s on %s...", group, brokers[0])
	resp, err := client.DeleteGroups(context.Background(), &kafkago.DeleteGroupsRequest{
		Addr:     kafkago.TCP(brokers[0]),
		GroupIDs: []string{group},
	})
	if err != nil {
		return hintAdvertisedListeners(fmt.Errorf("failed to delete consumer group: %w", err), brokers)
	}
	if groupErr := resp.Errors[group]; groupErr != nil {
		return fmt.Errorf("delete consumer group %s: %w", group, groupErr)
	}
	return nil
}

// UpdateTopic applies partition-count and/or config changes to an existing
// topic. partitions <= 0 means "leave unchanged" — Kafka partition counts can
// only increase, never decrease. Only the settings actually requested are
// touched, mirroring Artemis's update-queue partial-update behaviour.
func UpdateTopic(connArgs ConnArguments, topic string, partitions int, configs map[string]string) error {
	client, brokers, err := newAdminClient(connArgs)
	if err != nil {
		return err
	}
	ctx := context.Background()

	if partitions > 0 {
		log.Verbose("updating %s to %d partitions on %s...", topic, partitions, brokers[0])
		resp, err := client.CreatePartitions(ctx, &kafkago.CreatePartitionsRequest{
			Addr:   kafkago.TCP(brokers[0]),
			Topics: []kafkago.TopicPartitionsConfig{{Name: topic, Count: int32(partitions)}},
		})
		if err != nil {
			return hintAdvertisedListeners(fmt.Errorf("failed to update partitions for %s: %w", topic, err), brokers)
		}
		if topicErr := resp.Errors[topic]; topicErr != nil {
			return fmt.Errorf("update partitions for %s: %w", topic, topicErr)
		}
	}

	if len(configs) > 0 {
		entries := make([]kafkago.IncrementalAlterConfigsRequestConfig, 0, len(configs))
		for k, v := range configs {
			entries = append(entries, kafkago.IncrementalAlterConfigsRequestConfig{
				Name:            k,
				Value:           v,
				ConfigOperation: kafkago.ConfigOperationSet,
			})
		}

		log.Verbose("updating %s config on %s...", topic, brokers[0])
		resp, err := client.IncrementalAlterConfigs(ctx, &kafkago.IncrementalAlterConfigsRequest{
			Addr: kafkago.TCP(brokers[0]),
			Resources: []kafkago.IncrementalAlterConfigsRequestResource{
				{ResourceType: kafkago.ResourceTypeTopic, ResourceName: topic, Configs: entries},
			},
		})
		if err != nil {
			return hintAdvertisedListeners(fmt.Errorf("failed to update config for %s: %w", topic, err), brokers)
		}
		for _, r := range resp.Resources {
			if r.Error != nil {
				return fmt.Errorf("update config for %s: %w", topic, r.Error)
			}
		}
	}

	return nil
}

// TopicStats reports the current message count for topic, summed across all
// of its partitions' (last - first) watermark offsets. ConsumerCount is left
// at 0: unlike a queue, a Kafka topic has no single "consumer count" — any
// number of independent consumer groups may read it concurrently.
func TopicStats(connArgs ConnArguments, topic string) (*backends.QueueStats, error) {
	brokers, tlsConfig, err := parseKafkaURL(connArgs.Server, connArgs.TLS)
	if err != nil {
		return nil, err
	}

	dialer := buildDialer(connArgs, tlsConfig)
	if dialer == nil {
		dialer = &kafkago.Dialer{}
	}
	ctx := context.Background()

	log.Verbose("connecting to %s for stats on %s...", brokers[0], topic)
	conn, err := dialer.DialContext(ctx, "tcp", brokers[0])
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Kafka: %w", err)
	}
	defer conn.Close()

	parts, err := conn.ReadPartitions(topic)
	if err != nil {
		return nil, fmt.Errorf("failed to read partitions for %s: %w", topic, err)
	}
	if len(parts) == 0 {
		return nil, fmt.Errorf("topic %s not found", topic)
	}

	var total int64
	for _, p := range parts {
		pconn, err := dialer.DialLeader(ctx, "tcp", brokers[0], topic, p.ID)
		if err != nil {
			return nil, fmt.Errorf("failed to dial leader for %s partition %d: %w", topic, p.ID, err)
		}
		first, last, err := pconn.ReadOffsets()
		pconn.Close()
		if err != nil {
			return nil, fmt.Errorf("failed to read offsets for %s partition %d: %w", topic, p.ID, err)
		}
		total += last - first
	}

	return &backends.QueueStats{Name: topic, MessageCount: total}, nil
}

// ListConsumerGroups lists all consumer groups on the Kafka cluster.
func ListConsumerGroups(connArgs ConnArguments) ([]backends.ObjectNode, error) {
	brokers, tlsConfig, err := parseKafkaURL(connArgs.Server, connArgs.TLS)
	if err != nil {
		return nil, err
	}

	transport := &kafkago.Transport{TLS: tlsConfig}
	sasl := getSASLMechanism(connArgs.User, connArgs.Password)
	if sasl != nil {
		transport.SASL = sasl
	}

	client := &kafkago.Client{
		Addr:      kafkago.TCP(brokers...),
		Transport: transport,
	}

	log.Verbose("listing consumer groups on %s...", brokers[0])
	resp, err := client.ListGroups(context.Background(), &kafkago.ListGroupsRequest{
		Addr: kafkago.TCP(brokers[0]),
	})
	if err != nil {
		return nil, hintAdvertisedListeners(fmt.Errorf("failed to list consumer groups: %w", err), brokers)
	}
	if resp.Error != nil {
		return nil, fmt.Errorf("list consumer groups: %w", resp.Error)
	}

	var out []backends.ObjectNode
	for _, g := range resp.Groups {
		kind := g.ProtocolType
		if kind == "" {
			kind = "simple"
		}
		out = append(out, backends.ObjectNode{
			Name: g.GroupID,
			Kind: kind,
		})
	}
	return out, nil
}

// ListTopics lists all topics on the Kafka cluster
func ListTopics(connArgs ConnArguments) ([]TopicInfo, error) {
	brokers, tlsConfig, err := parseKafkaURL(connArgs.Server, connArgs.TLS)
	if err != nil {
		return nil, err
	}

	dialer := buildDialer(connArgs, tlsConfig)
	if dialer == nil {
		dialer = &kafkago.Dialer{}
	}

	log.Verbose("connecting to %s to list topics...", brokers[0])
	conn, err := dialer.DialContext(context.Background(), "tcp", brokers[0])
	if err != nil {
		return nil, fmt.Errorf("failed to connect to Kafka: %w", err)
	}
	defer conn.Close()

	partitions, err := conn.ReadPartitions()
	if err != nil {
		return nil, fmt.Errorf("failed to read partitions: %w", err)
	}

	// Deduplicate topics and count partitions
	topicMap := make(map[string]int)
	for _, p := range partitions {
		topicMap[p.Topic]++
	}

	var topics []TopicInfo
	for name, count := range topicMap {
		topics = append(topics, TopicInfo{
			Name:           name,
			PartitionCount: count,
		})
	}

	return topics, nil
}
