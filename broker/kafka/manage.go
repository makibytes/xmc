//go:build kafka

package kafka

import (
	"context"
	"fmt"

	"github.com/makibytes/xmc/log"
	kafkago "github.com/segmentio/kafka-go"
)

// TopicInfo holds topic information
type TopicInfo struct {
	Name           string
	PartitionCount int
}

// ListTopics lists all topics on the Kafka cluster
func ListTopics(connArgs ConnArguments) ([]TopicInfo, error) {
	brokers, tlsConfig, err := parseKafkaURL(connArgs.Server, connArgs.TLS)
	if err != nil {
		return nil, err
	}

	dialer := &kafkago.Dialer{}
	if tlsConfig != nil {
		dialer.TLS = tlsConfig
	}
	if sasl := getSASLMechanism(connArgs.User, connArgs.Password); sasl != nil {
		dialer.SASLMechanism = sasl
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
