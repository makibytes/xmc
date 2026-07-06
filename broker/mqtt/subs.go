//go:build mqtt

package mqtt

import (
	"fmt"
	"sync"
	"time"

	pahomqtt "github.com/eclipse/paho.mqtt.golang"
	"github.com/makibytes/xmc/log"
)

// tokenTimeout bounds every paho token wait so a broker that stops responding
// cannot hang a command forever.
const tokenTimeout = 30 * time.Second

// subscriptionCache keeps MQTT subscriptions open across successive
// Receive/Subscribe calls on the same adapter. Subscribing per call would drop
// every message that arrives between the unsubscribe and the next subscribe —
// with CleanSession the broker forgets the subscription immediately — which
// breaks streaming reads (-n 0, --for). Buffered channels absorb bursts while
// the caller processes the previous message.
type subscriptionCache struct {
	mu   sync.Mutex
	subs map[string]chan pahomqtt.Message
}

const subscriptionBuffer = 256

// channelFor returns the message channel for (topic, qos), subscribing on
// first use. Messages beyond the buffer are dropped with a verbose note —
// MQTT QoS 0/1 semantics allow this, and blocking would stall paho's router.
func (c *subscriptionCache) channelFor(client pahomqtt.Client, topic string, qos byte) (<-chan pahomqtt.Message, error) {
	key := fmt.Sprintf("%s|%d", topic, qos)

	c.mu.Lock()
	defer c.mu.Unlock()
	if c.subs == nil {
		c.subs = make(map[string]chan pahomqtt.Message)
	}
	if ch, ok := c.subs[key]; ok {
		return ch, nil
	}

	ch := make(chan pahomqtt.Message, subscriptionBuffer)
	token := client.Subscribe(topic, qos, func(_ pahomqtt.Client, msg pahomqtt.Message) {
		select {
		case ch <- msg:
		default:
			log.Verbose("⚠️  subscription buffer full, dropping message from %s", msg.Topic())
		}
	})
	if !token.WaitTimeout(tokenTimeout) {
		return nil, fmt.Errorf("MQTT subscribe to %q timed out", topic)
	}
	if err := token.Error(); err != nil {
		return nil, fmt.Errorf("MQTT subscribe to %q: %w", topic, err)
	}

	c.subs[key] = ch
	return ch, nil
}
