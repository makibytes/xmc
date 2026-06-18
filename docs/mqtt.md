# Getting Started with MQTT (`mmc`)

Binary: **`mmc`** — MQTT Messaging Client (MQTT 3.1.1)

```bash
go build -tags mqtt -o mmc .
```

## Authentication

`mmc` connects to any MQTT 3.1.1 broker using the standard TCP protocol. The default server is `tcp://localhost:1883`.

```bash
export MMC_SERVER="tcp://localhost:1883"
```

For password-protected brokers:

```bash
export MMC_USER="myuser"
export MMC_PASSWORD="mypassword"
```

For TLS use `ssl://host:8883` or add `--tls`. See `mmc --help` for cert options.

> **Quick start with Docker (Mosquitto):**
> ```bash
> docker run -d --name mosquitto -p 1883:1883 eclipse-mosquitto:latest \
>   mosquitto -c /dev/null -p 1883 --allow-anonymous true
> ```

---

## 1. Send and Receive

MQTT is a lightweight pub/sub protocol. xmc maps queue operations to **shared subscriptions** and topic operations to standard MQTT topics.

Queue-style (competing consumers via shared subscription):

```bash
mmc send myqueue "Hello World"
mmc receive myqueue
```

Topic-style:

```bash
# Terminal 1 (start subscriber first — MQTT has no message persistence by default)
mmc subscribe mytopic -n 1

# Terminal 2
mmc publish mytopic "Hello World"
```

Output:
```
Hello World
```

---

## 2. MQTT-Specific Features

> **Note:** MQTT does not have admin/manage commands in xmc. There are no `manage list`, `manage purge`, or lifecycle commands — MQTT brokers manage topics internally based on subscriptions.

### Topic wildcards

MQTT supports powerful topic wildcards for subscribing to hierarchies:

- `+` matches exactly one level: `sensors/+/temperature`
- `#` matches any remaining levels: `sensors/#`

```bash
# Subscribe to all sensor readings
mmc subscribe "sensors/#" -n 0
```

In another terminal:

```bash
mmc publish "sensors/room1/temperature" "22.5"
mmc publish "sensors/room2/humidity" "45"
mmc publish "sensors/room1/pressure" "1013"
```

The subscriber receives all three messages.

Subscribe to a specific sensor type across all rooms:

```bash
mmc subscribe "sensors/+/temperature" -n 0
```

This receives only `sensors/room1/temperature`, not humidity or pressure.

### Client ID

Set a custom client ID for persistent sessions and broker-side identification:

```bash
mmc subscribe mytopic --client-id "dashboard-01" -n 0
```

If not set, xmc auto-generates a unique client ID.

### Persistent delivery (QoS 1)

Use `-d` (persistent) to send with QoS 1 (at least once delivery):

```bash
mmc send myqueue "Important message" -d
```

Without `-d`, messages are sent with QoS 0 (at most once / fire-and-forget).

### MQTT metadata limitations

MQTT 3.1.1 does not support user properties at the protocol level. This means:

- Application properties (`-P`) are **not carried** through the broker
- Correlation ID (`-C`), reply-to (`-R`), content type (`-T`), and message ID (`-I`) are **not preserved**
- Message selectors (`-S`) are not available
- Request/reply (`request`/`reply`) is not supported

For metadata-rich messaging, consider a broker that supports application properties (Artemis, RabbitMQ, Kafka, Pulsar, or the cloud brokers).

---

## 3. Deep-Dive into xmc Features

### Move and Forward

Move messages between queues:

```bash
mmc move source destination -n 0
```

Continuously forward messages:

```bash
mmc forward incoming processed
```

Forward with transformation:

```bash
mmc forward raw clean -x "tr '[:lower:]' '[:upper:]'"
```

### Bulk Publishing

Send many messages at a controlled rate:

```bash
seq 100 | mmc send loadtest -l --rate 10
```

### Streaming with Stats

```bash
mmc subscribe "sensors/#" -n 0 --for 30s --stats
```

### Custom Output Format

While MQTT doesn't carry metadata, format strings still work for the payload and any locally available fields:

```bash
mmc receive myqueue -F "%s\n"
```

### JSON Output

```bash
mmc receive myqueue -J
```

---

## 4. Bridge to Another Broker

Stream from MQTT to RabbitMQ using NDJSON:

```bash
# Drain an MQTT queue into a RabbitMQ queue
mmc receive myqueue -n 0 --ndjson | rmc send target --ndjson

# Continuous bridge from MQTT topics to RabbitMQ
mmc subscribe "sensors/#" -n 0 --ndjson | rmc send sensors --ndjson
```

> **Note:** Since MQTT doesn't carry application properties or metadata, the NDJSON records will contain only the payload. Metadata (properties, correlation ID, etc.) added on the RabbitMQ side won't round-trip back through MQTT.
