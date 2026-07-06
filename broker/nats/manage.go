//go:build nats

package nats

import (
	"fmt"
	"strings"

	"github.com/makibytes/xmc/broker/backends"
	natsclient "github.com/nats-io/nats.go"
)

// CreateStream creates a JetStream WorkQueue stream for a queue.
func CreateStream(connArgs ConnArguments, queue string, retention string, maxMsgs int64, subjects []string) error {
	nc, js, err := ConnectWithJetStream(connArgs)
	if err != nil {
		return err
	}
	defer nc.Close()

	name := streamName(queue)

	retentionPolicy := natsclient.WorkQueuePolicy
	switch strings.ToLower(retention) {
	case "workqueue", "":
		retentionPolicy = natsclient.WorkQueuePolicy
	case "limits":
		retentionPolicy = natsclient.LimitsPolicy
	case "interest":
		retentionPolicy = natsclient.InterestPolicy
	default:
		return fmt.Errorf("unknown retention policy %q (valid: workqueue, limits, interest)", retention)
	}

	subjectList := subjects
	if len(subjectList) == 0 {
		subjectList = []string{queueSubject(queue)}
	}

	cfg := &natsclient.StreamConfig{
		Name:      name,
		Subjects:  subjectList,
		Retention: retentionPolicy,
	}
	if maxMsgs > 0 {
		cfg.MaxMsgs = maxMsgs
	}

	_, err = js.AddStream(cfg)
	if err != nil {
		return fmt.Errorf("creating stream %s: %w", name, err)
	}
	return nil
}

// ListStreamsWithConsumers returns streams with their consumers as children.
func ListStreamsWithConsumers(connArgs ConnArguments) ([]backends.ObjectNode, error) {
	nc, js, err := ConnectWithJetStream(connArgs)
	if err != nil {
		return nil, err
	}
	defer nc.Close()

	var nodes []backends.ObjectNode
	for info := range js.StreamsInfo() {
		node := backends.ObjectNode{
			Name: info.Config.Name,
			Kind: retentionLabel(info.Config.Retention),
			Metrics: []backends.Metric{
				{Label: "msgs", Value: int64(info.State.Msgs)},
			},
		}
		// List consumers for this stream.
		for ci := range js.ConsumersInfo(info.Config.Name) {
			child := backends.ObjectNode{
				Name: ci.Name,
				Kind: "consumer",
			}
			if ci.NumPending > 0 {
				child.Metrics = []backends.Metric{{Label: "pending", Value: int64(ci.NumPending)}}
			}
			node.Children = append(node.Children, child)
		}
		nodes = append(nodes, node)
	}

	return nodes, nil
}

// PurgeStream removes all messages from the JetStream stream for a queue and
// returns how many were purged.
func PurgeStream(connArgs ConnArguments, queue string) (int64, error) {
	nc, js, err := ConnectWithJetStream(connArgs)
	if err != nil {
		return 0, err
	}
	defer nc.Close()

	name := streamName(queue)
	info, err := js.StreamInfo(name)
	if err != nil {
		return 0, fmt.Errorf("looking up stream %s: %w", name, err)
	}
	count := int64(info.State.Msgs)
	if err := js.PurgeStream(name); err != nil {
		return 0, fmt.Errorf("purging stream %s: %w", name, err)
	}
	return count, nil
}

// GetStreamStats returns queue statistics for the JetStream stream backing a
// queue (message count, consumer count, lifetime enqueued).
func GetStreamStats(connArgs ConnArguments, queue string) (*backends.QueueStats, error) {
	nc, js, err := ConnectWithJetStream(connArgs)
	if err != nil {
		return nil, err
	}
	defer nc.Close()

	name := streamName(queue)
	info, err := js.StreamInfo(name)
	if err != nil {
		return nil, fmt.Errorf("looking up stream %s: %w", name, err)
	}
	return &backends.QueueStats{
		Name:          info.Config.Name,
		MessageCount:  int64(info.State.Msgs),
		ConsumerCount: info.State.Consumers,
		// LastSeq counts every message ever appended to the stream, so it
		// serves as the lifetime enqueue counter even after purges.
		EnqueueCount: int64(info.State.LastSeq),
	}, nil
}

// DeleteStream deletes the JetStream stream for a queue.
func DeleteStream(connArgs ConnArguments, queue string) error {
	nc, js, err := ConnectWithJetStream(connArgs)
	if err != nil {
		return err
	}
	defer nc.Close()

	name := streamName(queue)
	if err := js.DeleteStream(name); err != nil {
		return fmt.Errorf("deleting stream %s: %w", name, err)
	}
	return nil
}

// retentionLabel maps a JetStream retention policy to a short human-readable label.
func retentionLabel(r natsclient.RetentionPolicy) string {
	switch r {
	case natsclient.LimitsPolicy:
		return "limits"
	case natsclient.InterestPolicy:
		return "interest"
	case natsclient.WorkQueuePolicy:
		return "workq"
	default:
		return ""
	}
}
