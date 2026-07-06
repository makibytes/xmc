//go:build kafka

package kafka

import (
	"context"
	"fmt"
	"strconv"
	"sync"
	"time"

	"github.com/makibytes/xmc/broker/backends"
	"github.com/makibytes/xmc/log"
	kafka "github.com/segmentio/kafka-go"
)

const propTTL = "ttl"

// keyAwareBalancer uses Hash when a message key is set (so --key routes
// deterministically), and falls back to LeastBytes for keyless messages.
type keyAwareBalancer struct {
	hash       kafka.Hash
	leastBytes kafka.LeastBytes
}

func (b *keyAwareBalancer) Balance(msg kafka.Message, partitions ...int) int {
	if len(msg.Key) > 0 {
		return b.hash.Balance(msg, partitions...)
	}
	return b.leastBytes.Balance(msg, partitions...)
}

// TopicAdapter adapts Kafka to the TopicBackend interface
type TopicAdapter struct {
	connArgs ConnArguments
	brokers  []string
	writer   *kafka.Writer

	readerMu  sync.Mutex
	reader    *kafka.Reader
	readerKey string
}

// NewTopicAdapter creates a new Kafka topic adapter
func NewTopicAdapter(connArgs ConnArguments) (*TopicAdapter, error) {
	brokers, tlsConfig, err := parseKafkaURL(connArgs.Server, connArgs.TLS)
	if err != nil {
		return nil, err
	}

	writer := kafka.NewWriter(kafka.WriterConfig{
		Brokers:  brokers,
		Balancer: &keyAwareBalancer{},
		Dialer:   buildDialer(connArgs, tlsConfig),
	})
	writer.AllowAutoTopicCreation = true

	return &TopicAdapter{connArgs: connArgs, brokers: brokers, writer: writer}, nil
}

// Publish implements backends.TopicBackend
func (a *TopicAdapter) Publish(ctx context.Context, opts backends.PublishOptions) error {
	var headers []kafka.Header
	addHeader := func(key, value string) {
		if value != "" {
			headers = append(headers, kafka.Header{Key: key, Value: []byte(value)})
		}
	}
	addHeader(backends.PropContentType, opts.ContentType)
	addHeader(backends.PropCorrelationID, opts.CorrelationID)
	addHeader(backends.PropMessageID, opts.MessageID)
	addHeader(backends.PropReplyTo, opts.ReplyTo)
	if opts.TTL > 0 {
		addHeader(propTTL, strconv.FormatInt(opts.TTL, 10))
	}
	for k, v := range backends.StringifyProps(opts.Properties) {
		addHeader(k, v)
	}

	message := kafka.Message{
		Topic:   opts.Topic,
		Key:     []byte(opts.Key),
		Value:   opts.Message,
		Headers: headers,
	}

	log.Verbose("💌 publishing message to topic %s...", opts.Topic)
	if err := a.writer.WriteMessages(ctx, message); err != nil {
		return hintAdvertisedListeners(fmt.Errorf("failed to publish message: %w", err), a.brokers)
	}
	return nil
}

// Subscribe implements backends.TopicBackend
func (a *TopicAdapter) Subscribe(ctx context.Context, opts backends.SubscribeOptions) (*backends.Message, error) {
	args := ReceiveArguments{
		Topic:     opts.Topic,
		GroupID:   opts.GroupID,
		Timeout:   opts.Timeout,
		Wait:      opts.Wait,
		Partition: -1,
		Offset:    OffsetUnset,
	}

	if opts.Extra != nil {
		if v, ok := opts.Extra["partition"]; ok {
			p, err := strconv.Atoi(v)
			if err != nil {
				return nil, fmt.Errorf("invalid --partition value %q: %w", v, err)
			}
			args.Partition = p
		}
		if v, ok := opts.Extra["offset"]; ok {
			switch v {
			case "earliest":
				args.Offset = kafka.FirstOffset
			case "latest":
				args.Offset = kafka.LastOffset
			default:
				o, err := strconv.ParseInt(v, 10, 64)
				if err != nil {
					return nil, fmt.Errorf("invalid --offset value %q (use earliest, latest, or a number): %w", v, err)
				}
				args.Offset = o
			}
			if args.Partition < 0 {
				return nil, fmt.Errorf("--offset requires --partition (offsets are per-partition)")
			}
		}
	}

	reader, err := a.getReader(args)
	if err != nil {
		return nil, err
	}

	message, err := fetchMessage(ctx, reader, args, a.brokers)
	if err != nil {
		return nil, err
	}
	if message == nil {
		return nil, backends.ErrNoMessageAvailable
	}

	return convertKafkaToBackendMessage(message, opts.Verbosity >= backends.VerbosityVerbose), nil
}

