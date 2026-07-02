# Apache Artemis (`amc`) — AMQP 1.0

Default: `amqp://localhost:5672` (env `AMC_SERVER`). Auth: `-u`/`-p` or env `AMC_USER`/`AMC_PASSWORD`. TLS: `amqps://` or `--tls`.

## Addresses vs Queues

Artemis has a two-level model (analogous to RabbitMQ exchanges/queues):

- **Address**: routing namespace — receives messages and forwards them to bound queues based on routing type.
  - `ANYCAST`: point-to-point; one competing-consumer queue attached; like a JMS queue.
  - `MULTICAST`: fan-out; each subscriber gets their own bound queue; like a JMS topic.
  - An address can support both routing types simultaneously.
- **Queue**: actual message store, bound to an address. Consumers link to queues, not addresses.

`send`/`receive` auto-create an address + ANYCAST queue. `publish`/`subscribe` auto-create a MULTICAST address (each subscriber gets its own ephemeral queue).

When you want to pre-create topology explicitly:
- `manage create-address <name>` — create a bare address (default `--routing-type ANYCAST`; pass `MULTICAST` or `ANYCAST,MULTICAST`)
- `manage create-queue <name>` — create a queue; `--address <addr>` binds it to a different address (default: same name, auto-created), plus `--routing-type`, `--filter`, `--durable`, `--max-consumers`, `--purge-on-no-consumers`, `--exclusive`, `--last-value [--last-value-key]`, `--non-destructive`, `--ring-size`
- `manage bind-queue <queue> <address>` — same as create-queue --address (a queue is bound at creation; no re-bind exists — delete and bind again to move it); flags `--routing-type`, `--filter`, `--durable`
- `manage create-topic <name>` — create a MULTICAST address (no queue; subscribers auto-create queues)
- `manage delete-address <name>` — delete an address and all its queues
- `manage delete-queue <name>` — delete an ANYCAST queue (and auto-delete address if empty)
- `manage update-queue <queue>` — change settings of an existing queue: `--filter` (`--filter ""` removes it), `--max-consumers`, `--purge-on-no-consumers`, `--exclusive`, `--non-destructive`, `--ring-size`
- `manage enable-queue <queue>` / `manage disable-queue <queue>` — resume/stop message dispatch (disabled queues accumulate but do not deliver)

## Routing in send/publish

- `send <address> "msg"` → ANYCAST (point-to-point, one consumer)
- `publish <address> "msg"` → MULTICAST (fan-out, all subscribers)
- Override with `--anycast` or `--multicast` flags
- The same address can host both; routing type is chosen per-message via the command used
- Queues are bound to addresses; persistent queues store their type (ANYCASE or MULTICAST)
- If a message is sent to an address that has no queues Artemis automatically creates one with the same name as the address

## Manage commands

`list`, `purge <queue>`, `stats <queue>`, `create-queue <name> [--address --routing-type --filter --durable --max-consumers --purge-on-no-consumers --exclusive --last-value --last-value-key --non-destructive --ring-size]`, `delete-queue <name>`, `update-queue <queue> [--filter --max-consumers --purge-on-no-consumers --exclusive --non-destructive --ring-size]`, `enable-queue <queue>`, `disable-queue <queue>`, `bind-queue <queue> <address> [--routing-type --filter --durable]`, `create-topic <name>`, `delete-topic <name>`, `create-address <name> [--routing-type ANYCAST|MULTICAST|ANYCAST,MULTICAST]`, `delete-address <name>`.

Management uses the Jolokia REST API on port 8161 (derived from the AMQP host).

## Supported features

- Application properties (`-P`), selectors (`-S`), priority (`-Y 0-9`), TTL (`-E`), persistent (`-d`)
- Request/reply (`request`/`reply -e`), move, forward
- Durable subscriptions: `subscribe <topic> -D -g <group>`

## Constraints

- Queue names are case-sensitive
- Selectors use JMS-style syntax: `-S "env='prod'"`
