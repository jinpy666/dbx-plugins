# DBX Plugins

[中文](README.md) · [Contributing](CONTRIBUTING.md) ·
[Maintainer policy](MAINTAINERS.md) · [Security](SECURITY.md) ·
[MCP guide](docs/MCP_USAGE.en.md)

DBX Plugins provides focused workspaces for infrastructure, directory services,
file storage, and messaging platforms. Each plugin can be installed, configured,
upgraded, and released independently.

![DBX plugin center](shared/host-e2e/screenshots/02-plugin-center.png)

## Plugins

| Plugin | Use case | Highlights |
|---|---|---|
| [SSH & SFTP](ssh/README.en.md) | Server operations | Terminal, remote commands, SFTP, ProxyJump, Known Hosts, Quick Sudo |
| [LDAP](ldap/README.en.md) | Directory administration | DN tree, RFC 4515 search, entry editing, schema, LDIF/CSV export |
| [Files](files/README.en.md) | Files and object storage | Local, S3, OSS, WebDAV, FTP, SFTP, SMB, sync, large-file transfer |
| [Kafka](kafka/README.en.md) | Messaging operations | Topics, messages, consumer groups, lag, offsets, ACLs, Schema Registry |

All plugins support Simplified Chinese, Traditional Chinese, English, Spanish,
Italian, Japanese, and Portuguese. Connection credentials are managed through
DBX host secret bindings.

## Getting started

Install the DBX plugin CLI:

```bash
npm install --global @dbx-app/plugin-cli
```

Run checks for the plugin you are changing:

```bash
ssh/scripts/test.sh --skip-host
ldap/scripts/test.sh
files/scripts/test.sh
kafka/scripts/test.sh
```

Run workspace checks:

```bash
shared/test-all.sh --quick
python3 shared/release/check_repo_hygiene.py
```

Multi-platform builds are handled by [Build multi-platform plugin candidates](.github/workflows/build-candidates.yml),
covering four plugins × five native DBX targets and uploading `.dbxp`, `.artifact.json`,
and a merged candidate bundle. See [Multi-platform CI and release output](docs/CI_MULTI_PLATFORM.en.md).

## Repository layout

```text
ssh/       SSH and SFTP plugin
ldap/      LDAP plugin
files/     Multi-backend file storage plugin
kafka/     Kafka administration plugin
shared/    Shared host adapters, tests, and release checks
scripts/   Workspace maintenance and release scripts
host/      DBX host submodule
```

## Releases and security

The four plugins are independent release units using `<plugin>-v<version>` Git
tags. Author CI builds candidates; DBX Store performs review, signing, and
Marketplace catalog publication.

Credentials, private keys, connection exports, local app data, build artifacts,
and runtime state must not be committed. Report suspected vulnerabilities through
[SECURITY.md](SECURITY.md).

Before a public release, maintainers must confirm the license, copyright holder,
and publisher identity. See the [public release checklist](docs/REPO_PUBLISH.en.md).
