#!/usr/bin/env bash
# One-click local release for the dbx-plugins workspace.
#
# Pull latest host (main + plugin-framework branches) → build host DBX.app
# (debug bundle) → copy to /Applications → build all plugins → install the
# latest .dbxp of each into the app's plugin store → launch DBX.
#
# Usage:
#   scripts/release-local.sh                    # full pipeline
#   scripts/release-local.sh --skip-host        # reuse existing /Applications/DBX.app
#   scripts/release-local.sh --skip-plugins     # reuse existing dist/*.dbxp
#   scripts/release-local.sh --no-bump          # do not bump plugin versions
#   scripts/release-local.sh --no-launch        # do not open DBX.app at the end
#   scripts/release-local.sh --isolated         # install into shared/host-e2e/app-data
#                                               #   instead of the real app data
#   scripts/release-local.sh --merge-policy=manual
#                                               # on merge conflict: fail and let the
#                                               # human resolve (default: ours — retry
#                                               # the merge with -X ours so conflicting
#                                               # hunks keep local patches; main-side
#                                               # edits in those hunks are dropped and
#                                               # only reported in the log)
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOST_WORKTREE="${DBX_HOST_WORKTREE:-$ROOT/../dbx-plugin-host-worktree}"
APP_DATA="${DBX_APP_DATA:-$HOME/Library/Application Support/com.dbx.app}"
INSTALLED_APP="/Applications/DBX.app"
PLUGINS=(ssh ldap files)

SKIP_HOST=0; SKIP_PLUGINS=0; SKIP_PULL=0; SKIP_BUMP=0; NO_LAUNCH=0; MERGE_POLICY=ours
while [ $# -gt 0 ]; do
  case "$1" in
    --skip-host) SKIP_HOST=1 ;;
    --skip-plugins) SKIP_PLUGINS=1 ;;
    --skip-pull) SKIP_PULL=1 ;;
    --no-bump) SKIP_BUMP=1 ;;
    --no-launch) NO_LAUNCH=1 ;;
    --isolated) APP_DATA="$ROOT/shared/host-e2e/app-data" ;;
    --merge-policy=*) MERGE_POLICY="${1#--merge-policy=}" ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done
case "$MERGE_POLICY" in ours|manual) ;; *) echo "unknown merge policy: $MERGE_POLICY (ours|manual)" >&2; exit 2 ;; esac

# 每次发版版本号递增（patch +1）：manifest.json 必改；Rust 插件同步 Cargo.toml
# 并再生 Cargo.lock（否则 CLI 以 --locked 构建时因本地包版本过期而失败）。
bump_version() {
  python3 - "$1" <<'PY'
import json, pathlib, re, sys
d = pathlib.Path(sys.argv[1])
m = d / "manifest.json"
data = json.loads(m.read_text())
parts = data["version"].split(".")
parts[-1] = str(int(parts[-1]) + 1)
version = ".".join(parts)
data["version"] = version
m.write_text(json.dumps(data, indent=2, ensure_ascii=False) + "\n")
cargo = d / "backend" / "Cargo.toml"
if cargo.exists():
    text = cargo.read_text()
    cargo.write_text(re.sub(r'(?m)^version = ".*"$', f'version = "{version}"', text, count=1))
print(f"  {d.name}: {data['version']}")
PY
  local cargo="$1/backend/Cargo.toml"
  if [ -f "$cargo" ]; then
    local pkg
    pkg="$(sed -n 's/^name = "\(.*\)"/\1/p' "$cargo" | head -1)"
    (cd "$1/backend" && cargo update -p "$pkg" --offline >/dev/null)
  fi
}

# Toolchain paths (nvm node, pnpm, cargo) — same source of truth as plugin build.sh.
export PATH="$HOME/.nvm/versions/node/v22.21.0/bin:$HOME/Library/pnpm:$HOME/.cargo/bin:$PATH"
# Host Cargo.lock (aws-* crates) requires rustc >= 1.94.1; default toolchain is 1.88.0.
export RUSTUP_TOOLCHAIN="${RUSTUP_TOOLCHAIN:-1.97.1}"

log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

# ---------------------------------------------------------------------------
log "step 1/5: sync host worktree ($HOST_WORKTREE)"
cd "$HOST_WORKTREE"
[ -d .git ] || { echo "host worktree not found: $HOST_WORKTREE" >&2; exit 1; }
if [ "$SKIP_PULL" = 0 ]; then
  # Fetch with explicit refspecs: remote.origin.fetch in the host worktree may
  # not cover main, and a bare `git fetch origin main` then only writes
  # FETCH_HEAD, leaving origin/main stale so the behind-check below always
  # reports "up to date" and the merge never fires.
  git fetch origin \
    main:refs/remotes/origin/main \
    dev/plugin-framework-current:refs/remotes/origin/dev/plugin-framework-current
  for BRANCH in origin/main origin/dev/plugin-framework-current; do
    BEHIND="$(git rev-list --count HEAD.."$BRANCH" 2>/dev/null || echo 0)"
    TIP="$(git log -1 --format='%h %cs' "$BRANCH" 2>/dev/null || echo '?')"
    if [ "$BEHIND" != "0" ]; then
      echo "local HEAD is $BEHIND behind $BRANCH ($TIP) — merging (local patches are kept)"
      [ -z "$(git status --porcelain --untracked-files=no)" ] || {
        echo "host worktree dirty, cannot merge — commit/stash first" >&2; exit 1; }
      git merge "$BRANCH" || {
        if [ "$MERGE_POLICY" = "ours" ] && git diff --name-only --diff-filter=U | grep -q .; then
          CONFLICTS="$(git diff --name-only --diff-filter=U | sed 's/^/    ! /')"
          git merge --abort
          echo "merge conflicts with $BRANCH — retrying with -X ours (local patches win):"
          printf '%s\n' "$CONFLICTS"
          echo "    caution: main-side edits inside those hunks are dropped by the auto-merge;"
          echo "    skim the merge commit if a conflicted file carries features you need."
          git merge -X ours --no-edit "$BRANCH" || {
            echo "auto-merge with -X ours failed for $BRANCH — resolve manually" >&2; exit 1; }
        else
          echo "merge conflict with $BRANCH — resolve manually" >&2; exit 1
        fi
      }
    else
      echo "up to date vs $BRANCH ($TIP)"
    fi
  done
