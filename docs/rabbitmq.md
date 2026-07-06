# RabbitMQ (`rmc`) — AMQP 1.0

Default: `amqp://localhost:5672` (env `RMC_SERVER`). Auth: `-u`/`-p` or env `RMC_USER`/`RMC_PASSWORD`. TLS: `amqps://` or `--tls`.

## Addressing (AMQP 1.0 v2)

RabbitMQ 4.x uses AMQP 1.0 address v2 format. rmc applies smart defaults:

| Command | Result |
|---------|--------|
| `send q1 "hi"` | `/queues/q1` |
| `send -q q1 "hi"` | `/queues/q1` |
| `send -e fo1 "hi"` | `/exchanges/fo1` (fanout — no routing key, "hi" is the body) |
| `send -e amq.direct --routing-key key1 "hi"` | `/exchanges/amq.direct/key1` |
| `publish orders.eu "hi"` | `/exchanges/amq.topic/orders.eu` |
| `subscribe orders.#` | `/exchanges/amq.topic/orders.#` |
| `receive --exchange amq.direct --routing-key key1` | `/exchanges/amq.direct/key1` |
| `send /exchanges/foo/bar "hi"` | `/exchanges/foo/bar` (v2 address used verbatim) |

**Rules:**
- **send/receive** default to `/queues/<name>` (the default exchange routes by queue name)
- **publish/subscribe** default to `/exchanges/amq.topic/<name>` (topic exchange, `<name>` = routing key)
- `-e <exchange>`: with a single positional, that positional is the **message body** (no routing key — correct for `fanout`/`headers` exchanges). Use `--routing-key <key>` for `direct`/`topic` exchanges
- `-q <queue>` forces `/queues/<queue>` — `<to>` is forbidden with `-q`
- `-e` and `-q` are mutually exclusive
- Full v2 addresses (starting with `/`) are always used verbatim — highest precedence
- receive/subscribe use long-form `--exchange`/`--queue` (since `-q`=quiet, `-e`=echo)
- **Check the exchange type in the topology** — `fanout`/`headers`: omit `--routing-key`; `direct`/`topic`: provide `--routing-key`

## Subscriptions (pub/sub)

RabbitMQ 4.x AMQP 1.0 cannot consume from an exchange directly (only `/queues/...` sources are legal), so `subscribe` declares a backing queue via the Management API, binds it to the exchange with the topic as binding key, and consumes from it:

- `-g <group>` (default `xmc-consumer-group`): durable queue `<group>.<key>` — same group = competing consumers, persists and buffers between runs
- `--durable` with `-g ""`: durable queue `xmc-durable-<key>`
- `-g ""` alone: ephemeral queue `xmc-sub-<random>` (auto-expires after 5 min, deleted on exit)

Reserved characters in names/keys are percent-encoded in v2 addresses (the broker decodes them), matching the official RabbitMQ clients.

## Exchanges, bindings, and routing

Queues must be pre-created (`manage create-queue`). Exchange types: `direct`, `fanout`, `topic`, `headers`.

```
manage create-exchange myex --type topic
manage bind-queue myqueue myex --routing-key "orders.#"
send -e myex --routing-key orders.eu "msg"   # routed to myqueue via binding
manage unbind-queue myqueue myex --routing-key "orders.#"
```

## Manage commands

`list`, `purge <queue>`, `stats <queue>`, `create-queue`, `delete-queue`, `create-exchange --type <type>`, `delete-exchange`, `bind-queue <queue> <exchange> --routing-key <key>`, `unbind-queue <queue> <exchange> --routing-key <key>`.

## Constraints

- No auto-create for `send`/`receive`: queues/exchanges must exist before use (create with `manage` commands); `subscribe` auto-creates only its own backing queue
- `subscribe` and `peek -n 0` need the management plugin (HTTP port 15672)
- No per-message priority enforcement by default (enable in queue policy)
- Dead-letter: access via `receive` on the DLQ queue name directly
