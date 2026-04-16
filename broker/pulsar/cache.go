//go:build pulsar

package pulsar

import (
	"fmt"
	"sync"

	pulsar "github.com/apache/pulsar-client-go/pulsar"
)

type consumerKey struct {
	topic string
	sub   string
}

// clientCache holds a Pulsar client plus caches of producers/consumers so that
// repeated Send/Receive calls on the same topic reuse the same resources.
type clientCache struct {
	client    pulsar.Client
	mu        sync.Mutex
	producers map[string]pulsar.Producer
	consumers map[consumerKey]pulsar.Consumer
}

func newClientCache(client pulsar.Client) *clientCache {
	return &clientCache{
		client:    client,
		producers: make(map[string]pulsar.Producer),
		consumers: make(map[consumerKey]pulsar.Consumer),
	}
}

func (c *clientCache) getProducer(topic string) (pulsar.Producer, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if p, ok := c.producers[topic]; ok {
		return p, nil
	}
	p, err := c.client.CreateProducer(pulsar.ProducerOptions{Topic: topic})
	if err != nil {
		return nil, fmt.Errorf("creating producer for %s: %w", topic, err)
	}
	c.producers[topic] = p
	return p, nil
}

func (c *clientCache) getConsumer(opts pulsar.ConsumerOptions) (pulsar.Consumer, error) {
	key := consumerKey{topic: opts.Topic, sub: opts.SubscriptionName}
	c.mu.Lock()
	defer c.mu.Unlock()
	if cons, ok := c.consumers[key]; ok {
		return cons, nil
	}
	cons, err := c.client.Subscribe(opts)
	if err != nil {
		return nil, fmt.Errorf("subscribing to %s: %w", opts.Topic, err)
	}
	c.consumers[key] = cons
	return cons, nil
}

func (c *clientCache) close() {
	c.mu.Lock()
	for _, p := range c.producers {
		p.Close()
	}
	for _, cons := range c.consumers {
		cons.Close()
	}
	c.producers = nil
	c.consumers = nil
	c.mu.Unlock()
	if c.client != nil {
		c.client.Close()
	}
}
