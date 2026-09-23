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
#     version fields excluded) matches the last successful build
#   - install: skipped when the source is unchanged and the exact version is
#     already in the target store; REBUILT plugins reinstall the same version
#     (the store version dir is purged first via install.sh --reinstall)
# Versions are NEVER bumped by this script: manifest.json is the single version
# source (shared/RELEASE_PROCESS.zh-CN.md) — bump it by hand only when cutting
# a market release.
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
#   scripts/release-local.sh --no-gc           # skip the post-release workspace
#                                               #   garbage collection (Rust build
#                                               #   caches, .mimosa agent sessions,
#                                               #   old logs, old dist packages,
#                                               #   old plugin-store versions,
#                                               #   docker smoke leftovers);
#                                               #   scripts/gc.sh can also be run
#                                               #   standalone
#   scripts/release-local.sh --plan            # print what changed and what would
#                                               #   build/install — no side effects
# Plugins and host are git submodules; plugin fingerprints are computed inside
# each submodule repo (a superproject git ls-files only sees the gitlink).
# Fingerprints/logs live in .release-state/ (gitignored) — delete it to reset;
# also needed once after the 2026-09-18 submodule migration (fingerprint basis
# changed: first run rebuilds all plugins) and once after the 2026-09-19
# no-bump change (stored fingerprints were post-bump: first run rebuilds all
# plugins, then "unchanged" skipping converges again).
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
# kafka (Go, like ldap) rides the same generic loop: plugin_fingerprint's ver
# hash is guarded for Go plugins (no Cargo.toml/lock), and the build/install
# steps only assume <plugin>/scripts/build.sh + manifest.json.
PLUGINS=(ssh ldap files kafka)
STATE_DIR="$ROOT/.release-state"
mkdir -p "$STATE_DIR/logs"
T0="$(date +%s)"

SYNC_HOST=0; SKIP_HOST=0; SKIP_PLUGINS=0; FORCE_HOST=0; FORCE_PLUGINS=0
NO_LAUNCH=0; REINSTALL_PLUGINS=0; MERGE_POLICY=manual; NO_GC=0; PLAN=0
PLUGIN_SEL=()
while [ $# -gt 0 ]; do
  case "$1" in
    --sync-host) SYNC_HOST=1 ;;
    --skip-pull) ;; # legacy alias: pulling is opt-in now
    --skip-host) SKIP_HOST=1 ;;
    --skip-plugins) SKIP_PLUGINS=1 ;;
    --force-host) FORCE_HOST=1 ;;
    --force-plugins) FORCE_PLUGINS=1 ;;
    --no-bump) ;; # legacy no-op: versions are never bumped by this script
    --no-launch) NO_LAUNCH=1 ;;
    --no-gc) NO_GC=1 ;;
    --plan) PLAN=1 ;;
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

# Toolchain paths (nvm node, pnpm, cargo) — same source of truth as plugin build.sh.
export PATH="$HOME/.nvm/versions/node/v22.21.0/bin:$HOME/Library/pnpm:$HOME/.cargo/bin:$PATH"
# Background builds have no TTY: pnpm refuses to rebuild a modules dir that was
# installed by a different pnpm version unless it can proceed non-interactively.
export CI=true
# Host Cargo.lock (aws-* crates) requires rustc >= 1.94.1; default toolchain is 1.88.0.
export RUSTUP_TOOLCHAIN="${RUSTUP_TOOLCHAIN:-1.97.1}"
# Deliberately NO RUSTC_WRAPPER/sccache here: it crashed russh compilation
# under 3-way parallel builds, and enabling/disabling it invalidates every
# cargo fingerprint (one full dep-tree rebuild per toggle). Warm reuse comes
# from the persistent CARGO_TARGET_DIR in the plugin build scripts instead.

log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

