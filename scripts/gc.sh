#!/usr/bin/env bash
# Workspace garbage collection for dbx-plugins.
#
# Removes only regenerable artifacts — never source, fingerprints, or dist/:
#   - Rust build caches: dormant whole targets (untouched > GC_STALE_DAYS),
#     incremental caches, and stale deps/build entries older than GC_STALE_DAYS
#     (cargo leaves old copies behind after rustc/feature changes; they are
#     never referenced by the current fingerprints)
#   - AI agent session state: .mimosa/hook-state sessions older than
#     GC_SESSION_DAYS (each session = ~600KB JSON + a ~59MB baseline snapshot),
#     in the workspace root and inside every plugin
#   - logs: .release-state/ and shared/host-e2e/ logs older than GC_LOG_DAYS
#   - __pycache__ dirs, and the trimmed docker host context in /tmp
#
# Usage:
#   scripts/gc.sh              # collect
#   scripts/gc.sh --dry-run    # list what would be freed, remove nothing
#
# Env: GC_STALE_DAYS=7  GC_SESSION_DAYS=3  GC_LOG_DAYS=14
# Called automatically by release-local.sh after a successful release
# (opt-out with --no-gc); safe to run standalone at any time.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
STALE_DAYS="${GC_STALE_DAYS:-7}"
SESSION_DAYS="${GC_SESSION_DAYS:-3}"
LOG_DAYS="${GC_LOG_DAYS:-14}"
DRY_RUN=0
[ "${1:-}" = "--dry-run" ] && DRY_RUN=1

# Thresholds flow into `find -mtime +N`; reject anything that is not a plain
# integer so a stray env value cannot change what the deletion globs match.
for v in "$STALE_DAYS" "$SESSION_DAYS" "$LOG_DAYS"; do
  case "$v" in ''|*[!0-9]*) echo "gc: non-numeric threshold: $v" >&2; exit 2 ;; esac
done

log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }
FREED_KB=0

# rm_path <path>: record size and remove (or just record in --dry-run).
rm_path() {
  [ -e "$1" ] || return 0
  local kb
  kb="$(du -sk "$1" 2>/dev/null | cut -f1)"
  FREED_KB=$((FREED_KB + kb))
  if [ "$DRY_RUN" = 1 ]; then
    printf '  would remove %8sK  %s\n' "$kb" "${1#"$ROOT"/}"
  else
    rm -rf "$1"
  fi
}

# Cargo may be mid-build; touching target/ then would corrupt the running
# build. Skip the cache section instead of racing it.
CARGO_ACTIVE=0
if pgrep -x cargo >/dev/null 2>&1 || pgrep -x rustc >/dev/null 2>&1; then
  CARGO_ACTIVE=1
  echo "cargo/rustc is running — skipping Rust build-cache cleanup this run"
fi

# ---------------------------------------------------------------------------
log "Rust build caches (stale > ${STALE_DAYS}d)"
if [ "$CARGO_ACTIVE" = 0 ]; then
  # Host target is hot (dev builds run near-daily): never remove it wholesale —
  # only the incremental caches and per-entry stale sweep. Dormant plugin
  # targets are removed whole; their next build recompiles from scratch.
  HOST_TARGET="$ROOT/host/target"
  if [ -d "$HOST_TARGET" ]; then
    for sub in debug release; do
      rm_path "$HOST_TARGET/$sub/incremental"
    done
    for sub in deps build; do
      [ -d "$HOST_TARGET/$sub" ] || continue
      # shellcheck disable=SC2046  # entries are repo-relative, no spaces expected
      while IFS= read -r -d '' entry; do rm_path "$entry"; done < \
        <(find "$HOST_TARGET/$sub" -mindepth 1 -maxdepth 1 -mtime +"$STALE_DAYS" -print0)
    done
  fi
  for t in "$ROOT"/*/backend/target; do
    [ -d "$t" ] || continue
    # No file touched within the window → target is dormant: drop it whole.
    if ! find "$t" -type f -mtime -"$STALE_DAYS" -print -quit | grep -q .; then
      rm_path "$t"
    else
      for sub in debug release; do
        rm_path "$t/$sub/incremental"
      done
    fi
  done
fi

# ---------------------------------------------------------------------------
log "AI agent session state (.mimosa hook-state > ${SESSION_DAYS}d)"
for m in "$ROOT/.mimosa/hook-state" "$ROOT"/*/.mimosa/hook-state; do
  [ -d "$m" ] || continue
  while IFS= read -r -d '' entry; do rm_path "$entry"; done < \
    <(find "$m" -mindepth 1 -maxdepth 1 -mtime +"$SESSION_DAYS" -print0)
done

# ---------------------------------------------------------------------------
log "logs (older than ${LOG_DAYS}d) and pycache"
find "$ROOT/.release-state/logs" "$ROOT/shared/host-e2e/logs" \
  -type f -mtime +"$LOG_DAYS" -delete 2>/dev/null || true
find "$ROOT" -type d -name __pycache__ \
  -not -path "*/node_modules/*" -not -path "*/target/*" -not -path "*/vendor/*" \
  -not -path "*/host/*" -exec rm -rf {} + 2>/dev/null || true

# ---------------------------------------------------------------------------
log "docker build leftovers"
# Trimmed host context left by release-docker.sh (default path matches its
# DBX_DOCKER_HOST_CONTEXT resolution; pass the env through to override).
if [ "${DBX_DOCKER_HOST_CONTEXT:-/tmp/dbx-docker-host}" = /tmp/dbx-docker-host ]; then
  [ -d /tmp/dbx-docker-host ] && [ -n "$(find /tmp/dbx-docker-host -mtime +"$STALE_DAYS" -print -quit)" ] \
    && rm_path /tmp/dbx-docker-host || true
fi

printf 'gc done: freed %s' "$(awk -v k="$FREED_KB" 'BEGIN {printf "%.1fG", k/1048576}')"
# NB: `${DRY_RUN:+...}` would also fire on the value "0" — test it explicitly.
[ "$DRY_RUN" = 1 ] && printf ' (dry run)'
echo
