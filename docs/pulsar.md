# Apache Pulsar (`pmc`)

Default: `pulsar://localhost:6650` (env `PMC_SERVER`). Auth: `-p` (JWT token, set `-u token`). TLS: `pulsar+ssl://` or `--tls`.

## Addressing

Pulsar uses `persistent://tenant/namespace/topic` URLs internally. For queue commands, xmc auto-prefixes `persistent://public/default/` — use bare names:

```
send myqueue "msg"       # → persistent://public/default/myqueue (Shared subscription)
receive myqueue
```

For topic commands, use bare names or full Pulsar URLs:

```
publish mytopic "msg"
subscribe mytopic
publish persistent://custom-tenant/custom-ns/mytopic "msg"   # full URL used verbatim
```

## Subscriptions

- **Queue** (send/receive): Shared subscription `xmc-queue` — competing consumers
- **Topic** (subscribe): Exclusive by default (one consumer); `-g <group>` switches to Shared (competing consumers within the group)
- Durable: `-D -g <name>` creates a persistent subscription

```
subscribe events -g processors -n 0    # Shared subscription "processors"
subscribe events -D -g durable1 -n 0   # durable subscription
```

## Manage commands

`list`, `create-topic <name> --partitions N`, `delete-topic <name>`. Use `--admin-port` (default 8080) to override the admin REST API port.

## Supported features

- Application properties (`-P`), correlation-id, reply-to, content-type, message-id, message key (`-K`)
- Request/reply, move, forward
- TTL (`-E`): advisory header (Pulsar uses topic-level retention for actual expiry)

## Constraints

- Default tenant/namespace: `public/default` (not configurable via flags yet)
- No selectors, no priority
