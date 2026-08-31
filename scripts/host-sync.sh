#!/usr/bin/env bash
# Track the upstream DBX host from the host/ submodule.
#
# The submodule is our fork-style checkout of t8y2/dbx: branch
# dev/plugin-framework-current carries a handful of local (二开) commits on top
# of origin/main. Upstream stays authoritative — we merge origin/main
# regularly and keep the local patches until upstream covers the need.
#
# Usage:
#   scripts/host-sync.sh                 # status: ahead/behind vs upstream
#   scripts/host-sync.sh status          # same as above
#   scripts/host-sync.sh init            # (re)populate the host/ submodule
#   scripts/host-sync.sh fetch           # fetch upstream refs
#   scripts/host-sync.sh sync            # fetch + merge origin/main (and
#                                        #   origin/dev/plugin-framework-current)
#   scripts/host-sync.sh sync [--merge-policy=ours]
#                                        # on conflict: stop with the merge left
#                                        #   in progress for human resolution
#                                        #   (default: manual — no edits are
#                                        #   dropped). ours explicitly opts into a
#                                        #   -X ours retry: main-side edits in
#                                        #   conflicted hunks are DROPPED.
#
# NOTE: local commits are not pushed anywhere; the submodule pointer in this
# repo records them. Never run `git submodule update --force/--checkout` in
# host/ — it would move the checkout onto a fetchable upstream commit and
# strand the local patches on a branch.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOST_DIR="$ROOT/host"
UPSTREAM_MAIN="origin/main"
UPSTREAM_BRANCH="origin/dev/plugin-framework-current"
MERGE_POLICY=manual

log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

CMD=status
while [ $# -gt 0 ]; do
  case "$1" in
    status|init|fetch|sync) CMD="$1" ;;
    --merge-policy=*) MERGE_POLICY="${1#--merge-policy=}" ;;
    *) echo "unknown argument: $1" >&2; exit 2 ;;
  esac
  shift
done
case "$MERGE_POLICY" in ours|manual) ;; *) echo "unknown merge policy: $MERGE_POLICY (ours|manual)" >&2; exit 2 ;; esac

# In-repo submodule preferred; fall back to a legacy sibling checkout so the
# script keeps working before the submodule is initialized.
if [ ! -d "$HOST_DIR/.git" ]; then
  for candidate in "$ROOT/../dbx-plugin-host-worktree" "$HOME/btroot/dbx-plugin-host-worktree"; do
    if [ -d "$candidate/.git" ]; then HOST_DIR="$candidate"; break; fi
  done
fi

host_present() { [ -d "$HOST_DIR/.git" ]; }

init_submodule() {
  if host_present; then
    echo "host checkout already present: $HOST_DIR"
    return 0
  fi
  log "initializing host/ submodule"
  (cd "$ROOT" && git submodule update --init host)
  echo
  echo "next: scripts/host-sync.sh sync   # merge latest upstream main"
}

fetch_upstream() {
  log "fetching upstream refs in $HOST_DIR"
  # Explicit refspecs: remote.origin.fetch may not cover main, and a bare
  # `git fetch origin main` only writes FETCH_HEAD (leaves origin/main stale).
  (cd "$HOST_DIR" && git fetch origin \
    main:refs/remotes/origin/main \
    dev/plugin-framework-current:refs/remotes/origin/dev/plugin-framework-current)
}

show_status() {
  host_present || { echo "host checkout not found — run: scripts/host-sync.sh init" >&2; exit 1; }
  cd "$HOST_DIR"
  BRANCH="$(git rev-parse --abbrev-ref HEAD)"
  HEAD_SHA="$(git rev-parse --short HEAD)"
  echo "host: $HOST_DIR"
  echo "branch: $BRANCH @ $HEAD_SHA"
  for REF in "$UPSTREAM_MAIN" "$UPSTREAM_BRANCH"; do
    BEHIND="$(git rev-list --count "HEAD..$REF" 2>/dev/null || echo '?')"
    AHEAD="$(git rev-list --count "$REF..HEAD" 2>/dev/null || echo '?')"
    TIP="$(git log -1 --format='%h %cs' "$REF" 2>/dev/null || echo '?')"
    echo "vs $REF ($TIP): ahead $AHEAD, behind $BEHIND"
  done
  echo
  echo "local (二开) commits on top of $UPSTREAM_MAIN:"
  git log --no-merges --format='  %h %an  %s' "$UPSTREAM_MAIN..HEAD" || true
  if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
    echo
    echo "WARNING: host checkout has uncommitted changes — commit them first" >&2
    exit 1
  fi
}

merge_ref() {
  local REF="$1"
  local BEHIND
  BEHIND="$(git rev-list --count "HEAD..$REF" 2>/dev/null || echo 0)"
  if [ "$BEHIND" = "0" ]; then
    echo "up to date vs $REF"
    return 0
  fi
  echo "merging $REF ($BEHIND behind) — local patches are kept"
  if git merge --no-edit "$REF"; then
    return 0
  fi
  if ! git diff --name-only --diff-filter=U | grep -q .; then
    echo "merge failed for $REF without content conflicts — resolve manually" >&2
    exit 1
  fi
  if [ "$MERGE_POLICY" = "ours" ]; then
    echo "merge conflicts with $REF — retrying with -X ours (local patches win):"
    git diff --name-only --diff-filter=U | sed 's/^/    ! /'
    echo "    caution: main-side edits inside those hunks are dropped by the auto-merge;"
    echo "    skim the merge commit if a conflicted file carries features you need."
    git merge --abort
    git merge -X ours --no-edit "$REF" || {
      echo "auto-merge with -X ours failed for $REF — resolve manually" >&2; exit 1; }
    return 0
  fi
  echo "merge conflicts with $REF — stopping, merge left in progress:"
  git diff --name-only --diff-filter=U | sed 's/^/    ! /'
  echo
  echo "main-side changes at stake in those files (vs merge base):"
  git diff --name-only -z --diff-filter=U | xargs -0 git diff --stat "HEAD...$REF" --
  echo
  echo "resolve each conflicted hunk so BOTH sides' intent survives (local 二开 +"
  echo "upstream feature), then finish the merge and verify before building:"
  echo "    git add <resolved files> && git commit    # completes the merge"
  echo "    cargo check -p dbx --lib --bins --tests   # --tests catches dangling test imports"
  exit 1
}

sync_upstream() {
  host_present || { echo "host checkout not found — run: scripts/host-sync.sh init" >&2; exit 1; }
  cd "$HOST_DIR"
  if [ -n "$(git status --porcelain --untracked-files=no)" ]; then
    echo "host checkout dirty — commit or stash before sync" >&2
    exit 1
  fi
  fetch_upstream
  merge_ref "$UPSTREAM_MAIN"
  merge_ref "$UPSTREAM_BRANCH"
  echo
  echo "synced. submodule pointer changed — commit it in the plugins repo:"
  echo "  (cd $ROOT && git add host && git commit -m 'chore(host): track upstream <date>')"
}

case "$CMD" in
  init) init_submodule ;;
  fetch) host_present || { echo "host checkout not found — run: scripts/host-sync.sh init" >&2; exit 1; }
         (cd "$HOST_DIR" && fetch_upstream) ;;
  sync) sync_upstream ;;
  *) show_status ;;
esac
