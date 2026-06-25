# Forwarding & Bridging Messages

xmc offers three ways to relay messages. Pick the one that fits your scenario:

| | `forward` | `bridge` | `receive \| send` |
|---|---|---|---|
| **Form** | Single command | Single command | Two-stage pipeline |
| **Scope** | Same broker | Cross-broker | Same or cross-broker |
| **Metadata** | Always preserved | Always preserved (NDJSON) | Only with `--ndjson` on both sides |
| **Liveness** | Continuous (polls for new messages) | Continuous | Depends on flags (`-w`, `-n 0`) |
| **Transform** | `-x 'jq …'` per message, metadata kept | — | `\| jq \|` in pipeline (loses metadata) |
| **Recovery** | Consumed-but-unsent payload written to stdout | Consumed-but-unsent payload written to stdout | — |
| **Topic-only brokers** | Topic variant (e.g. Kafka) | Topic variant | Yes |

## When to Use What

### `forward` — same-broker relay

Continuously stream messages from one queue (or topic) to another on the **same broker**. Metadata is preserved by default — no `--ndjson` needed. Keeps polling as new messages arrive until interrupted, a `--for` window expires, or `--count` is reached.

```bash
# Mirror a queue (same broker)
forward orders orders-archive

# Redrive a DLQ with a transform
forward dlq orders -x 'jq ".status = \"retry\""'

# Kafka topic-to-topic
forward raw-events clean-events -x 'jq "del(.debug)"' --for 1h --stats
```

### `bridge` — cross-broker relay

Stream messages to a **different broker** by spawning the target binary as a subprocess and piping NDJSON to its stdin. `--ndjson` is auto-appended to the target command. Works in the regular shell and AI Shell's command mode.

```bash
# Artemis → Kafka
amc bridge orders --to 'kmc send orders-mirror'

# Kafka → RabbitMQ (topic to queue, time-bounded)
kmc bridge events --to 'rmc send events-archive' --for 1h --stats

# Redis → AWS SQS
redmc bridge tasks --to 'awsmc send tasks'
```

### `receive | send --ndjson` — manual pipeline

The low-level building block. Useful for backup/restore to a file, ad-hoc composition with shell tools, or when you need full control over flags on both sides.

```bash
# Backup a queue to a file
amc receive orders -n 0 --ndjson > orders-backup.ndjson

# Restore from file
amc send orders --ndjson < orders-backup.ndjson

# Cross-broker with inline filtering
amc receive orders -n 0 --ndjson | jq 'select(.properties.region == "eu")' | rmc send eu-orders --ndjson
```

## NDJSON Record Format

Each line is a JSON object. Binary payloads use `dataBase64` instead of `data`.

```json
{"data":"Hello","key":"","messageId":"msg-001","correlationId":"batch-42","replyTo":"reply-q","contentType":"text/plain","priority":4,"persistent":true,"properties":{"region":"eu"}}
```

### Field Fidelity

| Field | Round-trips? | Notes |
|---|---|---|
| Payload (text) | Yes | UTF-8 in `data` |
| Payload (binary) | Yes | Base64 in `dataBase64` |
| Application properties | Yes | `-P key=value` |
| Message ID | Yes | `-I` / `--message-id` |
| Correlation ID | Yes | `-C` / `--correlation-id` |
| Reply-to | Yes | `-R` / `--reply-to` |
| Content type | Yes | `-T` / `--content-type` |
| Priority | Yes | `-Y` / `--priority` (0–9) |
| Persistent / durable | Yes | `-d` / `--persistent` |
| Partition key | Yes | `-K` / `--key` (Kafka) |
| TTL / expiry | **No** | Not in the NDJSON record |

### Broker-Specific Caveats

- **MQTT** (3.1.1): no application properties, correlation ID, reply-to, or content type at the protocol level — NDJSON records from `mmc` contain only the payload.
- **NATS**: no application properties — records contain payload and basic metadata only.
- **Message IDs** are preserved, not regenerated. The target broker receives the original ID.
- **Numeric property values** pass through JSON and may change type (e.g. integer → float).
