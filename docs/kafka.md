# Apache Kafka (`kmc`)

Default: `kafka://localhost:9092` (env `KMC_SERVER`). Auth: `-u`/`-p` (SASL PLAIN). TLS: `kafka+ssl://` or `--tls`. Multiple brokers: `kafka://b1:9092,b2:9092`.

## Addressing

Topic-only broker — no queue commands (send/receive/peek/request/reply/move unavailable). Topic names are bare strings, no transformation.

```
publish mytopic "msg"
subscribe mytopic
publish mytopic -K "user-42" "msg"    # -K sets the partition key
```

## Consumer groups

`-g <group>` (default: `xmc-consumer-group`). Kafka manages offsets per group — restart with the same group to resume. Multiple consumers in the same group share partitions.

```
subscribe orders -g processors -n 0
```

## Partition reads

`--partition N` reads a single partition directly (no consumer group, nothing is committed). `--offset earliest|latest|<number>` positions the read and requires `--partition`:

```
subscribe orders --partition 0 --offset earliest -n 0
subscribe orders --partition 2 --offset 1500
```

## Topic forward / bridge

Kafka supports topic-to-topic forwarding (same broker) and bridging (cross-broker relay to another xmc binary):

```
forward source-topic dest-topic
forward raw clean -x "jq '.status = \"done\"'"
bridge orders --to 'rmc send orders-mirror'
```

## Manage commands

`list`, `create-topic <name> --partitions N --replication-factor N --config key=value`, `delete-topic <name>`, `update-topic <name> [--partitions N] [--config key=value]` (only given settings change; partitions can only increase), `stats <name>` (message count, summed across partitions), `delete-consumer-group <name>` (group must have no active members).

No `create-consumer-group`: Kafka creates a group implicitly the moment a consumer with that `group.id` first joins — there is no admin API to pre-create one.

No `purge`: Kafka's topic-truncate equivalent (`DeleteRecords`) has no client wrapper in the Go library this tool uses, only the raw protocol API key.

## Supported features

- Application properties (`-P`, carried as Kafka headers), content-type, correlation-id, message-id
- Message key (`-K`) for partition affinity
- TTL (`-E`): stamps a `ttl` header (advisory only — Kafka uses topic-level `retention.ms` for actual expiry)

## Constraints

- Topic-only: no queue commands
- No selectors, no priority, no persistent flag (Kafka always persists)
- Topics auto-created on publish by default
