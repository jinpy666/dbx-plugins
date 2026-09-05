#!/usr/bin/env bash
# One-click local release for the dbx-plugins workspace.
#
# Sync host upstream (opt-in) → plan plugin builds → build host DBX.app and
# plugins in parallel → publish to /Applications → install changed plugins
# into the app's plugin store → launch DBX.
#
# Change detection keeps repeat releases fast:
#   - host: skipped when HEAD + worktree state match the last successful build
#   - plugins: skipped when the source fingerprint (tracked + untracked files,
#     version fields excluded) matches the last successful build; version is
#     NOT bumped for skipped plugins
#   - install: skipped when the exact version is already in the target store
#
# Usage:
#   scripts/release-local.sh                    # release only what changed
#   scripts/release-local.sh ssh                # limit the release to one plugin
#   scripts/release-local.sh ssh files          # ... or any subset (host still
#                                               #   follows its own skip logic)
#   scripts/release-local.sh --sync-host        # fetch/merge host upstream first
#                                               #   (manual/periodic; NOT default)
#   scripts/release-local.sh --skip-pull        # no-op (pull is opt-in now)
#   scripts/release-local.sh --skip-host        # never build/publish host,
#                                               #   reuse /Applications/DBX.app
#   scripts/release-local.sh --force-host       # rebuild host even if unchanged
#   scripts/release-local.sh --skip-plugins     # reuse existing dist/*.dbxp
#   scripts/release-local.sh --force-plugins    # rebuild all plugins
#   scripts/release-local.sh --no-bump          # do not bump plugin versions
#   scripts/release-local.sh --no-launch        # do not open DBX.app at the end
#   scripts/release-local.sh --isolated         # install into shared/host-e2e/app-data
#                                               #   instead of the real app data
#   scripts/release-local.sh --reinstall-plugins
#                                               # force reinstall even when the
#                                               #   version is already in the store
#   scripts/release-local.sh --merge-policy=ours
#                                               # on merge conflict (--sync-host):
#                                               #   retry with -X ours (main-side
#                                               #   edits in conflicted hunks are
#                                               #   DROPPED). Default manual: stop
#                                               #   and leave the merge in progress.
# Fingerprints/logs live in .release-state/ (gitignored) — delete it to reset.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# Host checkout resolution: DBX_HOST_WORKTREE env → in-repo host/ submodule →
# legacy sibling dbx-plugin-host-worktree.
HOST_WORKTREE="${DBX_HOST_WORKTREE:-}"
if [ -z "$HOST_WORKTREE" ]; then
  for candidate in "$ROOT/host" "$ROOT/../dbx-plugin-host-worktree"; do
    if [ -d "$candidate/.git" ]; then HOST_WORKTREE="$candidate"; break; fi
  done
fi
[ -n "$HOST_WORKTREE" ] && [ -d "$HOST_WORKTREE/.git" ] || {
  echo "host checkout not found — init the host/ submodule (scripts/host-sync.sh init) or set DBX_HOST_WORKTREE" >&2
  exit 1
}
APP_DATA="${DBX_APP_DATA:-$HOME/Library/Application Support/com.dbx.app}"
INSTALLED_APP="/Applications/DBX.app"
# kafka (Go, like ldap) rides the same generic loop: bump_version's Cargo.toml
# handling and plugin_fingerprint's ver hash are guarded for Go plugins, and
# the build/install steps only assume <plugin>/scripts/build.sh + manifest.json.
PLUGINS=(ssh ldap files kafka)
STATE_DIR="$ROOT/.release-state"
mkdir -p "$STATE_DIR/logs"
T0="$(date +%s)"

SYNC_HOST=0; SKIP_HOST=0; SKIP_PLUGINS=0; FORCE_HOST=0; FORCE_PLUGINS=0
SKIP_BUMP=0; NO_LAUNCH=0; REINSTALL_PLUGINS=0; MERGE_POLICY=manual
PLUGIN_SEL=()
while [ $# -gt 0 ]; do
  case "$1" in
    --sync-host) SYNC_HOST=1 ;;
    --skip-pull) ;; # legacy alias: pulling is opt-in now
    --skip-host) SKIP_HOST=1 ;;
    --skip-plugins) SKIP_PLUGINS=1 ;;
    --force-host) FORCE_HOST=1 ;;
    --force-plugins) FORCE_PLUGINS=1 ;;
    --no-bump) SKIP_BUMP=1 ;;
    --no-launch) NO_LAUNCH=1 ;;
    --isolated) APP_DATA="$ROOT/shared/host-e2e/app-data" ;;
    --reinstall-plugins) REINSTALL_PLUGINS=1 ;;
    --merge-policy=*) MERGE_POLICY="${1#--merge-policy=}" ;;
    -*) echo "unknown option: $1" >&2; exit 2 ;;
    *) PLUGIN_SEL+=("$1") ;;
  esac
  shift
