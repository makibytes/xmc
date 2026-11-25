//go:build google

package gcppubsub

import (
	"context"
	"errors"
	"sync"

	"cloud.google.com/go/pubsub"

	"github.com/makibytes/xmc/broker/backends"
)

// receiveOne receives at most one message from sub. The message is acked when
// acknowledge is set and nacked otherwise: Pub/Sub has no non-destructive peek,
// so a nack leaves it for redelivery. It returns backends.ErrNoMessageAvailable
// when nothing arrives within the timeout.
func receiveOne(ctx context.Context, sub *pubsub.Subscription, timeout float32, wait, acknowledge bool) (*backends.Message, error) {
	receiveCtx, cancel := context.WithTimeout(ctx, backends.TimeoutDuration(timeout, wait))
	defer cancel()

	var (
		msg *backends.Message
		mu  sync.Mutex
	)

	err := sub.Receive(receiveCtx, func(_ context.Context, m *pubsub.Message) {
		mu.Lock()
		defer mu.Unlock()
		if msg != nil {
			m.Nack()
			return
		}
		if acknowledge {
			m.Ack()
		} else {
			m.Nack()
		}
		msg = pubsubToBackendMessage(m)
		cancel()
	})
	mu.Lock()
	defer mu.Unlock()
	if msg != nil {
		return msg, nil
	}
	if err != nil && !errors.Is(err, context.Canceled) {
		return nil, err
	}
	return nil, backends.ErrNoMessageAvailable
}
