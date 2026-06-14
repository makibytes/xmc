package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/makibytes/xmc/broker/backends"
)

// QueueFactory opens a fresh queue connection. The MCP server connects per tool
// call and closes immediately after, mirroring the CLI's per-invocation model:
// it keeps no long-lived broker state, so concurrent HTTP requests never share
// a connection and there is no cross-call race.
type QueueFactory func() (backends.QueueBackend, error)

// TopicFactory opens a fresh topic connection (reserved for publish/subscribe
// tools; not registered in the default set).
type TopicFactory func() (backends.TopicBackend, error)

// QueueInfo / QueueStats are neutral management result types so the mcp package
// stays broker-agnostic; each broker maps its native results into these.
type QueueInfo struct {
	Name          string `json:"name"`
	RoutingType   string `json:"routingType,omitempty"`
	MessageCount  int64  `json:"messageCount"`
	ConsumerCount int64  `json:"consumerCount"`
}

type QueueStats struct {
	Name          string `json:"name"`
	MessageCount  int64  `json:"messageCount"`
	ConsumerCount int64  `json:"consumerCount"`
	EnqueueCount  int64  `json:"enqueueCount"`
	DequeueCount  int64  `json:"dequeueCount"`
}

// Deps is everything a broker wires in to build its MCP server. Connection
// credentials are captured in these closures, so they never appear as tool
// parameters and never enter the model's context. Management hooks are
// optional: a tool is only registered when its hook is non-nil.
type Deps struct {
	ServerName    string
	ServerVersion string
	Target        string // broker URL, surfaced in descriptions for context

	NewQueue QueueFactory
	NewTopic TopicFactory // optional, reserved

	ListQueues func(ctx context.Context) ([]QueueInfo, error)
	PurgeQueue func(ctx context.Context, queue string) (int64, error)
	QueueStats func(ctx context.Context, queue string) (*QueueStats, error)
}

// NewServerFromDeps builds a Server with the standard messaging tool set plus
// any management tools whose hooks were supplied.
func NewServerFromDeps(d Deps) *Server {
	s := NewServer(orDefault(d.ServerName, "xmc"), orDefault(d.ServerVersion, "dev"))

	registerSend(s, d)
	registerRequest(s, d)
	registerPeek(s, d)
	registerReceive(s, d)
	registerPing(s, d)

	if d.ListQueues != nil {
		registerListQueues(s, d)
	}
	if d.QueueStats != nil {
		registerQueueStats(s, d)
	}
	if d.PurgeQueue != nil {
		registerPurgeQueue(s, d)
	}
	return s
}

func orDefault(v, def string) string {
	if v == "" {
		return def
	}
	return v
}

// withQueue opens a connection, runs fn, and always closes. Connection failures
// are surfaced with the target so the agent can see what was unreachable.
func withQueue(d Deps, fn func(backends.QueueBackend) (*ToolResult, error)) (*ToolResult, error) {
	q, err := d.NewQueue()
	if err != nil {
		return nil, fmt.Errorf("could not connect to broker %s: %v", d.Target, err)
	}
	defer q.Close()
	return fn(q)
}

// ---- send ------------------------------------------------------------------

type sendArgs struct {
	Address       string         `json:"address"`
	Body          string         `json:"body"`
	Properties    map[string]any `json:"properties"`
	ContentType   string         `json:"content_type"`
	CorrelationID string         `json:"correlation_id"`
	MessageID     string         `json:"message_id"`
	ReplyTo       string         `json:"reply_to"`
	Priority      *int           `json:"priority"`
	Persistent    bool           `json:"persistent"`
	TTLms         int64          `json:"ttl_ms"`
	Count         *int           `json:"count"`
}

