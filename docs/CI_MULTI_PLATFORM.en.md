# Multi-platform CI and release output

This repository uses the official DBX plugin CLI and reusable workflow contract
to build native plugin candidates. Native sidecars are built on the matching
GitHub Actions runner instead of pretending that a cross-compiled binary is a
different target. One exception applies to the Linux C runtime baseline: a
build runner's glibc may only be newer than the runtime environment, never
older, so Linux targets link through cargo-zigbuild with a pinned low glibc
baseline (see "Linux glibc baseline" below).

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

## Linux glibc baseline (critical for web/docker installs)

The official DBX web docker image runs `debian:bookworm-slim` (glibc 2.36). Rust
sidecars built natively on ubuntu-24.04 (glibc 2.39) pin `GLIBC_2.38`/`GLIBC_2.39`
symbols (`__isoc23_sscanf`, `pidfd_spawnp`, …), so the dynamic loader fails
before `main()` and the host reports
`Plugin 'io.dbx.ssh' initialization failed: exited with status 1`
(dbx-plugin-ssh#8, #58; the files plugin shares the root cause). To prevent
this, the CI sets up Linux runners as follows:

1. Install `ziglang` (pip) and `cargo-zigbuild`, then place a `cargo` wrapper at
   the front of PATH: the bare `cargo build` invoked by the packaging CLI is
   rerouted to `cargo zigbuild --target <host-triple>` (zig links, pinning a low
   glibc baseline — 2.30 observed), and the artifacts are mirrored from
   `<triple>/release/` back to `<CARGO_TARGET_DIR>/release/` where the CLI
   copies from. The explicit `--target` is REQUIRED: without it cargo-zigbuild
   falls back to the distro toolchain and the baseline follows the runner (a
   no-op on ubuntu-24.04 — verified end-to-end, it produced bookworm-level
   2.34 instead of zig's 2.30). The Rust code still compiles natively
   per-architecture — this is not a disguised cross-target build. The plugins'
   `scripts/build.sh` therefore only prepends `~/.cargo/bin` to PATH
   conditionally, so the wrapper is not shadowed.
2. Export `CGO_ENABLED=0` so the Go sidecars (ldap/kafka) built by the CLI's
   internal `go build` stay statically linked (a cgo build pins `GLIBC_2.34+`),
   matching how the deploy/web image packages them.

`shared/release/validate_artifact_set.py` unpacks every linux candidate and
requires the ELF sidecars' highest GLIBC symbol version to be ≤ 2.31 (with
margin; statically linked Go binaries have no dynamic version requirements and
pass through). Raising the baseline must be a deliberate decision.

Reproduce the build locally (arm64 example):

```bash
pip3 install ziglang && cargo install cargo-zigbuild
rustup target add aarch64-unknown-linux-gnu
cd ssh/backend && cargo zigbuild --release --target aarch64-unknown-linux-gnu
# Run the binary inside a debian:bookworm-slim container; it should enter the
# stdio main loop instead of failing in the loader
```

The four `release-*.yml` workflows continue to call DBX's official
`t8y2/dbx/.github/workflows/plugin-release-reusable.yml@plugin-sdk-v1` workflow.
That workflow uses the same five-target matrix and publishes candidates to a
GitHub Release for review.

Candidates are unsigned artifacts. Signing keys must never be placed in this
repository's build jobs; publishing the GitHub Release lets the DBX Store sync
and sign automatically.

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
