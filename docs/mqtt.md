# MQTT (`mmc`) — MQTT 3.1.1

Default: `tcp://localhost:1883` (env `MMC_SERVER`). Auth: `-u`/`-p` or env `MMC_USER`/`MMC_PASSWORD`. TLS: `ssl://host:8883` or `--tls`. Optional: `--client-id` (auto-generated if unset).

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

## QoS and persistence

- Queue send/receive: always QoS 1 (at least once)
- Topic publish: QoS 1 with `-d` (persistent), QoS 0 without
- QoS 2 is not available in xmc

## Consumer groups (topic)

`-g <group>` maps to MQTT shared subscriptions: `$share/<group>/<topic>`

```
subscribe events -g processors -n 0
```

## Supported features

- Move, forward (queue-to-queue)
- Topic wildcards (`+`, `#`)

## Constraints — MQTT 3.1.1 limitations

- **No application properties** (`-P` not carried through the broker)
- **No metadata**: correlation-id, reply-to, content-type, message-id are NOT preserved
- **No selectors** (`-S`)
- **No request/reply** (`request`/`reply` commands unavailable)
- **No manage commands** (no list, purge, stats, create, delete)
- NDJSON round-trip loses all metadata — only payload survives