func registerSend(s *Server, d Deps) {
	s.AddTool(&Tool{
		Name: "send",
		Description: "Send a message to a queue or anycast address (fire-and-forget; does not wait for a reply). " +
			"Use this to deliver a one-way message, e.g. a 'ping' to a microservice listening on an address. " +
			"To send and wait for a response, use 'request' instead. To deliver to a multicast topic, this still applies in Artemis since addresses are unified.",
		InputSchema: object(map[string]any{
			"address":        stringProp("Target queue or address name, e.g. \"A.foo\"."),
			"body":           stringProp("Message payload as text."),
			"properties":     mapProp("Optional application properties (string keys to values)."),
			"content_type":   stringProp("MIME type of the body (default \"text/plain\")."),
			"correlation_id": stringProp("Optional correlation ID."),
			"message_id":     stringProp("Optional message ID."),
			"reply_to":       stringProp("Optional reply-to address."),
			"priority":       intProp("Message priority 0-9 (default 4)."),
			"persistent":     boolProp("Persist the message to broker storage (default false)."),
			"ttl_ms":         intProp("Time-to-live in milliseconds (0 = no expiry)."),
			"count":          intProp("Number of times to send the message (default 1)."),
		}, "address", "body"),
		Annotations: map[string]any{
			"title":           "Send message",
			"readOnlyHint":    false,
			"destructiveHint": false,
			"idempotentHint":  false,
			"openWorldHint":   true,
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a sendArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, fmt.Errorf("invalid arguments: %v", err)
			}
			if a.Address == "" {
				return nil, errors.New("address is required")
			}
			count := 1
			if a.Count != nil {
				count = *a.Count
			}
			if count < 1 {
				return nil, errors.New("count must be at least 1")
			}
			priority := 4
			if a.Priority != nil {
				priority = *a.Priority
			}
			contentType := orDefault(a.ContentType, "text/plain")

			return withQueue(d, func(q backends.QueueBackend) (*ToolResult, error) {
				opts := backends.SendOptions{
					Queue:         a.Address,
					Message:       []byte(a.Body),
					Properties:    a.Properties,
					MessageID:     a.MessageID,
					CorrelationID: a.CorrelationID,
					ReplyTo:       a.ReplyTo,
					ContentType:   contentType,
					Priority:      priority,
					Persistent:    a.Persistent,
					TTL:           a.TTLms,
				}
				for i := 0; i < count; i++ {
					if err := q.Send(ctx, opts); err != nil {
						return nil, fmt.Errorf("send failed after %d/%d messages: %v", i, count, err)
					}
				}
				return jsonResult(map[string]any{"sent": count, "address": a.Address})
			})
		},
	})
}

// ---- request ---------------------------------------------------------------

type requestArgs struct {
	Address        string         `json:"address"`
	Body           string         `json:"body"`
	ReplyTo        string         `json:"reply_to"`
	TimeoutSeconds *float64       `json:"timeout_seconds"`
	Properties     map[string]any `json:"properties"`
	ContentType    string         `json:"content_type"`
	CorrelationID  string         `json:"correlation_id"`
	Priority       *int           `json:"priority"`
}

func registerRequest(s *Server, d Deps) {
	s.AddTool(&Tool{
		Name: "request",
		Description: "Send a message and wait for a single reply (request-reply pattern). " +
			"This is the tool to use for a 'ping' that expects a 'pong': it sends to the address, " +
			"then consumes one message from the reply address and returns it. Returns isError if no reply " +
			"arrives within the timeout. For one-way delivery with no reply, use 'send'.",
		InputSchema: object(map[string]any{
			"address":         stringProp("Target queue or address to send the request to, e.g. \"A.foo\"."),
			"body":            stringProp("Request payload as text (e.g. \"ping\")."),
			"reply_to":        stringProp("Reply address to listen on (default \"xmc.reply\")."),
			"timeout_seconds": numberProp("How long to wait for the reply, in seconds (default 30)."),
			"properties":      mapProp("Optional application properties."),
			"content_type":    stringProp("MIME type of the body (default \"text/plain\")."),
			"correlation_id":  stringProp("Optional correlation ID for matching the reply."),
			"priority":        intProp("Message priority 0-9 (default 4)."),
		}, "address", "body"),
		Annotations: map[string]any{
			"title":          "Request/reply",
			"readOnlyHint":   false,
			"idempotentHint": false,
			"openWorldHint":  true,
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a requestArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, fmt.Errorf("invalid arguments: %v", err)
			}
			if a.Address == "" {
				return nil, errors.New("address is required")
			}
			replyTo := orDefault(a.ReplyTo, "xmc.reply")
			timeout := float32(30)
			if a.TimeoutSeconds != nil {
				timeout = float32(*a.TimeoutSeconds)
			}
			priority := 4
			if a.Priority != nil {
				priority = *a.Priority
			}

			callCtx, cancel := context.WithTimeout(ctx, time.Duration(timeout+5)*time.Second)
			defer cancel()

			return withQueue(d, func(q backends.QueueBackend) (*ToolResult, error) {
				sendOpts := backends.SendOptions{
					Queue:         a.Address,
					Message:       []byte(a.Body),
					Properties:    a.Properties,
					CorrelationID: a.CorrelationID,
					ReplyTo:       replyTo,
					ContentType:   orDefault(a.ContentType, "text/plain"),
					Priority:      priority,
				}
				if err := q.Send(callCtx, sendOpts); err != nil {
					return nil, fmt.Errorf("failed to send request to %s: %v", a.Address, err)
				}
				msg, err := q.Receive(callCtx, backends.ReceiveOptions{
					Queue:       replyTo,
					Timeout:     timeout,
					Acknowledge: true,
					Verbosity:   backends.VerbosityNormal,
				})
				if err != nil {
					if errors.Is(err, context.DeadlineExceeded) || errors.Is(err, backends.ErrNoMessageAvailable) {
						return nil, fmt.Errorf("no reply received on %s within %.0fs", replyTo, timeout)
					}
					return nil, fmt.Errorf("failed to receive reply: %v", err)
				}
				if msg == nil {
					return nil, fmt.Errorf("no reply received on %s within %.0fs", replyTo, timeout)
				}
				return jsonResult(toMessageJSON(msg))
			})
		},
	})
}

