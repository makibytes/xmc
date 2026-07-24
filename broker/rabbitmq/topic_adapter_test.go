//go:build rabbitmq

package rabbitmq

import (
	"testing"

	"github.com/makibytes/xmc/broker/backends"
)

func TestSubscriptionCacheKeyIncludesDurability(t *testing.T) {
	base := backends.SubscribeOptions{Topic: "orders", GroupID: "g1", Durable: false}
	durable := base
	durable.Durable = true

	k1 := subscriptionCacheKey("amq.topic", "orders", base)
	k2 := subscriptionCacheKey("amq.topic", "orders", durable)
	if k1 == k2 {
		t.Fatalf("cache key should differ by durability: %q", k1)
	}
}
