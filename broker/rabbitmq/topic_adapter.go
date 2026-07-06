//go:build rabbitmq

package rabbitmq

import (
	"context"
	"fmt"
	"net/url"
	"strings"

	"github.com/Azure/go-amqp"
	"github.com/makibytes/xmc/broker/amqpcommon"
	"github.com/makibytes/xmc/broker/backends"
)

// TopicAdapter adapts RabbitMQ to the TopicBackend interface using exchange-based routing
type TopicAdapter struct {
	connArgs   ConnArguments
	connection *amqp.Conn
	session    *amqp.Session
	subQueues  map[string]string // (exchange|key|group) → ensured subscription queue
	ephemeral  []string          // subscription queues to delete on Close
}

// NewTopicAdapter creates a new RabbitMQ topic adapter
func NewTopicAdapter(connArgs ConnArguments) (*TopicAdapter, error) {
	connection, session, err := Connect(connArgs)
	if err != nil {
		return nil, err
	}

	return &TopicAdapter{
		connArgs:   connArgs,
		connection: connection,
		session:    session,
		subQueues:  make(map[string]string),
	}, nil
}

// topicAddress returns the AMQP 1.0 v2 target address for a publish.
// Absolute addresses are passed through. A bare name publishes to the default
// topic exchange with the name as routing key (mirroring ResolveTarget's
// publish default), unless an explicit routing key is given — then the name is
// the exchange: /exchanges/<name>/<key>.
func topicAddress(name, key string) string {
	if strings.HasPrefix(name, "/") {
		return name
	}
	if key != "" {
		return "/exchanges/" + escapeName(name) + "/" + escapeName(key)
	}
	return "/exchanges/amq.topic/" + escapeName(name)
}

// parseExchangeAddress splits an AMQP 1.0 v2 exchange address into its
// exchange name and routing key. ok is false when addr is not an exchange
// address (e.g. /queues/<name>).
func parseExchangeAddress(addr string) (exchange, key string, ok bool) {
	rest, found := strings.CutPrefix(addr, "/exchanges/")
	if !found {
		return "", "", false
	}
	rawExchange, rawKey, _ := strings.Cut(rest, "/")
	exchange, key = rawExchange, rawKey
	if s, err := url.PathUnescape(rawExchange); err == nil {
		exchange = s
	}
	if s, err := url.PathUnescape(rawKey); err == nil {
		key = s
	}
	return exchange, key, true
}

// Publish implements backends.TopicBackend
func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	args := SendArguments{
		Queue:         topicAddress(opts.Topic, opts.Key),
		Message:       opts.Message,
		Properties:    opts.Properties,
		MessageID:     opts.MessageID,
		CorrelationID: opts.CorrelationID,
		ReplyTo:       opts.ReplyTo,
		ContentType:   opts.ContentType,
		Priority:      uint8(opts.Priority),
		Durable:       opts.Persistent,
		TTL:           opts.TTL,
	}

	return SendMessage(ctx, a.session, args)
}

// Subscribe implements backends.TopicBackend.
//
// RabbitMQ 4.x AMQP 1.0 (address v2) only allows /queues/<name> as a receiver
// source — consuming directly from an exchange is not possible. Subscribe
// therefore declares a backing queue via the Management API, binds it to the
// exchange with the topic as binding key, and consumes from that queue. The
// queue name follows the cross-broker subscription convention (group name for
// shared subscriptions, xmc-durable-* for durable ones, random xmc-sub-* for
// ephemeral ones that are deleted on Close).
func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	// Bare topic names go to the default topic exchange, mirroring
	// ResolveTarget's publish-side default.
	source := topicAddress(opts.Topic, "")
	if exchange, key, ok := parseExchangeAddress(source); ok {
		queueName, err := a.ensureSubscriptionQueue(exchange, key, opts)
		if err != nil {
			return nil, err
		}
		source = "/queues/" + escapeName(queueName)
	}

	message, err := ReceiveMessage(ctx, a.session, ReceiveArguments{
		Queue:       source,
		Acknowledge: true,
		Selector:    opts.Selector,
		Timeout:     opts.Timeout,
		Wait:        opts.Wait,
	})
	if err != nil {
		return nil, err
	}
	if message == nil {
		return nil, backends.ErrNoMessageAvailable
	}

	return amqpcommon.ConvertAMQPToBackendMessage(message, opts.Verbosity >= backends.VerbosityVerbose), nil
}

// subscriptionQueueName derives the backing queue name for a subscription on
// (exchange, key), following the cross-broker convention of
// backends.SubscriptionName but scoped by the binding so that the same group
// on different topics uses separate queues. scope is the binding key, or the
// exchange name for key-less (fanout/headers) bindings.
func subscriptionQueueName(exchange, key string, opts backends.SubscribeOptions) (name string, ephemeral bool) {
	scope := key
	if scope == "" {
		scope = exchange
	}
	if opts.GroupID != "" {
		return opts.GroupID + "." + scope, false
	}
	if opts.Durable {
		return "xmc-durable-" + scope, false
	}
	return "xmc-sub-" + backends.RandomSuffix(), true
}

// ensureSubscriptionQueue declares and binds the backing queue for a
// subscription once per adapter; later Subscribe calls for the same binding
// reuse it without further management calls.
func (a *TopicAdapter) ensureSubscriptionQueue(exchange, key string, opts backends.SubscribeOptions) (string, error) {
	cacheKey := exchange + "|" + key + "|" + opts.GroupID
	if q, ok := a.subQueues[cacheKey]; ok {
		return q, nil
	}

	name, ephemeral := subscriptionQueueName(exchange, key, opts)
	mgmt := ManagementArgs{Server: a.connArgs.Server, User: a.connArgs.User, Password: a.connArgs.Password}
	if err := DeclareSubscriptionQueue(mgmt, name, ephemeral); err != nil {
		return "", fmt.Errorf("declaring subscription queue %s: %w", name, err)
	}
	if err := BindQueue(mgmt, name, exchange, key); err != nil {
		return "", fmt.Errorf("binding queue %s to exchange %s: %w", name, exchange, err)
	}

	if ephemeral {
		a.ephemeral = append(a.ephemeral, name)
	}
	a.subQueues[cacheKey] = name
	return name, nil
}

// Close implements backends.TopicBackend
func (a *TopicAdapter) Close() error {
	mgmt := ManagementArgs{Server: a.connArgs.Server, User: a.connArgs.User, Password: a.connArgs.Password}
	for _, q := range a.ephemeral {
		DeleteQueue(mgmt, q) //nolint:errcheck
	}
	if a.session != nil {
		a.session.Close(context.Background())
	}
	if a.connection != nil {
		return a.connection.Close()
	}
	return nil
}