// ---- peek / receive --------------------------------------------------------

type readArgs struct {
	Queue          string   `json:"queue"`
	Count          *int     `json:"count"`
	TimeoutSeconds *float64 `json:"timeout_seconds"`
	Selector       string   `json:"selector"`
}

func readSchema(verb string) map[string]any {
	return object(map[string]any{
		"queue":           stringProp("Queue or address to read from."),
		"count":           intProp(fmt.Sprintf("Maximum number of messages to %s (default 1).", verb)),
		"timeout_seconds": numberProp("How long to wait for a message before returning, in seconds (default 2)."),
		"selector":        stringProp("Optional JMS-style selector, e.g. \"color='red'\"."),
	}, "queue")
}

// readMessages drains up to count messages, stopping early when none are
// available. acknowledge=false is a browse (peek); true is a destructive read.
func readMessages(ctx context.Context, d Deps, a readArgs, acknowledge bool) (*ToolResult, error) {
	if a.Queue == "" {
		return nil, errors.New("queue is required")
	}
	count := 1
	if a.Count != nil {
		count = *a.Count
	}
	if count < 1 {
		return nil, errors.New("count must be at least 1")
	}
	timeout := float32(2)
	if a.TimeoutSeconds != nil {
		timeout = float32(*a.TimeoutSeconds)
	}

	callCtx, cancel := context.WithTimeout(ctx, time.Duration((timeout*float32(count))+5)*time.Second)
	defer cancel()

	return withQueue(d, func(q backends.QueueBackend) (*ToolResult, error) {
		opts := backends.ReceiveOptions{
			Queue:       a.Queue,
			Timeout:     timeout,
			Acknowledge: acknowledge,
			Verbosity:   backends.VerbosityNormal,
			Selector:    a.Selector,
		}
		messages := make([]messageJSON, 0, count)
		for i := 0; i < count; i++ {
			msg, err := q.Receive(callCtx, opts)
			if err != nil {
				if errors.Is(err, backends.ErrNoMessageAvailable) || errors.Is(err, context.DeadlineExceeded) {
					break
				}
				return nil, fmt.Errorf("read failed after %d messages: %v", len(messages), err)
			}
			if msg == nil {
				break
			}
			messages = append(messages, toMessageJSON(msg))
		}
		return jsonResult(map[string]any{
			"queue":    a.Queue,
			"count":    len(messages),
			"messages": messages,
		})
	})
}

func registerPeek(s *Server, d Deps) {
	s.AddTool(&Tool{
		Name: "peek",
		Description: "Look at messages on a queue without removing them (non-destructive browse). " +
			"Safe to call for inspection. Returns up to 'count' messages, or fewer if the queue is shallow. " +
			"To consume (remove) messages, use 'receive'.",
		InputSchema: readSchema("peek"),
		Annotations: map[string]any{
			"title":         "Peek (browse) messages",
			"readOnlyHint":  true,
			"openWorldHint": true,
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a readArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, fmt.Errorf("invalid arguments: %v", err)
			}
			return readMessages(ctx, d, a, false)
		},
	})
}

func registerReceive(s *Server, d Deps) {
	s.AddTool(&Tool{
		Name: "receive",
		Description: "Receive messages from a queue, REMOVING them from the broker (destructive read). " +
			"Consumed messages are gone. Use 'peek' instead if you only need to inspect without consuming.",
		InputSchema: readSchema("receive"),
		Annotations: map[string]any{
			"title":           "Receive (consume) messages",
			"readOnlyHint":    false,
			"destructiveHint": true,
			"idempotentHint":  false,
			"openWorldHint":   true,
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a readArgs
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, fmt.Errorf("invalid arguments: %v", err)
			}
			return readMessages(ctx, d, a, true)
		},
	})
}

