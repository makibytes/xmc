//go:build google

package gcppubsub

import (
	"testing"

	"cloud.google.com/go/pubsub"

	"github.com/makibytes/xmc/broker/backends"
)

func TestPubsubToBackendMessage_BackfillMessageID(t *testing.T) {
	result := pubsubToBackendMessage(&pubsub.Message{
		ID:   "server-assigned-id",
		Data: []byte("hello"),
	})
	if result.MessageID != "server-assigned-id" {
		t.Errorf("MessageID: got %q, want back-filled server-assigned-id", result.MessageID)
	}
}

func TestPubsubToBackendMessage_SenderIDWins(t *testing.T) {
	result := pubsubToBackendMessage(&pubsub.Message{
		ID:         "server-assigned-id",
		Data:       []byte("hello"),
		Attributes: map[string]string{backends.PropMessageID: "explicit-id"},
	})
	if result.MessageID != "explicit-id" {
		t.Errorf("MessageID: got %q, want sender-set explicit-id", result.MessageID)
	}
}

func TestPubsubToBackendMessage_OrderingKeyToKey(t *testing.T) {
	result := pubsubToBackendMessage(&pubsub.Message{
		ID:          "id",
		Data:        []byte("hello"),
		OrderingKey: "checkout",
	})
	if result.Key != "checkout" {
		t.Errorf("Key: got %q, want checkout (from OrderingKey)", result.Key)
	}
}
