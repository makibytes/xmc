//go:build nats

package nats

import (
	"fmt"
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

// FormatStreamList formats stream information for display.
func FormatStreamList(streams []StreamInfo) string {
	var result string
	for _, s := range streams {
		result += fmt.Sprintf("%-40s  messages=%d\n", s.Name, s.MessageCount)
	}
	return result
}