// ---- ping ------------------------------------------------------------------

func registerPing(s *Server, d Deps) {
	s.AddTool(&Tool{
		Name: "ping",
		Description: "Check connectivity to the broker by opening a connection (including auth/TLS handshake) " +
			"and reporting the round-trip time. Read-only; does not send or consume any message.",
		InputSchema: object(map[string]any{}),
		Annotations: map[string]any{
			"title":         "Ping broker",
			"readOnlyHint":  true,
			"openWorldHint": true,
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (*ToolResult, error) {
			start := time.Now()
			q, err := d.NewQueue()
			elapsed := time.Since(start)
			if err != nil {
				return nil, fmt.Errorf("broker %s unreachable: %v", d.Target, err)
			}
			_ = q.Close()
			return jsonResult(map[string]any{
				"ok":     true,
				"target": d.Target,
				"rttMs":  float64(elapsed.Microseconds()) / 1000.0,
			})
		},
	})
}

// ---- management (optional) -------------------------------------------------

func registerListQueues(s *Server, d Deps) {
	s.AddTool(&Tool{
		Name:        "manage_list_queues",
		Description: "List queues/addresses on the broker with message and consumer counts. Read-only.",
		InputSchema: object(map[string]any{}),
		Annotations: map[string]any{
			"title":         "List queues",
			"readOnlyHint":  true,
			"openWorldHint": true,
		},
		Handler: func(ctx context.Context, _ json.RawMessage) (*ToolResult, error) {
			queues, err := d.ListQueues(ctx)
			if err != nil {
				return nil, fmt.Errorf("failed to list queues: %v", err)
			}
			return jsonResult(map[string]any{"count": len(queues), "queues": queues})
		},
	})
}

func registerQueueStats(s *Server, d Deps) {
	s.AddTool(&Tool{
		Name:        "manage_queue_stats",
		Description: "Show statistics for one queue (message count, consumers, lifetime enqueue/dequeue). Read-only.",
		InputSchema: object(map[string]any{
			"queue": stringProp("Queue name to inspect."),
		}, "queue"),
		Annotations: map[string]any{
			"title":         "Queue statistics",
			"readOnlyHint":  true,
			"openWorldHint": true,
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a struct {
				Queue string `json:"queue"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, fmt.Errorf("invalid arguments: %v", err)
			}
			if a.Queue == "" {
				return nil, errors.New("queue is required")
			}
			stats, err := d.QueueStats(ctx, a.Queue)
			if err != nil {
				return nil, fmt.Errorf("failed to get stats for %s: %v", a.Queue, err)
			}
			return jsonResult(stats)
		},
	})
}

func registerPurgeQueue(s *Server, d Deps) {
	s.AddTool(&Tool{
		Name: "manage_purge_queue",
		Description: "Permanently delete ALL messages from a queue. This is irreversible. " +
			"As a guard, 'confirm' must be set to true or the call is rejected.",
		InputSchema: object(map[string]any{
			"queue":   stringProp("Queue to purge."),
			"confirm": boolProp("Must be true to actually purge. Acts as a safety interlock."),
		}, "queue", "confirm"),
		Annotations: map[string]any{
			"title":           "Purge queue",
			"readOnlyHint":    false,
			"destructiveHint": true,
			"idempotentHint":  true,
			"openWorldHint":   true,
		},
		Handler: func(ctx context.Context, raw json.RawMessage) (*ToolResult, error) {
			var a struct {
				Queue   string `json:"queue"`
				Confirm bool   `json:"confirm"`
			}
			if err := json.Unmarshal(raw, &a); err != nil {
				return nil, fmt.Errorf("invalid arguments: %v", err)
			}
			if a.Queue == "" {
				return nil, errors.New("queue is required")
			}
			if !a.Confirm {
				return nil, errors.New("refusing to purge: set confirm=true to proceed (this permanently deletes all messages)")
			}
			n, err := d.PurgeQueue(ctx, a.Queue)
			if err != nil {
				return nil, fmt.Errorf("failed to purge %s: %v", a.Queue, err)
			}
			return jsonResult(map[string]any{"purged": n, "queue": a.Queue})
		},
	})
}
