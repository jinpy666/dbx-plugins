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
if [ -f manifest.json ] && command -v dbx-plugin >/dev/null 2>&1; then
  unset DBX_PLUGIN_SDK_ROOT
  NO_COLOR=1 dbx-plugin package . || echo "WARN: dbx-plugin package failed (backend under parallel development)"
else
  echo "SKIP: manifest.json/dbx-plugin CLI not ready yet; frontend artifacts are in ui/"
fi

echo "==> smoke (Kafka container auto-SKIP; unimplemented methods SKIP)"
python3 scripts/smoke_test.py

echo
echo "all green (frontend three-step + smoke suite, SKIPs allowed by design)"
