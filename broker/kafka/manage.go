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
		return nil, fmt.Errorf("failed to list consumer groups: %w", err)
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
