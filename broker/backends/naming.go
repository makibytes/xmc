package backends

import (
	"crypto/rand"
	"encoding/hex"
	"fmt"
)

// RandomSuffix returns a random 12-character hex string suitable for ephemeral
// resource naming (subscriptions, consumer groups, etc.).
func RandomSuffix() string {
	var b [6]byte
	rand.Read(b[:]) //nolint:errcheck
	return hex.EncodeToString(b[:])
}

// SubscriptionName computes a subscription/consumer-group name from subscribe
// options, following the cross-broker convention:
//   - GroupID set: use it verbatim (shared/competing consumers).
//   - Durable set: "xmc-durable-{topic}" (stable across restarts).
//   - Otherwise: "xmc-sub-{random}" (ephemeral, cleaned up on Close).
//
// The second return value is true when the subscription is ephemeral and should
// be deleted on adapter Close.
func SubscriptionName(opts SubscribeOptions) (name string, ephemeral bool) {
	if opts.GroupID != "" {
		return opts.GroupID, false
	}
	if opts.Durable {
		return fmt.Sprintf("xmc-durable-%s", opts.Topic), false
	}
	return fmt.Sprintf("xmc-sub-%s", RandomSuffix()), true
}

// ScopedSubscriptionName is SubscriptionName for brokers whose subscription
// namespace is global rather than per-topic (AWS SQS queue names, Google
// Pub/Sub subscription IDs): the group form is additionally scoped by the
// topic, so the same group on two topics yields two subscription objects
// instead of silently mixing both topics' messages into one. sep joins group
// and topic and must be legal in the broker's naming rules.
func ScopedSubscriptionName(opts SubscribeOptions, sep string) (name string, ephemeral bool) {
	if opts.GroupID != "" {
		return opts.GroupID + sep + opts.Topic, false
	}
	return SubscriptionName(opts)
}
