# MCP server

`xmc` can run as a [Model Context Protocol](https://modelcontextprotocol.io)
server, exposing its broker operations as tools an AI agent can call. The same
binary that serves the CLI serves MCP — no separate artifact.

```
amc mcp                     # stdio transport (agent launches it as a subprocess)
amc mcp --http :8080        # Streamable HTTP transport (long-lived service)
```

The broker connection is taken from the usual flags and environment variables
(`--server`/`--user`/`--password`, or `AMC_SERVER`/`AMC_USER`/`AMC_PASSWORD` for
the Artemis flavor). It is configured once, at startup or deploy time, so the
**agent only ever supplies message addresses and bodies — never credentials.**

## Transports

| Transport | When to use | How |
| --------- | ----------- | --- |
| stdio | Agent runs co-located and spawns the binary (e.g. Claude Desktop / Code style configs) | `amc mcp` |
| Streamable HTTP | Remote agent over the network; in-cluster Deployment | `amc mcp --http :8080` (endpoint `/mcp`, health `/healthz`) |

The HTTP transport is stateless (no sessions, no server-initiated SSE stream):
each POST carries one JSON-RPC message and gets one JSON response. That is
sufficient for request/response tool use and makes horizontal scaling trivial.

## Tools

| Tool | Effect | Annotations |
| ---- | ------ | ----------- |
| `send` | Send a one-way message to an address | — |
| `request` | Send and wait for one reply (the "ping → pong" tool) | — |
| `peek` | Browse messages without removing them | `readOnlyHint` |
| `receive` | Consume (remove) messages | `destructiveHint` |
| `ping` | Connectivity / round-trip check | `readOnlyHint` |
| `manage_list_queues` | List queues with counts | `readOnlyHint` |
| `manage_queue_stats` | Stats for one queue | `readOnlyHint` |
| `manage_purge_queue` | Delete all messages on a queue (requires `confirm: true`) | `destructiveHint` |

Messages are returned in the same JSON shape as `receive --ndjson`
(`data`/`dataBase64`/`messageId`/`properties`/…), so the message model is
consistent across the CLI and the MCP server. Tool failures (timeouts, bad
input, unreachable broker) come back as `isError` results with a message written
for recovery, rather than as opaque protocol faults.

Management tools are only registered for brokers that support management
operations (currently Artemis).

## Deploy on Kubernetes

Build a broker-specific image and run it next to the broker:

```sh
docker build -f Dockerfile.mcp -t ghcr.io/makibytes/xmc-mcp:artemis .
kubectl apply -f deploy/kubernetes/xmc-mcp.yaml
```

Then point your MCP client at the Service:

```
http://xmc-mcp.<namespace>.svc.cluster.local:8080/mcp
```

Set `AMC_SERVER` to your in-cluster Artemis AMQP Service and put SASL
credentials in the `xmc-mcp-broker` Secret.

## Extending

`publish`/`subscribe` (topic) tools are easy to add: a `TopicFactory` is already
threaded through `mcp.Deps`; register them alongside the queue tools in
`mcp/tools.go`. Other brokers gain the server by adding `mcp.NewCommand(...)` to
their `GetRootCommand` (see `broker/artemis.go`); messaging tools work through
the shared `backends` interfaces, and management tools light up automatically
when the corresponding hooks are supplied.
