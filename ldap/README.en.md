# DBX LDAP

[中文](README.md) · [Workspace contribution guide](../CONTRIBUTING.md)

DBX LDAP is a directory browsing and administration workspace. It is designed
for directory searches, user and group maintenance, schema inspection, and
controlled exports, with clear read-only and write-scope protections.

![DBX LDAP workspace](docs/screenshots/01-workbench-initial.png)

## Use cases

- Find users, groups, organizational units, and service accounts quickly.
- Inspect directory schema, attribute definitions, and RootDSE capabilities.
- Maintain entries inside approved Base DN scopes and export audit or delivery data.

## Highlights

- DN tree browsing, paged searches, and RFC 4515 filters.
- View, create, edit, rename, and delete directory entries.
- RootDSE, schema, and attribute metadata inspection.
- Saved search presets with LDIF and CSV export.
- Anonymous, unauthenticated, simple, Kerberos/GSSAPI, NTLM, NTLM hash,
  DIGEST-MD5, and SASL External authentication.
- LDAP, StartTLS, and LDAPS with configurable certificate validation, CA path,
  and server name.
- Read-only mode, allowed read/write Base DN scopes, blocked attributes, and audit controls.
- Simplified Chinese, Traditional Chinese, English, Spanish, Italian, Japanese,
  and Portuguese UI.

![LDAP schema inspection](docs/screenshots/05-schema-panel.png)

## MCP automation

Start standalone stdio mode with:

```bash
backend/bin/dbx-plugin-ldap --mcp
```

Useful tools include `ldap_search_digest`, `ldap_cursor_next`, `ldap_ui_schema`,
and `ldap_entry_write`. Page large results with cursors; entry deletion requires
two-phase confirmation. See the [MCP guide](../docs/MCP_USAGE.en.md) and the
[LDAP MCP reference](docs/MCP.zh-CN.md).

## Security

Bind passwords, Kerberos credentials, and other sensitive values are managed by
DBX host secret bindings and are not persisted by the plugin. For production
connections, enable TLS verification, read-only mode, and the narrowest practical
read/write Base DN scopes.

## Development

```bash
cd frontend && pnpm install && pnpm typecheck && pnpm test && pnpm build
cd ../backend && go vet ./... && go test ./...
cd ..
scripts/test.sh
```

Protocol and integration details live under `docs/`. Contributors should read
the [workspace contribution guide](../CONTRIBUTING.md) first.
