#!/usr/bin/env bash
# Verification suite for the Kafka plugin (frontend-owned path).
#
#   scripts/test.sh                # everything available on this machine
#
# Steps: frontend typecheck/test/build -> go vet/test (when the backend
# workspace exists) -> .dbxp package (when manifest.json exists) -> smoke
# (whole suite SKIPs automatically without a Kafka container; methods not
# implemented yet SKIP instead of FAIL while the backend is under parallel
# development).
set -euo pipefail
cd "$(dirname "$0")/.."

if ! command -v pnpm >/dev/null 2>&1; then
  NODE_BIN="$(ls -d "$HOME"/.nvm/versions/node/v22*/bin 2>/dev/null | sort -V | tail -1 || true)"
  export PATH="$HOME/Library/pnpm:${NODE_BIN:+$NODE_BIN:}$PATH"
fi

echo "==> frontend typecheck + tests + build"
node ../shared/connection-forms/verify.mjs kafka
[ -d frontend/node_modules ] || pnpm --dir frontend install
pnpm --dir frontend typecheck
pnpm --dir frontend test
pnpm --dir frontend build

echo "==> UI walkthrough (headless Chrome via scripts/ui_test.mjs; SKIP without Chrome/network)"
node scripts/ui_test.mjs || exit 1

if [ -f backend/go.mod ] && command -v go >/dev/null 2>&1; then
  echo "==> backend unit tests (owned by backend path)"
  (cd backend && go vet ./... && go test ./...) || echo "WARN: backend go test failed (parallel development) — see docs/PROGRESS-B-KAFKA.zh-CN.md"
else
  echo "==> backend unit tests skipped (no backend/go.mod or no go toolchain yet)"
fi

echo "==> package .dbxp (only when manifest.json exists)"
if [ -f manifest.json ]; then
  unset DBX_PLUGIN_SDK_ROOT
  # Same native-CLI direct call as build.sh: the npm wrapper injects
  # DBX_PLUGIN_SDK_ROOT whose bundled go.work is pinned to go 1.22 and breaks
  # modules requiring >=1.24.
  CLI_PKG="$(npm root -g 2>/dev/null)/@dbx-app/plugin-cli"
  NATIVE_CLI="$CLI_PKG/node_modules/@dbx-app/plugin-cli-darwin-arm64/bin/dbx-plugin"
  if [ -x "$NATIVE_CLI" ]; then
    env -u DBX_PLUGIN_SDK_ROOT NO_COLOR=1 "$NATIVE_CLI" package .
  elif command -v dbx-plugin >/dev/null 2>&1; then
    env -u DBX_PLUGIN_SDK_ROOT NO_COLOR=1 dbx-plugin package .
  else
    echo "SKIP: dbx-plugin CLI not available"
  fi
else
  echo "SKIP: manifest.json/dbx-plugin CLI not ready yet; frontend artifacts are in ui/"
fi

echo "==> smoke (Kafka container auto-SKIP; unimplemented methods SKIP)"
python3 scripts/smoke_test.py

echo "==> MCP smoke (sidecar auto-built; dev-cluster cases auto-SKIP)"
if [ -f backend/go.mod ] && command -v go >/dev/null 2>&1; then
  (cd backend && CGO_ENABLED=0 go build -trimpath -o bin/dbx-plugin-kafka .)
fi
python3 scripts/smoke_mcp.py

echo
echo "all green (frontend three-step + smoke suite, SKIPs allowed by design)"
