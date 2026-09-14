# Multi-platform CI and release output

This repository uses the official DBX plugin CLI and reusable workflow contract
to build native plugin candidates. Native sidecars are built on the matching
GitHub Actions runner instead of pretending that a cross-compiled binary is a
different target.

## Supported targets

| DBX target | Runner | Example |
|---|---|---|
| `darwin-arm64` | `macos-15` | Apple Silicon |
| `darwin-x64` | `macos-15-intel` | Intel macOS |
| `windows-x64` | `windows-2022` | Windows x64 |
| `linux-x64` | `ubuntu-24.04` | Linux x64 |
| `linux-arm64` | `ubuntu-24.04-arm` | Linux ARM64 |

Each plugin produces five unsigned candidates and matching `.artifact.json` metadata:

```text
io.dbx.<plugin>-<version>-<target>.dbxp
io.dbx.<plugin>-<version>-<target>.artifact.json
```

## CI workflows

`.github/workflows/build-candidates.yml` runs a 20-job matrix:

```text
4 plugins × 5 native targets = 20 builds
```

Each job installs the pinned `@dbx-app/plugin-cli`, sets Node.js, Go, and Rust,
sets `DBX_PLUGIN_TARGET`, runs `dbx-plugin package .`, and uploads the package and
metadata. A final verification job checks identity, target coverage, file size,
SHA-256, and unsigned-candidate status before uploading a merged bundle.

The four `release-*.yml` workflows continue to call DBX's official
`t8y2/dbx/.github/workflows/plugin-release-reusable.yml@plugin-sdk-v1` workflow.
That workflow uses the same five-target matrix and publishes candidates to a
GitHub Release for review.

Candidates are unsigned review artifacts. Signing keys must never be placed in
this repository's build jobs; DBX Store or a protected repository workflow signs
approved packages later.

## Reproduce locally

Run the package command on the matching host:

```bash
npm install --global @dbx-app/plugin-cli@0.1.3
export DBX_PLUGIN_TARGET=darwin-arm64  # replace for the current host
cd ssh
dbx-plugin package .
```

The official CLI rejects a native target that does not match the build host. This
is why CI uses native runners for each target.

## Official references

- [DBX plugin publishing](https://github.com/t8y2/dbx/blob/main/host/plugins/RELEASING.md)
- [DBX plugin CLI guide](https://github.com/t8y2/dbx/blob/main/host/plugins/sdk/cli/README.md)
- [GitHub Actions workflow syntax](https://docs.github.com/en/actions/using-workflows/workflow-syntax-for-github-actions)
- [GitHub Actions artifacts](https://docs.github.com/en/actions/using-workflows/storing-workflow-data-as-artifacts)
- [Rust platform support](https://doc.rust-lang.org/rustc/platform-support.html)
- [Go build environment](https://go.dev/doc/install/source#environment)