done
# Positional args limit the pipeline to a plugin subset (build, install, store
# listing); fingerprints/skip logic apply within the subset as usual.
if [ "${#PLUGIN_SEL[@]}" -gt 0 ]; then
  SEL=""
  for S in "${PLUGIN_SEL[@]}"; do
    case " ${PLUGINS[*]} " in
      *" $S "*) ;;
      *) echo "unknown plugin: $S (valid: ${PLUGINS[*]})" >&2; exit 2 ;;
    esac
    case " $SEL " in *" $S "*) ;; *) SEL="$SEL $S" ;; esac
  done
  # word-splitting is safe: names come from the validated PLUGINS set
  # shellcheck disable=SC2206
  PLUGINS=($SEL)
fi
case "$MERGE_POLICY" in ours|manual) ;; *) echo "unknown merge policy: $MERGE_POLICY (ours|manual)" >&2; exit 2 ;; esac

# 每次构建版本号递增（patch +1）：manifest.json 必改；Rust 插件同步 Cargo.toml
# 并再生 Cargo.lock（否则 CLI 以 --locked 构建时因本地包版本过期而失败）。
# 只对真正要构建的插件 bump；指纹未变的插件版本保持不变。
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
# Deliberately NO RUSTC_WRAPPER/sccache here: it crashed russh compilation
# under 3-way parallel builds, and enabling/disabling it invalidates every
# cargo fingerprint (one full dep-tree rebuild per toggle). Warm reuse comes
# from the persistent CARGO_TARGET_DIR in the plugin build scripts instead.

log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

# ---------------------------------------------------------------------------
# Fingerprints: plugin = (rest, ver). rest covers every tracked + untracked
# source file except docs/ and the version-bearing files; ver covers exactly
# those. A successful build stores the post-bump fingerprint, so "no edits
# since" reproduces it verbatim on the next run and skips bump + build.
plugin_rest_files() {
  (cd "$ROOT" && git ls-files -co --exclude-standard -- "$1" \
    | awk -v p="$1/" '
      index($0, p "docs/") == 1 { next }
      $0 == p "manifest.json" { next }
      $0 == p "backend/Cargo.toml" { next }
      $0 == p "backend/Cargo.lock" { next }
      { print }')
}
hash_files() { # paths (relative to ROOT) on stdin → one sha256
  while IFS= read -r f; do
    if [ -f "$ROOT/$f" ]; then shasum -a 256 "$ROOT/$f"; else printf 'MISSING %s\n' "$f"; fi
  done | shasum -a 256 | cut -d' ' -f1
}
plugin_fingerprint() {
  local rest ver
  rest="$(plugin_rest_files "$1" | hash_files)"
  # '|| true': Go plugins have no Cargo.toml/lock, so cat always fails there —
  # without the guard pipefail + set -e killed the caller and left .fp files
  # empty (which made every later run rebuild those plugins unconditionally).
  ver="$(cd "$ROOT/$1" && cat manifest.json backend/Cargo.toml backend/Cargo.lock 2>/dev/null \
    | shasum -a 256 | cut -d' ' -f1 || true)"
  printf '%s %s\n' "$rest" "$ver"
}
host_fingerprint() {
  printf '%s %s\n' \
    "$(git -C "$HOST_WORKTREE" rev-parse HEAD)" \
    "$(git -C "$HOST_WORKTREE" status --porcelain | shasum -a 256 | cut -d' ' -f1)"
}

# ---------------------------------------------------------------------------
log "step 1/5: host worktree ($HOST_WORKTREE)"
cd "$HOST_WORKTREE"
[ -d .git ] || { echo "host worktree not found: $HOST_WORKTREE" >&2; exit 1; }
if [ "$SYNC_HOST" = 1 ]; then
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
        if ! git diff --name-only --diff-filter=U | grep -q .; then
          echo "merge failed for $BRANCH without content conflicts — resolve manually" >&2; exit 1
        fi
        if [ "$MERGE_POLICY" = "ours" ]; then
          echo "merge conflicts with $BRANCH — retrying with -X ours (local patches win):"
          git diff --name-only --diff-filter=U | sed 's/^/    ! /'
          echo "    caution: main-side edits inside those hunks are dropped by the auto-merge;"
          echo "    skim the merge commit if a conflicted file carries features you need."
          git merge --abort
          git merge -X ours --no-edit "$BRANCH" || {
            echo "auto-merge with -X ours failed for $BRANCH — resolve manually" >&2; exit 1; }
        else
          echo "merge conflicts with $BRANCH — stopping, merge left in progress:"
          git diff --name-only --diff-filter=U | sed 's/^/    ! /'
          echo
          echo "main-side changes at stake in those files (vs merge base):"
          git diff --name-only -z --diff-filter=U | xargs -0 git diff --stat "HEAD...$BRANCH" --
          echo
          echo "resolve each conflicted hunk so BOTH sides' intent survives (local 二开 +"
          echo "upstream feature), finish the merge, verify, then rerun this script:"
          echo "    git add <resolved files> && git commit    # completes the merge"
          echo "    cargo check -p dbx --lib --bins --tests   # --tests catches dangling test imports"
          exit 1
        fi
      }
    else
      echo "up to date vs $BRANCH ($TIP)"
    fi
  done
