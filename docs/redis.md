# Redis Streams (`redmc`)

Default: `redis://localhost:6379` (env `REDMC_SERVER`). Auth: `redis://:password@host` or `-u`/`-p`. TLS: `rediss://` or `--tls`.

## Addressing

Redis uses Streams for both queues and topics. xmc prefixes keys automatically:

- `send myqueue` → Redis key `xmc:queue:myqueue`
- `publish events` → Redis key `xmc:topic:events`
- Queue: `XADD` + consumer group `xmc-queue` (competing consumers, `XACK`+`XDEL` on receive)
- Topic: `XADD` with `MAXLEN ~ 10000` approximate trimming

```
send myqueue "msg"
receive myqueue
publish events "msg"
subscribe events -g analytics -n 0    # consumer group "analytics"
subscribe events -n 0                 # independent subscriber (fan-out)
```

## Manage commands

`list`, `purge <queue>`, `stats <queue>`, `create-queue <name>`, `delete-queue <name>`, `create-topic <name>`, `delete-topic <name>`.

## Supported features

- Application properties (`-P`), correlation-id, reply-to, content-type, message-id
- Request/reply, move, forward
- Durable subscriptions: `-D -g <group>` (group retains read position)

## Constraints

- No per-message TTL (Redis Streams have no per-entry expiry)
- No selectors, no priority
- Topic trimming fixed at `MAXLEN ~ 10000`
- Key prefix `xmc:` is not configurable yet
- Queue names and topic names must not contain `:`