// receiveArgsKey identifies which (topic, group, partition, offset) a Reader was
// built for, so getReader knows whether it can reuse the cached one or must
// build a fresh one for different subscribe parameters.
func receiveArgsKey(args ReceiveArguments) string {
	if args.Partition >= 0 {
		return fmt.Sprintf("topic=%s|partition=%d|offset=%d", args.Topic, args.Partition, args.Offset)
	}
	return fmt.Sprintf("topic=%s|group=%s", args.Topic, args.GroupID)
}

// getReader returns a Reader for args, reusing the previous one when the
// subscription target (topic/group/partition) hasn't changed. Callers such as
// forward/bridge/drain-mode subscribe invoke Subscribe repeatedly in a tight
// poll loop; a fresh Reader per call would force a full consumer-group join on
// every iteration, which typically costs far more than the poll interval itself.
func (a *TopicAdapter) getReader(args ReceiveArguments) (*kafka.Reader, error) {
	key := receiveArgsKey(args)

	a.readerMu.Lock()
	defer a.readerMu.Unlock()

	if a.reader != nil && a.readerKey == key {
		return a.reader, nil
	}

	if a.reader != nil {
		a.reader.Close()
	}

	brokers, tlsConfig, err := parseKafkaURL(a.connArgs.Server, a.connArgs.TLS)
	if err != nil {
		return nil, err
	}

	log.Verbose("📥 creating Kafka reader...")

	readerConfig := kafka.ReaderConfig{
		Brokers: brokers,
		Topic:   args.Topic,
		// MinBytes: 1 (kafka-go's own default) so the broker answers as soon as
		// any data is available, instead of holding the fetch open until MaxWait
		// elapses — this CLI deals in individual small messages, not high-volume
		// batches, so latency matters more than request-count efficiency.
		MinBytes: 1,
		MaxBytes: 10e6,        // 10MB
		MaxWait:  time.Second, // cap the broker long-poll so an empty partition keeps --timeout responsive
		Dialer:   buildDialer(a.connArgs, tlsConfig),
	}

	if args.Partition >= 0 {
		readerConfig.Partition = args.Partition
	} else {
		readerConfig.GroupID = args.GroupID
	}

	reader := kafka.NewReader(readerConfig)

	// ReaderConfig.StartOffset only applies to consumer groups; partition
	// readers position via SetOffset, which also resolves the FirstOffset /
	// LastOffset sentinels (--offset earliest / latest).
	if args.Partition >= 0 && args.Offset != OffsetUnset {
		if err := reader.SetOffset(args.Offset); err != nil {
			reader.Close()
			return nil, fmt.Errorf("setting offset %d on partition %d: %w", args.Offset, args.Partition, err)
		}
	}

	a.reader = reader
	a.readerKey = key
	return a.reader, nil
}

// Close implements backends.TopicBackend
func (a *TopicAdapter) Close() error {
	a.readerMu.Lock()
	reader := a.reader
	a.reader = nil
	a.readerMu.Unlock()

	var err error
	if reader != nil {
		err = reader.Close()
	}
	if a.writer != nil {
		if werr := a.writer.Close(); werr != nil && err == nil {
			err = werr
		}
	}
	return err
}

func convertKafkaToBackendMessage(msg *kafka.Message, withMetadata bool) *backends.Message {
	result := &backends.Message{
		Data:       msg.Value,
		Key:        string(msg.Key),
		Properties: make(map[string]any),
	}

	for _, h := range msg.Headers {
		result.Properties[h.Key] = string(h.Value)
	}

	extract := func(key string, target *string) {
		if v, ok := result.Properties[key]; ok {
			*target = v.(string)
			delete(result.Properties, key)
		}
	}
	extract(backends.PropContentType, &result.ContentType)
	extract(backends.PropCorrelationID, &result.CorrelationID)
	extract(backends.PropMessageID, &result.MessageID)
	extract(backends.PropReplyTo, &result.ReplyTo)
	// propTTL is an xmc-internal transport header (set by Publish when --ttl is
	// given), not application data — strip it so it doesn't leak into
	// Properties on receive/subscribe.
	delete(result.Properties, propTTL)

	// Kafka has no server-assigned message ID; the broker-assigned identity of
	// a record is its coordinate. Back-fill when the sender set none.
	if result.MessageID == "" {
		result.MessageID = fmt.Sprintf("%s:%d:%d", msg.Topic, msg.Partition, msg.Offset)
	}

	if withMetadata {
		result.InternalMetadata = map[string]any{
			"Topic":     msg.Topic,
			"Partition": msg.Partition,
			"Offset":    msg.Offset,
			"Time":      msg.Time,
		}
		if len(msg.Key) > 0 {
			result.InternalMetadata["Key"] = string(msg.Key)
		}
	}

	return result
}
