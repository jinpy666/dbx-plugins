#!/usr/bin/env bash
# Build the Kafka plugin frontend and package a .dbxp for the current platform.
# Frontend three-step (typecheck/test/build) is owned by this path; the Go
# backend + manifest.json are owned by the backend path — packaging is
# attempted only when manifest.json exists.
set -euo pipefail
cd "$(dirname "$0")/.."

if ! command -v pnpm >/dev/null; then
  export PATH="$HOME/Library/pnpm:$HOME/.nvm/versions/node/v22.21.0/bin:$PATH"
fi

echo "==> frontend: install + typecheck + test + build"
if [ ! -d frontend/node_modules ]; then
  pnpm --dir frontend install
fi
pnpm --dir frontend typecheck
pnpm --dir frontend test
pnpm --dir frontend build

echo "==> package .dbxp"
unset DBX_PLUGIN_SDK_ROOT
if ! command -v dbx-plugin >/dev/null 2>&1; then
  HOST="${DBX_HOST_WORKTREE:-$PWD/../dbx-plugin-host-worktree}"
  if [ ! -x "$HOST/plugins/sdk/cli/target/release/dbx-plugin" ]; then
    (cd "$HOST/plugins/sdk/cli" && cargo build --release)
  fi
  export PATH="$HOST/plugins/sdk/cli/target/release:$PATH"
fi

if [ ! -f manifest.json ]; then
  echo "SKIP: manifest.json not present yet (backend path owns it); frontend artifacts are in ui/"
  exit 0
fi

# Identity version comes from manifest.json — the single source of truth — so
# the sidecar's reported identity always matches the manifest (the host rejects
# mismatches at init: "backend identity ... does not match manifest").
PLUGIN_VERSION="$(sed -n 's/.*"version": *"\([^"]*\)".*/\1/p' manifest.json | head -1)"
PLUGIN_VERSION="${PLUGIN_VERSION:-0.0.0-dev}"

# Build the Go sidecar first when the backend workspace is present.
# backend/bin is for local smoke/debug; the packaging CLI rebuilds the sidecar
# itself (build_go_backend runs plain `go build`, no ldflags support), so the
# same version is injected into that rebuild via GOFLAGS below.
if [ -f backend/go.mod ] && command -v go >/dev/null 2>&1; then
  echo "==> backend: go build (owned by backend path; failures are recorded in docs/PROGRESS-B-KAFKA.zh-CN.md)"
  (cd backend && CGO_ENABLED=0 go build -trimpath -ldflags "-s -w -X main.version=${PLUGIN_VERSION}" -o bin/dbx-plugin-kafka .) || {
    echo "WARN: go build failed (backend under parallel development); packaging will fail until it is green"
  }
fi

# The npm CLI wrapper injects DBX_PLUGIN_SDK_ROOT (bundled SDK ships a go.work
# pinned to go 1.22, which breaks modules requiring >=1.24). Call the native
# binary directly without SDK_ROOT so the local Go toolchain is used.
CLI_PKG="$(npm root -g 2>/dev/null)/@dbx-app/plugin-cli"
NATIVE_CLI="$CLI_PKG/node_modules/@dbx-app/plugin-cli-darwin-arm64/bin/dbx-plugin"
if [ -x "$NATIVE_CLI" ]; then
  env -u DBX_PLUGIN_SDK_ROOT NO_COLOR=1 GOFLAGS="-ldflags=-X=main.version=${PLUGIN_VERSION}" "$NATIVE_CLI" package .
else
  env -u DBX_PLUGIN_SDK_ROOT NO_COLOR=1 GOFLAGS="-ldflags=-X=main.version=${PLUGIN_VERSION}" dbx-plugin package .
fi

echo
echo "Artifacts:"
ls -la dist/*.dbxp dist/*.artifact.json 2>/dev/null || true
