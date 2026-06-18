//go:build nats

package nats

import (
	"fmt"
	"strings"

	natsclient "github.com/nats-io/nats.go"
)

// StreamInfo holds basic information about a JetStream stream.
type StreamInfo struct {
	Name         string
	MessageCount uint64
}

// ListStreams returns the list of JetStream streams and their message counts.
func ListStreams(connArgs ConnArguments) ([]StreamInfo, error) {
	nc, js, err := ConnectWithJetStream(connArgs)
	if err != nil {
		return nil, err
	}
	defer nc.Close()

	var streams []StreamInfo
	for info := range js.StreamsInfo() {
		streams = append(streams, StreamInfo{
			Name:         info.Config.Name,
			MessageCount: info.State.Msgs,
		})
	}

	return streams, nil
}

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
