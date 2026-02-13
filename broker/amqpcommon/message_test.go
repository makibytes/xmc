package amqpcommon

import (
	"testing"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/amc/log"
)

func TestConvertAMQPToBackendMessage_BasicData(t *testing.T) {
	msg := amqp.NewMessage([]byte("test data"))

	result := ConvertAMQPToBackendMessage(msg)

	if string(result.Data) != "test data" {
		t.Errorf("Data = %q, want %q", result.Data, "test data")
	}
}

func TestConvertAMQPToBackendMessage_WithProperties(t *testing.T) {
	msg := amqp.NewMessage([]byte("test"))

	replyTo := "reply-queue"
	contentType := "application/json"

	// MessageID and CorrelationID are `any` in AMQP - they can be string, uint64, UUID, etc.
	// The AMQP library stores them as-is, not as *string.
	msg.Properties = &amqp.MessageProperties{
		MessageID:     "msg-123",
		CorrelationID: "corr-456",
		ReplyTo:       &replyTo,
		ContentType:   &contentType,
	}

	result := ConvertAMQPToBackendMessage(msg)

	if result.MessageID != "msg-123" {
		t.Errorf("MessageID = %q, want %q", result.MessageID, "msg-123")
	}
	if result.CorrelationID != "corr-456" {
		t.Errorf("CorrelationID = %q, want %q", result.CorrelationID, "corr-456")
	}
	if result.ReplyTo != "reply-queue" {
		t.Errorf("ReplyTo = %q, want %q", result.ReplyTo, "reply-queue")
	}
	if result.ContentType != "application/json" {
		t.Errorf("ContentType = %q, want %q", result.ContentType, "application/json")
	}
}

func TestConvertAMQPToBackendMessage_WithHeader(t *testing.T) {
	msg := amqp.NewMessage([]byte("test"))
	msg.Header = &amqp.MessageHeader{
		Durable:  true,
		Priority: 7,
	}

	result := ConvertAMQPToBackendMessage(msg)

	if result.Priority != 7 {
		t.Errorf("Priority = %d, want %d", result.Priority, 7)
	}
	if !result.Persistent {
		t.Error("Persistent = false, want true")
	}
}

func TestConvertAMQPToBackendMessage_ApplicationProperties(t *testing.T) {
	msg := amqp.NewMessage([]byte("test"))
	msg.ApplicationProperties = map[string]any{
		"env":  "production",
		"tier": "premium",
	}

	result := ConvertAMQPToBackendMessage(msg)

	if result.Properties["env"] != "production" {
		t.Errorf("env property = %v, want %q", result.Properties["env"], "production")
	}
	if result.Properties["tier"] != "premium" {
		t.Errorf("tier property = %v, want %q", result.Properties["tier"], "premium")
	}
}

func TestConvertAMQPToBackendMessage_NilProperties(t *testing.T) {
	msg := amqp.NewMessage([]byte("test"))
	// Properties and Header are nil by default

	result := ConvertAMQPToBackendMessage(msg)

	if result.Priority != 0 {
		t.Errorf("Priority = %d, want 0", result.Priority)
	}
	if result.Persistent {
		t.Error("Persistent = true, want false")
	}
}

func TestConvertAMQPToBackendMessage_VerboseMetadata(t *testing.T) {
	origVerbose := log.IsVerbose
	log.IsVerbose = true
	defer func() { log.IsVerbose = origVerbose }()

	msg := amqp.NewMessage([]byte("test"))
	contentType := "text/plain"
	msg.Properties = &amqp.MessageProperties{
		ContentType: &contentType,
	}
	msg.Header = &amqp.MessageHeader{Priority: 5}

	result := ConvertAMQPToBackendMessage(msg)

	if _, ok := result.InternalMetadata["Header"]; !ok {
		t.Error("expected Header in InternalMetadata when verbose")
	}
	if _, ok := result.InternalMetadata["MessageProperties"]; !ok {
		t.Error("expected MessageProperties in InternalMetadata when verbose")
	}
}

func TestConvertAMQPToBackendMessage_NonVerboseNoMetadata(t *testing.T) {
	origVerbose := log.IsVerbose
	log.IsVerbose = false
	defer func() { log.IsVerbose = origVerbose }()

	msg := amqp.NewMessage([]byte("test"))
	contentType := "text/plain"
	msg.Properties = &amqp.MessageProperties{
		ContentType: &contentType,
	}

	result := ConvertAMQPToBackendMessage(msg)

	if len(result.InternalMetadata) != 0 {
		t.Errorf("expected empty InternalMetadata when not verbose, got %d entries", len(result.InternalMetadata))
	}
}
