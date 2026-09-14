# DBX plugin MCP guide

All four plugins expose MCP capabilities. Prefer the DBX MCP bridge for
production use: DBX owns saved connections, credentials, read-only policy, and
auditing. Use standalone sidecar stdio mode for local scripts, CI, diagnostics,
and development.

## Choose a connection mode

| Mode | Best for | Credential source | Recommendation |
|---|---|---|---|
| DBX MCP bridge | Production, saved connections, host policy | DBX saved connection | Recommended |
| Standalone stdio | Local scripts, CI, diagnostics, development | Inline per-call or environment | Development |

### DBX MCP bridge

1. Install and enable the target plugin in DBX.
2. Start the DBX MCP service, either the desktop service or `dbx mcp`.
3. Use `dbx_list_plugin_tools` to inspect installed plugin tools and schemas.
4. Call tools through `dbx_call_plugin_tool`, using the saved DBX `connectionId`.

The MCP client does not need access to passwords, private keys, or Kerberos credentials.

## Standalone stdio configuration

Build the target plugin first. The sidecar paths are:

```text
ssh/backend/target/release/dbx-plugin-ssh
files/backend/target/release/dbx-plugin-files
ldap/backend/bin/dbx-plugin-ldap
kafka/backend/bin/dbx-plugin-kafka
```

Add the following to a client that supports `mcp.servers`. Replace
`/absolute/path/to/dbx-plugins` with the absolute repository path. Do not put
passwords directly in the configuration file.

```json
{
  "mcp.servers": {
    "dbx-ssh": {
      "type": "stdio",
      "command": "/absolute/path/to/dbx-plugins/ssh/backend/target/release/dbx-plugin-ssh",
      "args": ["--mcp"],
      "env": { "DBX_SSH_MCP_READ_ONLY": "1" }
    },
    "dbx-files": {
      "type": "stdio",
      "command": "/absolute/path/to/dbx-plugins/files/backend/target/release/dbx-plugin-files",
      "args": ["--mcp"],
      "env": { "DBX_PLUGIN_DATA_DIR": "~/.dbx-plugin-data/io.dbx.files" }
    },
    "dbx-ldap": {
      "type": "stdio",
      "command": "/absolute/path/to/dbx-plugins/ldap/backend/bin/dbx-plugin-ldap",
      "args": ["--mcp"],
      "env": { "DBX_PLUGIN_DATA_DIR": "~/.dbx-plugin-data/io.dbx.ldap" }
    },
    "dbx-kafka": {
      "type": "stdio",
      "command": "/absolute/path/to/dbx-plugins/kafka/backend/bin/dbx-plugin-kafka",
      "args": ["--mcp"],
      "env": { "DBX_PLUGIN_DATA_DIR": "~/.dbx-plugin-data/io.dbx.kafka" }
    }
  }
}
```

Clients may call the outer field `mcp.servers`, `mcpServers`, or expose it in a
settings UI. The server fields keep the same meaning: `command`, `args`, and `env`.
Use absolute paths; on Windows, provide the `.exe` path.

Restart the MCP session after changing configuration or rebuilding a sidecar.
stdio processes do not hot-reload a new binary.

## First call

After connecting, an MCP client follows this sequence:

1. `initialize`
2. `notifications/initialized`
3. `tools/list`
4. `tools/call`

Start with a read-only discovery request:

```text
List the tools exposed by the current MCP servers and identify which ones are read-only.
```

## Plugin examples

### SSH & SFTP

Useful tools include `ssh_exec`, `ssh_metrics`, `sftp_list`, `sftp_upload`, and
`sftp_download`. For production, start standalone MCP with
`DBX_SSH_MCP_READ_ONLY=1`, or use the DBX bridge to inherit connection approvals
and sudo allowlists.

```text
Using SSH, list the 20 most recently modified files under /var/log/app. Read-only; do not write.
```

### Files

Useful tools include `files_scan_digest`, `files_cursor_next`, `files_write`,
`files_mkdir`, `files_rename`, and `files_delete`.

For local testing, use a credential-free connection:

```json
{
  "connection": { "protocol": "local", "root": "/tmp/dbx-files" },
  "path": "/",
  "format": "rows"
}
```

Use `files_scan_digest` first for large directories, then page with `files_cursor_next`.
Delete and purge operations require two-phase confirmation.

### LDAP

Useful tools include `ldap_search_digest`, `ldap_cursor_next`, `ldap_ui_schema`,
and `ldap_entry_write`. Narrow `baseDn`, `scope`, and projected attributes before
searching; use `format: "rows"` and cursors for large results. Entry and subtree
deletion require a `confirmToken` and the exact original parameters.

### Kafka

Useful tools include `kafka_messages_digest`, `kafka_cursor_next`,
`kafka_messages_produce`, `kafka_groups_offsets_reset`, and `kafka_topics_delete`.

Standalone Kafka MCP is read-only by default. Writes must explicitly opt in:

```json
{
  "brokers": ["127.0.0.1:9092"],
  "readOnly": false,
  "topic": "dbx-events",
  "value": "{\"event\":\"test\"}"
}
```

Topic deletion, record clearing, and consumer-group offset resets also require
`allowDelete: true` and two-phase confirmation. Standalone tools do not guess topic names.

## Security boundaries

- Inline standalone credentials are present in MCP tool arguments and client context;
  use them only in trusted local sessions.
- Prefer the DBX bridge in production so the host resolves connections and enforces policy.
- Deletion, clearing, offset reset, and entry deletion require explicit confirmation.
- If a response has `truncated: true`, narrow the query or use cursors.
- `*_ui_*` tools depend on the DBX workbench and may return `UNAVAILABLE` in standalone mode.

## Validate a configuration

```bash
cd ssh && python3 scripts/smoke_mcp.py
cd ../files && python3 scripts/smoke_mcp.py
cd ../ldap && python3 scripts/smoke_mcp.py
cd ../kafka && python3 scripts/smoke_mcp.py
```

See each plugin's `docs/MCP.zh-CN.md` for the complete tool reference and protocol details.