fi

# ---------------------------------------------------------------------------
log "step 2/5: build host DBX.app (debug bundle)"
HOST_APP="$HOST_WORKTREE/target/debug/bundle/macos/DBX.app"
if [ "$SKIP_HOST" = 1 ] && [ -d "$INSTALLED_APP" ]; then
  echo "skip host build (--skip-host)"
elif [ ! -d "$HOST_APP" ] || [ "$SKIP_HOST" = 0 ]; then
  # --bundles app + config override: skip dmg/updater artifacts — updater
  # signing needs TAURI_SIGNING_PRIVATE_KEY (release infra, not needed locally).
  (cd "$HOST_WORKTREE" && pnpm exec tauri build --debug --bundles app \
    --config '{"bundle":{"createUpdaterArtifacts":false}}')
fi
if [ ! -d "$HOST_APP" ]; then
  if [ "$SKIP_HOST" = 1 ] && [ -d "$INSTALLED_APP" ]; then
    echo "no worktree bundle — will reuse installed app"
  else
    echo "host build produced no DBX.app at $HOST_APP" >&2
    exit 1
  fi
fi

log "step 3/5: publish DBX.app -> $INSTALLED_APP"
RUNNING="$(pgrep -x DBX || true)"
if [ ! -d "$HOST_APP" ] && [ -d "$INSTALLED_APP" ]; then
  echo "reuse installed app (--skip-host): $INSTALLED_APP"
elif [ ! -d "$HOST_APP" ]; then
  echo "host build produced no DBX.app at $HOST_APP and none installed" >&2
  exit 1
else
if [ -n "$RUNNING" ]; then
  echo "quitting running DBX (pid $RUNNING)"
  osascript -e 'quit app "DBX"' 2>/dev/null || kill $RUNNING || true
  sleep 2
fi
if [ -d "$INSTALLED_APP" ]; then
  rm -rf "$INSTALLED_APP" || sudo rm -rf "$INSTALLED_APP"
fi
if ! ditto "$HOST_APP" "$INSTALLED_APP" 2>/dev/null; then
  sudo ditto "$HOST_APP" "$INSTALLED_APP"
fi
echo "published: $INSTALLED_APP"
# Single-copy policy: /Applications/DBX.app is the one dev app (plugins live in
# the shared app-data, so this copy is fully featured). Drop the worktree
# bundle and stale updater/dmg artifacts so Spotlight never sees a second —
# plugin-less-looking — copy. --skip-host already falls back to $INSTALLED_APP.
rm -rf "$HOST_APP"
rm -f "$HOST_WORKTREE"/target/debug/bundle/macos/*.tar.gz
rm -rf "$HOST_WORKTREE/target/debug/bundle/dmg"
fi

# ---------------------------------------------------------------------------
if [ "$SKIP_PLUGINS" = 0 ]; then
  log "step 4/5: build plugins (latest workspace code)"
  if [ "$SKIP_BUMP" = 0 ]; then
    echo "bump versions:"
    for P in "${PLUGINS[@]}"; do bump_version "$ROOT/$P"; done
  fi
  for P in "${PLUGINS[@]}"; do
    echo "---- $P ----"
    (cd "$ROOT/$P" && scripts/build.sh)
  done
else
  log "step 4/5: skip plugin build (--skip-plugins)"
fi

# ---------------------------------------------------------------------------
log "step 5/5: install plugins -> $APP_DATA/plugins"
cd "$ROOT"
for P in "${PLUGINS[@]}"; do
  shared/host-e2e/install.sh "$ROOT/$P" --app-data "$APP_DATA" --no-restart
done
python3 - "$APP_DATA/plugins" <<'PY'
import json, pathlib, sys
store = pathlib.Path(sys.argv[1])
print("\nInstalled plugin store:")
for pid in sorted(p.name for p in store.iterdir() if p.is_dir()):
    versions = sorted((store / pid / "versions").glob("*")) if (store / pid / "versions").is_dir() else []
    print(f"  {pid}: {[v.name for v in versions]}")
PY

# ---------------------------------------------------------------------------
if [ "$NO_LAUNCH" = 0 ]; then
  log "launch DBX.app"
  open "$INSTALLED_APP"
fi
echo
echo "done."
