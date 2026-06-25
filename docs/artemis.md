# Apache Artemis (`amc`) — AMQP 1.0

Default: `amqp://localhost:5672` (env `AMC_SERVER`). Auth: `-u`/`-p` or env `AMC_USER`/`AMC_PASSWORD`. TLS: `amqps://` or `--tls`.

## Addressing

Artemis auto-creates queues/topics on first use. Destination names are flat strings — no prefixes or transformations applied.

- `send <queue> "msg"` → ANYCAST (point-to-point, one consumer)
- `publish <topic> "msg"` → MULTICAST (fan-out, all subscribers)
- The routing type is determined by the command, not the address name
- The same address can host both ANYCAST and MULTICAST

```
send myqueue "hi"              # ANYCAST to address "myqueue"
publish notifications "alert"  # MULTICAST to address "notifications"
```

## Manage commands

`list`, `purge <queue>`, `stats <queue>`, `create-queue <name>`, `delete-queue <name>`, `create-topic <name>`, `delete-topic <name>`.

Management uses the Jolokia REST API on port 8161 (derived from the AMQP host).

## Supported features

- Application properties (`-P`), selectors (`-S`), priority (`-Y 0-9`), TTL (`-E`), persistent (`-d`)
- Request/reply (`request`/`reply -e`), move, forward
- Durable subscriptions: `subscribe <topic> -D -g <group>`

## Constraints

- Queue names are case-sensitive
- Selectors use JMS-style syntax: `-S "env='prod'"`