else
  echo "skip upstream pull (default — pass --sync-host to fetch/merge origin main + plugin-framework,"
  echo "or run scripts/host-sync.sh sync periodically)"
fi

# ---------------------------------------------------------------------------
log "step 2/5: plan plugin builds (parallel, skip unchanged)"
TO_BUILD=()
if [ "$SKIP_PLUGINS" = 0 ]; then
  for P in "${PLUGINS[@]}"; do
    FP="$(plugin_fingerprint "$P")"
    STORED="$(cat "$STATE_DIR/$P.fp" 2>/dev/null || true)"
    if [ "$FORCE_PLUGINS" = 0 ] && [ -n "$STORED" ] && [ "$FP" = "$STORED" ] \
      && ls "$ROOT/$P"/dist/*.dbxp >/dev/null 2>&1; then
      echo "  $P: unchanged since last build — skip (--force-plugins to rebuild)"
    else
      TO_BUILD+=("$P")
    fi
  done
  if [ "${#TO_BUILD[@]}" -gt 0 ] && [ "$SKIP_BUMP" = 0 ]; then
    echo "bump versions:"
    for P in "${TO_BUILD[@]}"; do bump_version "$ROOT/$P"; done
  fi
  # Parallel plugin builds share the dbx-plugin CLI; make sure it exists ONCE
  # before the jobs race for it (build.sh rebuilds it only when absent).
  if ! command -v dbx-plugin >/dev/null 2>&1; then
    export PATH="$HOST_WORKTREE/plugins/sdk/cli/target/release:$PATH"
    if [ ! -x "$HOST_WORKTREE/plugins/sdk/cli/target/release/dbx-plugin" ]; then
      echo "pre-warming dbx-plugin CLI (shared by parallel builds)"
      (cd "$HOST_WORKTREE/plugins/sdk/cli" && cargo build --release)
    fi
  fi
  if [ "${#TO_BUILD[@]}" -gt 0 ]; then
    for P in "${TO_BUILD[@]}"; do
      echo "  $P: building in background (log: $STATE_DIR/logs/$P.log)"
    done
  fi
else
  log "  skip plugin build (--skip-plugins)"
fi
BUILD_NAMES=(); BUILD_PIDS=(); BUILD_STARTS=()
if [ "${#TO_BUILD[@]}" -gt 0 ]; then
  for P in "${TO_BUILD[@]}"; do
    (cd "$ROOT/$P" && exec scripts/build.sh) >"$STATE_DIR/logs/$P.log" 2>&1 &
    BUILD_NAMES+=("$P"); BUILD_PIDS+=("$!"); BUILD_STARTS+=("$(date +%s)")
  done
fi

# ---------------------------------------------------------------------------
log "step 3/5: build host DBX.app (debug bundle, skip if unchanged)"
HOST_APP="$HOST_WORKTREE/target/debug/bundle/macos/DBX.app"
HOST_FP="$(host_fingerprint)"
STORED_HOST_FP="$(cat "$STATE_DIR/host.fp" 2>/dev/null || true)"
NEED_HOST=1
if [ "$SKIP_HOST" = 1 ] && [ -d "$INSTALLED_APP" ]; then
  NEED_HOST=0; echo "skip host build+publish (--skip-host)"
elif [ "$FORCE_HOST" = 0 ] && [ -d "$INSTALLED_APP" ] && [ -n "$STORED_HOST_FP" ] \
  && [ "$HOST_FP" = "$STORED_HOST_FP" ]; then
  NEED_HOST=0; echo "host unchanged since last release — skip build+publish (--force-host to rebuild)"
fi
if [ "$NEED_HOST" = 0 ]; then
  echo "reuse installed app: $INSTALLED_APP"
elif [ "$SKIP_HOST" = 1 ]; then
  echo "host build produced no --skip-host fallback: $INSTALLED_APP missing" >&2
  exit 1
else
  # --bundles app + config override: skip dmg/updater artifacts — updater
  # signing needs TAURI_SIGNING_PRIVATE_KEY (release infra, not needed locally).
  (cd "$HOST_WORKTREE" && pnpm exec tauri build --debug --bundles app \
    --config '{"bundle":{"createUpdaterArtifacts":false}}')
  [ -d "$HOST_APP" ] || { echo "host build produced no DBX.app at $HOST_APP" >&2; exit 1; }

  # publish DBX.app -> $INSTALLED_APP
  # Upstream renamed the binary to lowercase `dbx`; match case-insensitively or
  # the quit/kill below silently misses the running app.
  RUNNING="$(pgrep -ix DBX || true)"
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
  # plugin-less-looking — copy. Unchanged-HEAD runs skip this whole block.
  rm -rf "$HOST_APP"
  rm -f "$HOST_WORKTREE"/target/debug/bundle/macos/*.tar.gz
  rm -rf "$HOST_WORKTREE/target/debug/bundle/dmg"
  printf '%s\n' "$HOST_FP" > "$STATE_DIR/host.fp"
fi

# ---------------------------------------------------------------------------
log "step 4/5: wait for plugin builds"
if [ "${#BUILD_PIDS[@]}" -eq 0 ]; then
  echo "nothing to wait for"
else
  FAIL=0
  for i in "${!BUILD_PIDS[@]}"; do
    P="${BUILD_NAMES[$i]}"
    if wait "${BUILD_PIDS[$i]}"; then
      echo "  $P: OK ($(( $(date +%s) - ${BUILD_STARTS[$i]} ))s)"
    else
      echo "  $P: FAILED after $(( $(date +%s) - ${BUILD_STARTS[$i]} ))s — tail of $STATE_DIR/logs/$P.log:" >&2
      tail -30 "$STATE_DIR/logs/$P.log" >&2 || true
      FAIL=1
    fi
  done
  [ "$FAIL" = 0 ] || { echo "plugin build(s) failed" >&2; exit 1; }
  # Store post-bump fingerprints only after every build succeeded.
  for P in "${TO_BUILD[@]}"; do plugin_fingerprint "$P" > "$STATE_DIR/$P.fp"; done
fi

# ---------------------------------------------------------------------------
log "step 5/5: install plugins -> $APP_DATA/plugins"
cd "$ROOT"
for P in "${PLUGINS[@]}"; do
  ID_VER="$(python3 - "$ROOT/$P/manifest.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
print(d["id"], d["version"])
PY
)"
  P_ID="${ID_VER% *}"; P_VER="${ID_VER#* }"
  if [ "$REINSTALL_PLUGINS" = 0 ] && [ -d "$APP_DATA/plugins/$P_ID/versions/$P_VER" ]; then
    echo "  $P: v$P_VER already in store — skip install (--reinstall-plugins to force)"
  else
    shared/host-e2e/install.sh "$ROOT/$P" --app-data "$APP_DATA" --no-restart
  fi
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
echo "done in $(( $(date +%s) - T0 ))s."
