# Bridging Brokers Without Data Loss

xmc can pipe messages — payload **and** metadata — from one broker to another using the `--ndjson` flag. This works between any two of the 11 supported brokers.

## How It Works

Every read command (`receive`, `peek`, `subscribe`) supports `--ndjson`, which writes **one JSON object per line to STDOUT** containing the full message: payload, application properties, and protocol metadata. Every write command (`send`, `publish`) also supports `--ndjson`, reading those same records from STDIN and reconstructing the message on the target broker.

```
┌──────────┐  --ndjson   ┌──────┐  --ndjson   ┌──────────┐
│ Broker A │ ──STDOUT──► │ pipe │ ──STDIN───► │ Broker B │
└──────────┘             └──────┘             └──────────┘
```

STDERR carries only verbose/stats output, never message data — the pipe is clean.

## NDJSON Record Format

Each line is a JSON object with these fields:

```json
{
  "data": "Hello World",
  "key": "",
  "messageId": "msg-001",
  "correlationId": "batch-42",
  "replyTo": "reply-queue",
  "contentType": "text/plain",
  "priority": 4,
  "persistent": true,
  "properties": {
    "customer": "acme",
    "region": "eu"
  }
}
```

Binary payloads are base64-encoded into `dataBase64` (instead of `data`) so the bytes survive the round-trip exactly.

## Worked Example: Artemis → RabbitMQ

### One-shot drain into a RabbitMQ queue

```bash
amc receive orders -n 0 --ndjson | rmc send orders-copy --ndjson
```

This drains all available messages from the Artemis queue `orders` and sends each into the RabbitMQ queue `orders-copy`, preserving all metadata.

### Continuous stream

```bash
amc receive orders -w -n 0 --ndjson | rmc send orders-copy --ndjson
```

The `-w` flag makes the source block for new messages — the pipe stays open and relays messages as they arrive. Press Ctrl-C to stop.

### Into a RabbitMQ exchange (fan-out)

```bash
amc receive orders -n 0 --ndjson | rmc publish anything -e broadcast --ndjson
```

This pipes messages into a RabbitMQ exchange named `broadcast`. For a fanout exchange, the routing key (`anything`) is ignored and all bound queues receive a copy.

### Topic bridging

```bash
amc subscribe events -n 0 --ndjson | rmc publish events --ndjson
```

Stream from an Artemis topic into RabbitMQ's default topic exchange (`amq.topic`), preserving the message as the routing key.

## Field Fidelity

| Field | Round-trips? | Notes |
| --- | --- | --- |
| Payload (text) | Yes | UTF-8 text in `data` field |
| Payload (binary) | Yes | Base64-encoded in `dataBase64` field |
| Application properties | Yes | The JMS / user-defined key-value pairs (`-P key=value`) |
| Message ID | Yes | `-I` / `--message-id` |
| Correlation ID | Yes | `-C` / `--correlation-id` |
| Reply-to | Yes | `-R` / `--reply-to` |
| Content type | Yes | `-T` / `--content-type` |
| Priority | Yes | `-Y` / `--priority` (0–9) |
| Persistent / durable | Yes | `-d` / `--persistent` |
| Partition key | Yes | `-K` / `--key` (Kafka) |
| TTL / expiry | **No** | Not modeled in the NDJSON record; TTL does not survive the bridge |
| JMSType / Subject | **No** | Not modeled by xmc |
| JMSTimestamp | **No** | Not modeled by xmc |

## Caveats

- **`--ndjson` must be set on BOTH sides.** Omit it on the source and you get the raw payload on STDOUT (no metadata). Omit it on the target and the JSON line becomes the literal message body.

- **Metadata fidelity depends on the source broker.** Not all brokers carry all metadata at the protocol level:
  - **MQTT** (MQTT 3.1.1) cannot carry application properties, correlation ID, reply-to, or content type — NDJSON records from `mmc` will contain only the payload.
  - **NATS** does not support application properties — records from `nmc` contain payload and basic metadata only.
  - All other brokers populate the full set of fields listed above.

- **Application-property value types pass through JSON.** String values are preserved exactly. Numeric values are serialized as JSON numbers and may be re-encoded with a different numeric type on the target broker (e.g. an integer property may arrive as a floating-point value).

- **Message IDs are preserved, not regenerated.** The target broker receives the original message ID from the source. Some brokers may assign their own internal IDs in addition.

## More Examples

### Kafka → RabbitMQ (preserving partition keys)

```bash
kmc subscribe events -n 0 --ndjson | rmc send events --ndjson
```

Kafka partition keys are carried in the `key` field of the NDJSON record.

### Redis → Artemis

```bash
redmc receive tasks -n 0 --ndjson | amc send tasks --ndjson
```

### AWS SQS → Azure Service Bus

```bash
awsmc receive orders -n 0 --ndjson | azmc send orders --ndjson
```

### Backup and restore

The NDJSON format is also useful for queue backup and restore on the same broker:

```bash
# Backup
amc receive orders -n 0 --ndjson > orders-backup.ndjson

# Restore
amc send orders --ndjson < orders-backup.ndjson
```
