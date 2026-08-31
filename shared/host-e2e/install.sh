#!/usr/bin/env bash
# Host end-to-end installer for the dbx-plugins workspace (ssh / ldap / files).
# Wraps the official PluginPackageInstaller from the plugin-framework host worktree
# (checksum + compatibility verified), same semantics as ssh scripts/install.sh
# but plugin-agnostic and with an isolated default app-data so the user's real
# DBX data directory is never touched.
#
# Usage:
#   shared/host-e2e/install.sh <plugin-dir>            # install into isolated app-data
#   shared/host-e2e/install.sh <plugin-dir> --app-data <dir>
#   shared/host-e2e/install.sh <plugin-dir> --reinstall
#   shared/host-e2e/install.sh <plugin-dir> --no-restart
#
# Environment:
#   DBX_HOST_WORKTREE   host checkout used for the installer binary
#                       (default: sibling dbx-plugin-host-worktree)
#   DBX_TEST_APP        DBX.app bundle to relaunch (default: host worktree debug bundle)
set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
WORKSPACE_ROOT="$(cd "$SCRIPT_DIR/../.." && pwd)"

PLUGIN_DIR=""
APP_DATA="${DBX_E2E_APP_DATA:-$SCRIPT_DIR/app-data}"
REINSTALL=0
RESTART=0
while [ $# -gt 0 ]; do
  case "$1" in
    --app-data) APP_DATA="$2"; shift 2 ;;
    --reinstall) REINSTALL=1; shift ;;
    --no-restart) RESTART=0; shift ;;
    -*) echo "unknown option: $1" >&2; exit 2 ;;
    *) PLUGIN_DIR="$1"; shift ;;
  esac
done
[ -n "$PLUGIN_DIR" ] || { echo "usage: install.sh <plugin-dir> [--app-data <dir>] [--reinstall] [--no-restart]" >&2; exit 2; }
cd "$PLUGIN_DIR"

DBXP="$(ls -t dist/*.dbxp 2>/dev/null | head -1 || true)"
[ -n "$DBXP" ] || { echo "no .dbxp in $PLUGIN_DIR/dist/ — build first" >&2; exit 1; }
PLUGIN_ID="$(python3 -c "import json;print(json.load(open('manifest.json'))['id'])")"
VERSION="$(python3 -c "import json;print(json.load(open('manifest.json'))['version'])")"

if [ -z "${DBX_HOST_WORKTREE:-}" ]; then
  for candidate in "$WORKSPACE_ROOT/../dbx-plugin-host-worktree" "$HOME/dbx-plugin-host-worktree"; do
    [ -d "$candidate" ] && DBX_HOST_WORKTREE="$candidate" && break
  done
fi
[ -n "${DBX_HOST_WORKTREE:-}" ] && [ -d "$DBX_HOST_WORKTREE" ] || {
  echo "DBX host worktree not found (set DBX_HOST_WORKTREE)" >&2
  exit 1
}
export PATH="$HOME/.cargo/bin:$PATH"

INSTALLER="$DBX_HOST_WORKTREE/target/release/examples/install_plugin"
build_installer() {
  echo "==> building PluginPackageInstaller example"
  rm -f "$INSTALLER"   # build-cache pitfall: stale installer rejects new manifest fields
  (cd "$DBX_HOST_WORKTREE" && cargo build -p dbx-core --example install_plugin --release)
}
if [ ! -x "$INSTALLER" ]; then
  build_installer
fi

# Pick the first existing host bundle and read its version in a clean
# substitution: PlistBuddy prints "File Doesn't Exist, Will Create: ..." on
# stdout even when it fails, so `cmd1 || cmd2` inside $() would concatenate
# that error text with the real version.
APP_PLIST=""
for candidate in \
  "$DBX_HOST_WORKTREE/target/debug/bundle/macos/DBX.app/Contents/Info.plist" \
  "/Applications/DBX.app/Contents/Info.plist"; do
  [ -f "$candidate" ] && APP_PLIST="$candidate" && break
done
APP_VERSION="0.6.0"
if [ -n "$APP_PLIST" ]; then
  APP_VERSION="$(/usr/libexec/PlistBuddy -c 'Print :CFBundleShortVersionString' "$APP_PLIST")"
fi

# Do not restart/kill anything by default: the isolated app-data is meant for a
# manually launched test DBX.app; killing the user's real DBX would be rude.
PLUGIN_STORE="$APP_DATA/plugins"
if [ "$REINSTALL" = 1 ]; then
  VERSION_DIR="$PLUGIN_STORE/$PLUGIN_ID/versions/$VERSION"
  if [ -d "$VERSION_DIR" ]; then
    echo "==> dev reinstall: removing installed v$VERSION"
    rm -rf "$VERSION_DIR"
    python3 - "$PLUGIN_STORE/$PLUGIN_ID/activations" "$VERSION" <<'PY'
import json, pathlib, sys
store = pathlib.Path(sys.argv[1])
version = sys.argv[2]
if store.is_dir():
    for record in store.glob("*.json"):
        try:
            if json.load(open(record)).get("version") == version:
                record.unlink()
        except Exception:
            pass
PY
  fi
fi

echo "==> installing $(basename "$DBXP") (v$VERSION) into $PLUGIN_STORE"
if ! "$INSTALLER" "$PLUGIN_STORE" "$DBXP" "$APP_VERSION"; then
  echo "==> installer failed — rebuilding installer (cache pitfall) and retrying once" >&2
  build_installer
  "$INSTALLER" "$PLUGIN_STORE" "$DBXP" "$APP_VERSION"
fi

if [ "$RESTART" = 1 ]; then
  TEST_APP="${DBX_TEST_APP:-$DBX_HOST_WORKTREE/target/debug/bundle/macos/DBX.app}"
  [ -d "$TEST_APP" ] || TEST_APP="/Applications/DBX.app"
  echo "==> launching test DBX.app: $TEST_APP"
  open "$TEST_APP"
fi
echo "installed $PLUGIN_ID v$VERSION -> $PLUGIN_STORE"
