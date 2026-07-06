# MQTT (`mmc`) — MQTT 5 (default), MQTT 3.1.1 via `--mqtt-version 3`

Default: `tcp://localhost:1883` (env `MMC_SERVER`). Auth: `-u`/`-p` or env `MMC_USER`/`MMC_PASSWORD`. TLS: `ssl://host:8883` or `--tls`. Optional: `--client-id` (auto-generated if unset), `--mqtt-version 3` (env `MMC_MQTT_VERSION`) for legacy 3.1.1-only brokers.

## Addressing

- **Queue** commands: xmc maps to MQTT shared subscriptions. `send myqueue` publishes to MQTT topic `queue/myqueue`; `receive myqueue` subscribes to `$share/xmc/queue/myqueue` (competing consumers).
- **Topic** commands: bare MQTT topic names, no transformation.

```
send myqueue "msg"
receive myqueue
publish sensors/room1/temp "22.5"
subscribe "sensors/#"              # MQTT wildcard
```

## MQTT topic wildcards

- `+` matches one level: `sensors/+/temperature`
- `#` matches all remaining levels: `sensors/#`

## QoS and retain

- `--qos 0|1|2` on send/publish/receive/subscribe (default 1, at least once)
- `--retain` on publish stores the message as the topic's retained message
- Subscriptions stay open for the whole command, so streaming reads (`-n 0`, `--for`) don't lose messages between reads

## Consumer groups (topic)

`-g <group>` maps to MQTT shared subscriptions: `$share/<group>/<topic>`

```
subscribe events -g processors -n 0
```

## Metadata (MQTT 5)

Mapped to the native MQTT 5 property slots:

- `-P key=value` → user properties
- `--content-type` → content type
- `--correlation-id` → correlation data
- `--reply-to` → response topic (queue commands prefix/strip `queue/` so `request`/`reply` and native MQTT 5 responders land in the same place)
- `-E`/`--ttl` → message expiry (seconds, rounded up)
- `--message-id` → user property `message-id` (MQTT 5 has no message-id slot; no broker-assigned ID exists, so no back-fill)

## Supported features

- Application properties and metadata (see above; MQTT 5 mode only)
- Request/reply (`request`/`reply`), TTL, move, forward (queue-to-queue)
- Topic wildcards (`+`, `#`)

## Constraints

- **No selectors** (`-S`), no priority
- **No manage commands** (MQTT has no broker management protocol)
- **`--mqtt-version 3` (MQTT 3.1.1)**: no properties or metadata at the protocol level — send/publish reject `-P`/metadata flags loudly; NDJSON round-trip loses all metadata; request/reply unavailable
