//go:build mqtt

package mqtt

import (
	"context"
	"fmt"
	"strings"
	"sync"

	"github.com/eclipse/paho.golang/autopaho"
	"github.com/eclipse/paho.golang/paho"

	"github.com/makibytes/xmc/log"
)

// subCache5 keeps MQTT 5 subscriptions open across successive
// Receive/Subscribe calls on the same adapter, for the same reason as the v3
// subscriptionCacheV3: subscribing per call would drop every message that
// arrives between calls, breaking streaming reads (-n 0, --for).
//
// paho.golang has no per-subscription router, so each cached subscription
// registers a publish callback that filter-matches incoming topics itself.
type subCache5 struct {
	mu   sync.Mutex
	subs map[string]chan *paho.Publish
}

// channelFor returns the message channel for (topic, qos), subscribing on
// first use. Messages beyond the buffer are dropped with a verbose note —
// MQTT QoS 0/1 semantics allow this, and blocking would stall the client.
func (c *subCache5) channelFor(ctx context.Context, cm *autopaho.ConnectionManager, topic string, qos byte) (<-chan *paho.Publish, error) {
	key := fmt.Sprintf("%s|%d", topic, qos)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subs == nil {
		c.subs = make(map[string]chan *paho.Publish)
	}
	if ch, ok := c.subs[key]; ok {
		return ch, nil
	}

	// Messages arrive on the actual topic, not the subscription filter:
	// strip the shared-subscription prefix before matching.
	filter := topic
	if strings.HasPrefix(filter, "$share/") {
		if parts := strings.SplitN(filter, "/", 3); len(parts) == 3 {
			filter = parts[2]
		}
	}

	ch := make(chan *paho.Publish, subscriptionBuffer)
	cm.AddOnPublishReceived(func(pr autopaho.PublishReceived) (bool, error) {
		if !topicMatchesFilter(filter, pr.Packet.Topic) {
			return false, nil
		}
		select {
		case ch <- pr.Packet:
		default:
			log.Verbose("⚠️  subscription buffer full, dropping message from %s", pr.Packet.Topic)
		}
		return true, nil
	})

	subCtx, cancel := context.WithTimeout(ctx, tokenTimeout)
	defer cancel()
	if _, err := cm.Subscribe(subCtx, &paho.Subscribe{
		Subscriptions: []paho.SubscribeOptions{{Topic: topic, QoS: qos}},
	}); err != nil {
		return nil, fmt.Errorf("MQTT subscribe to %q: %w", topic, err)
	}

	c.subs[key] = ch
	return ch, nil
}

// topicMatchesFilter reports whether a published topic matches an MQTT topic
// filter ('+' matches one level, '#' matches the rest including the parent
// level, per the MQTT specification).
func topicMatchesFilter(filter, topic string) bool {
	if filter == topic {
		return true
	}
	fl := strings.Split(filter, "/")
	tl := strings.Split(topic, "/")
	for i, f := range fl {
		switch f {
		case "#":
			return true
		case "+":
			if i >= len(tl) {
				return false
			}
		default:
			if i >= len(tl) || tl[i] != f {
				return false
			}
		}
	}
	return len(fl) == len(tl)
}
