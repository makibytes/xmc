//go:build azure

package azuresb

import (
	"context"
	"fmt"

	"github.com/Azure/azure-sdk-for-go/sdk/messaging/azservicebus"
)

// senderCache provides a shared map of cached senders keyed by destination
// name. Both QueueAdapter and TopicAdapter embed it to avoid duplicating the
// get-or-create pattern.
type senderCache struct {
	client  *azservicebus.Client
	senders map[string]*azservicebus.Sender
}

func newSenderCache(client *azservicebus.Client) senderCache {
	return senderCache{
		client:  client,
		senders: make(map[string]*azservicebus.Sender),
	}
}

func (c *senderCache) getSender(dest string) (*azservicebus.Sender, error) {
	if s, ok := c.senders[dest]; ok {
		return s, nil
	}
	s, err := c.client.NewSender(dest, nil)
	if err != nil {
		return nil, fmt.Errorf("creating sender for %s: %w", dest, err)
	}
	c.senders[dest] = s
	return s, nil
}

func (c *senderCache) closeSenders(ctx context.Context) {
	for _, s := range c.senders {
		s.Close(ctx) //nolint:errcheck
	}
}
