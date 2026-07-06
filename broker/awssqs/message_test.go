//go:build aws

package awssqs

import (
	"testing"

	sqstypes "github.com/aws/aws-sdk-go-v2/service/sqs/types"

	"github.com/makibytes/xmc/broker/backends"
)

func TestSQSToBackendMessage_BackfillMessageID(t *testing.T) {
	msg := sqstypes.Message{
		Body:      strPtr("hello"),
		MessageId: strPtr("sqs-assigned-id"),
	}

	result := sqsToBackendMessage(msg)
	if result.MessageID != "sqs-assigned-id" {
		t.Errorf("MessageID: got %q, want back-filled sqs-assigned-id", result.MessageID)
	}
}

func TestSQSToBackendMessage_SenderIDWins(t *testing.T) {
	msg := sqstypes.Message{
		Body:      strPtr("hello"),
		MessageId: strPtr("sqs-assigned-id"),
		MessageAttributes: map[string]sqstypes.MessageAttributeValue{
			backends.PropMessageID: {DataType: strPtr("String"), StringValue: strPtr("explicit-id")},
		},
	}

	result := sqsToBackendMessage(msg)
	if result.MessageID != "explicit-id" {
		t.Errorf("MessageID: got %q, want sender-set explicit-id", result.MessageID)
	}
}

func TestSQSToBackendMessage_GroupIDToKey(t *testing.T) {
	msg := sqstypes.Message{
		Body: strPtr("hello"),
		Attributes: map[string]string{
			string(sqstypes.MessageSystemAttributeNameMessageGroupId): "checkout",
		},
	}

	result := sqsToBackendMessage(msg)
	if result.Key != "checkout" {
		t.Errorf("Key: got %q, want checkout (from MessageGroupId)", result.Key)
	}
}
