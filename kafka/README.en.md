# DBX Kafka

[中文](README.md) · [Workspace contribution guide](../CONTRIBUTING.md)

DBX Kafka is a visual workspace for Apache Kafka operations and troubleshooting.
It brings topics, messages, consumer groups, ACLs, and Schema Registry into one
interface for everyday development, testing, and production administration.

![DBX plugin center](../shared/host-e2e/screenshots/02-plugin-center.png)

## Use cases

- Confirm topic, partition, consumer-group, and cluster metadata quickly.
- Investigate payload formats, filters, consumer lag, and offset behavior.
- Produce messages, adjust offsets, and manage ACLs behind read-only and confirmation safeguards.

## Highlights

- Inspect topics, partitions, ISR, configuration, and cluster metadata.
- Produce and consume messages with filtering, field search, offset strategies,
  Base64 handling, and common compression/decoding options.
- Stream records with pause, resume, buffering, and export.
- Inspect consumer-group lag, members, and offsets, with controlled offset reset.
- Manage topics, ACLs, and related configuration with read-only and delete safeguards.
- PLAINTEXT, TLS, SASL/PLAIN, SCRAM, Kerberos/GSSAPI, and OAUTHBEARER support.
- Confluent-compatible and AWS Glue Schema Registry support.
- ZooKeeper broker discovery, JSON/CSV export, and MCP automation interfaces.
- Simplified Chinese, Traditional Chinese, English, Spanish, Italian, Japanese,
  and Portuguese UI.

A Kafka-specific workspace screenshot will be added when the stable host UI capture
set is prepared; the current image shows the DBX plugin center and unified install entry point.

## MCP automation

Start standalone stdio mode with:

```bash
backend/bin/dbx-plugin-kafka --mcp
```

Kafka MCP is read-only by default. Useful tools include `kafka_messages_digest`,
`kafka_cursor_next`, `kafka_messages_produce`, and consumer-group offset tools.
Producing requires `readOnly: false`; deletion and clearing also require
`allowDelete: true`. See the [MCP guide](../docs/MCP_USAGE.en.md) and the
[Kafka MCP reference](docs/MCP.zh-CN.md).

## Security

SASL, TLS, Kerberos, AWS, and Schema Registry credentials are managed through DBX
host secret bindings and are not persisted by the plugin. For production clusters,
prefer TLS, read-only mode, and least-privilege ACLs; require confirmation for
topic, consumer-group, and ACL deletion operations.

## Development

```bash
cd frontend && pnpm install && pnpm typecheck && pnpm test && pnpm build
cd ../backend && go vet ./... && go test ./...
cd ..
scripts/test.sh
```

Protocol, Schema Registry, and integration details live under `docs/`. Contributors
should read the [workspace contribution guide](../CONTRIBUTING.md) first.