# ---------------------------------------------------------------------------
# Fingerprints (submodule-aware). Each plugin is its own git repo, so the file
# list MUST come from inside the plugin submodule: a superproject
# `git ls-files -- ssh` only sees the gitlink, which collapsed the fingerprint
# to a constant and would skip every future change. plugin = (rest, ver, head).
# rest covers every tracked + untracked source file except docs/, .worktrees/
# and the version-bearing files; ver covers exactly those; head is the plugin
# repo HEAD. A successful build stores the post-build fingerprint, so "no edits
# since" reproduces it verbatim on the next run and skips the rebuild.
plugin_rest_files() { # <plugin> → plugin-repo-relative source paths on stdout
  (cd "$ROOT/$1" && git ls-files -co --exclude-standard \
    | awk '
      index($0, "docs/") == 1 { next }
      index($0, ".worktrees/") == 1 { next }
      $0 == "manifest.json" { next }
      $0 == "backend/Cargo.toml" { next }
      $0 == "backend/Cargo.lock" { next }
      { print }')
}
hash_files() { # <base-dir>; repo-relative paths on stdin → one sha256
  local base="$1"
  while IFS= read -r f; do
    if [ -f "$base/$f" ]; then shasum -a 256 "$base/$f"; else printf 'MISSING %s\n' "$f"; fi
  done | shasum -a 256 | cut -d' ' -f1
}
plugin_fingerprint() { # <plugin> → "rest ver head"
  local rest ver head
  rest="$(plugin_rest_files "$1" | hash_files "$ROOT/$1")"
  # '|| true': Go plugins have no Cargo.toml/lock, so cat always fails there —
  # without the guard pipefail + set -e killed the caller and left .fp files
  # empty (which made every later run rebuild those plugins unconditionally).
  ver="$(cd "$ROOT/$1" && cat manifest.json backend/Cargo.toml backend/Cargo.lock 2>/dev/null \
    | shasum -a 256 | cut -d' ' -f1 || true)"
  head="$(git -C "$ROOT/$1" rev-parse HEAD 2>/dev/null || echo none)"
  printf '%s %s %s\n' "$rest" "$ver" "$head"
}
host_fingerprint() {
  # Hash dirty-file CONTENTS too: a porcelain-only list can't see edits to
  # already-modified files, which silently skipped host rebuilds after
  # in-place frontend fixes (the "no effect" bug of 2026-09-05).
  local status diff untracked
  status="$(git -C "$HOST_WORKTREE" status --porcelain | shasum -a 256 | cut -d' ' -f1)"
  diff="$(git -C "$HOST_WORKTREE" diff HEAD | shasum -a 256 | cut -d' ' -f1)"
  untracked="$(git -C "$HOST_WORKTREE" ls-files --others --exclude-standard -z \
    | xargs -0 -n1 -I{} shasum -a 256 -- "$HOST_WORKTREE/{}" 2>/dev/null \
    | shasum -a 256 | cut -d' ' -f1)"
  printf '%s %s\n' \
    "$(git -C "$HOST_WORKTREE" rev-parse HEAD)" \
    "$(printf '%s\n%s\n%s\n' "$status" "$diff" "$untracked" | shasum -a 256 | cut -d' ' -f1)"
}

was_rebuilt() { # <plugin> → 0 if it is in this run's TO_BUILD set
  local B
  [ "${#TO_BUILD[@]}" -gt 0 ] || return 1
  for B in "${TO_BUILD[@]}"; do
    [ "$B" = "$1" ] && return 0
  done
  return 1
}

# ---------------------------------------------------------------------------
# --plan: print what changed and what this run would build/install, then exit.
# No bump, no build, no publish, no install — safe to run at any time.
plan_only() {
  echo "release plan (no side effects)"
  if [ "$SKIP_HOST" = 1 ]; then
    echo "  host:   skip (--skip-host)"
  elif [ "$FORCE_HOST" = 1 ]; then
    echo "  host:   rebuild + publish (--force-host)"
  else
    local HF SF dirty
    HF="$(host_fingerprint)"
    SF="$(cat "$STATE_DIR/host.fp" 2>/dev/null || true)"
    dirty="$(git -C "$HOST_WORKTREE" status --porcelain | wc -l | tr -d ' ')"
    if [ -n "$SF" ] && [ "$HF" = "$SF" ]; then
      echo "  host:   unchanged since last build — skip"
    else
      echo "  host:   changed — rebuild + publish (HEAD $(git -C "$HOST_WORKTREE" rev-parse --short HEAD), $dirty dirty files)"
    fi
  fi
  local P FP STORED have_dist
  PLAN_REBUILT=""
  for P in "${PLUGINS[@]}"; do
    FP="$(plugin_fingerprint "$P")"
    STORED="$(cat "$STATE_DIR/$P.fp" 2>/dev/null || true)"
    have_dist=0; ls "$ROOT/$P"/dist/*.dbxp >/dev/null 2>&1 && have_dist=1
    local dirty_n
    dirty_n="$(git -C "$ROOT/$P" status --porcelain | wc -l | tr -d ' ')"
    local detail="HEAD $(git -C "$ROOT/$P" rev-parse --short HEAD 2>/dev/null || echo '?'), $dirty_n uncommitted files"
    if [ "$SKIP_PLUGINS" = 1 ]; then
      echo "  $P: skip build (--skip-plugins), $detail"
    elif [ "$FORCE_PLUGINS" = 1 ]; then
      PLAN_REBUILT="$PLAN_REBUILT $P"
      echo "  $P: rebuild (--force-plugins), $detail"
    elif [ -n "$STORED" ] && [ "$FP" = "$STORED" ] && [ "$have_dist" = 1 ]; then
      echo "  $P: unchanged since last build — skip, $detail"
    else
      PLAN_REBUILT="$PLAN_REBUILT $P"
      [ "$have_dist" = 1 ] || detail="$detail, no dist/*.dbxp"
      echo "  $P: changed — build, $detail"
    fi
    # install plan (same skip rule as step 5)
    local ID_VER P_ID P_VER
    ID_VER="$(python3 - "$ROOT/$P/manifest.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
print(d["id"], d["version"])
PY
)"
    P_ID="${ID_VER% *}"; P_VER="${ID_VER#* }"
    if [ "$REINSTALL_PLUGINS" = 1 ]; then
      echo "  $P: reinstall v$P_VER (--reinstall-plugins)"
    elif [ -d "$APP_DATA/plugins/$P_ID/versions/$P_VER" ]; then
      case " $PLAN_REBUILT " in
        *" $P "*)
          echo "  $P: rebuild — reinstall v$P_VER (store dir purged first)" ;;
        *)
          echo "  $P: v$P_VER already in store, source unchanged — install skipped" ;;
      esac
    else
      echo "  $P: install v$P_VER -> $APP_DATA/plugins"
    fi
  done
  exit 0
}
[ "$PLAN" = 1 ] && plan_only

# ---------------------------------------------------------------------------
log "step 1/6: host worktree ($HOST_WORKTREE)"
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
log "step 2/6: plan plugin builds (parallel, skip unchanged)"
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
log "step 3/6: build host DBX.app (debug bundle, skip if unchanged)"
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
log "step 4/6: wait for plugin builds"
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
  # Store post-build fingerprints only after every build succeeded.
  for P in "${TO_BUILD[@]}"; do plugin_fingerprint "$P" > "$STATE_DIR/$P.fp"; done
fi

# ---------------------------------------------------------------------------
log "step 5/6: install plugins -> $APP_DATA/plugins"
cd "$ROOT"
for P in "${PLUGINS[@]}"; do
  ID_VER="$(python3 - "$ROOT/$P/manifest.json" <<'PY'
import json, sys
d = json.load(open(sys.argv[1]))
print(d["id"], d["version"])
PY
)"
  P_ID="${ID_VER% *}"; P_VER="${ID_VER#* }"
  if [ -d "$APP_DATA/plugins/$P_ID/versions/$P_VER" ] \
    && ! was_rebuilt "$P" && [ "$REINSTALL_PLUGINS" = 0 ]; then
    echo "  $P: v$P_VER already in store, source unchanged — skip install (--reinstall-plugins to force)"
    continue
  fi
  INSTALL_ARGS=(--app-data "$APP_DATA" --no-restart)
  if [ -d "$APP_DATA/plugins/$P_ID/versions/$P_VER" ]; then
    # Versions are no longer bumped locally, so a rebuilt plugin keeps its
    # version and the host store installer rejects "already installed" —
    # purge the old version dir + activation records first via --reinstall.
    INSTALL_ARGS+=(--reinstall)
  fi
  shared/host-e2e/install.sh "$ROOT/$P" "${INSTALL_ARGS[@]}"
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
if [ "$NO_GC" = 0 ]; then
  log "step 6/6: garbage collection"
  "$ROOT/scripts/gc.sh" || echo "WARN: garbage collection failed — release is still complete" >&2
else
  echo "skip garbage collection (--no-gc)"
fi

# ---------------------------------------------------------------------------
if [ "$NO_LAUNCH" = 0 ]; then
  log "launch DBX.app"
  open "$INSTALLED_APP"
fi
echo
echo "done in $(( $(date +%s) - T0 ))s."
