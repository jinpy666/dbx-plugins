#!/usr/bin/env bash
# Track the upstream DBX host from the host/ submodule.
#
# Current model (2026-09): host/ main tracks upstream/main directly — the old
# dev/plugin-framework-current branch with local (二开) commits is retired, all
# family-needed features (visible_when/required_when, DBX_PLUGIN_DATA_DIR, …)
# have been absorbed upstream into crates/dbx-plugin-runtime. Upstream stays
# authoritative; PR-prep work lives on dedicated fix/* branches, never on main.
#
# Usage:
#   scripts/host-sync.sh                 # status: ahead/behind vs upstream
#   scripts/host-sync.sh status          # same as above
#   scripts/host-sync.sh init            # (re)populate the host/ submodule
#   scripts/host-sync.sh fetch           # fetch upstream refs
#   scripts/host-sync.sh sync            # fetch + fast-forward/merge upstream/main
#   scripts/host-sync.sh sync [--merge-policy=ours]
#                                        # on conflict: stop with the merge left
#                                        #   in progress for human resolution
#                                        #   (default: manual — no edits are
#                                        #   dropped). ours explicitly opts into a
#                                        #   -X ours retry: main-side edits in
#                                        #   conflicted hunks are DROPPED.
#   scripts/host-sync.sh contract        # snapshot the plugin-facing host
#                                        #   contract (bridge + sidecar SDK
#                                        #   shapes); run after every sync /
#                                        #   DBX.app update and align the
#                                        #   plugins (see AGENTS.md 宿主异动跟进)
#
# NOTE: never run `git submodule update --force/--checkout` in host/ — it would
# move the checkout onto a fetchable upstream commit and strand any un-pushed
# branch work.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOST_DIR="$ROOT/host"
UPSTREAM_REMOTE="upstream"
UPSTREAM_MAIN="upstream/main"
MERGE_POLICY=manual

log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

CMD=status
while [ $# -gt 0 ]; do
  case "$1" in
    status|init|fetch|sync|contract) CMD="$1" ;;
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
  (cd "$HOST_DIR" || exit 1
   # The submodule clones from .gitmodules (origin = fork). The authoritative
   # ref lives on the upstream remote; add it on first use.
   if ! git remote get-url "$UPSTREAM_REMOTE" >/dev/null 2>&1; then
     git remote add "$UPSTREAM_REMOTE" https://github.com/t8y2/dbx.git
   fi
   log "fetching $UPSTREAM_REMOTE refs in $HOST_DIR"
   # Explicit refspec: a bare `git fetch upstream main` only writes FETCH_HEAD
   # and leaves upstream/main stale.
   git fetch "$UPSTREAM_REMOTE" main:refs/remotes/upstream/main)
}

show_status() {
  host_present || { echo "host checkout not found — run: scripts/host-sync.sh init" >&2; exit 1; }
  cd "$HOST_DIR"
  BRANCH="$(git rev-parse --abbrev-ref HEAD)"
  HEAD_SHA="$(git rev-parse --short HEAD)"
  echo "host: $HOST_DIR"
  echo "branch: $BRANCH @ $HEAD_SHA"
  BEHIND="$(git rev-list --count "HEAD..$UPSTREAM_MAIN" 2>/dev/null || echo '?')"
  AHEAD="$(git rev-list --count "$UPSTREAM_MAIN..HEAD" 2>/dev/null || echo '?')"
  TIP="$(git log -1 --format='%h %cs' "$UPSTREAM_MAIN" 2>/dev/null || echo '?')"
  echo "vs $UPSTREAM_MAIN ($TIP): ahead $AHEAD, behind $BEHIND"
  echo
  echo "local commits on top of $UPSTREAM_MAIN (expect none on main — PR-prep lives on fix/* branches):"
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
  echo "merging $REF ($BEHIND behind)"
  if git merge --ff-only --no-edit "$REF" 2>/dev/null; then
    return 0
  fi
  # Local commits on main (should not happen — PR-prep belongs on branches):
  # fall back to a plain merge so nothing is dropped silently.
  if git merge --no-edit "$REF"; then
    echo "note: merged with a merge commit — main had local commits, consider rebasing PR-prep onto branches"
    return 0
  fi
  if ! git diff --name-only --diff-filter=U | grep -q .; then
    echo "merge failed for $REF without content conflicts — resolve manually" >&2
    exit 1
  fi
  if [ "$MERGE_POLICY" = "ours" ]; then
    echo "merge conflicts with $REF — retrying with -X ours (main-side wins):"
    git diff --name-only --diff-filter=U | sed 's/^/    ! /'
    echo "    caution: upstream edits inside those hunks are dropped by the auto-merge;"
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
  echo "resolve each conflicted hunk so BOTH sides' intent survives (local edit +"
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
  local PRE_SYNC_HEAD
  PRE_SYNC_HEAD="$(git rev-parse HEAD)"
  merge_ref "$UPSTREAM_MAIN"
  echo
  echo "synced. submodule pointer changed — commit it in the plugins repo:"
  echo "  (cd $ROOT && git add host && git commit -m 'chore(host): track upstream <date>')"
  echo
  contract_watch "$PRE_SYNC_HEAD"
}

# Plugin-facing contract surfaces: everything a plugin's frontend/backend is
# allowed to depend on. A change in any of these must be mirrored by all four
# plugins (consumers + mocks + types) before the synced host ships to users —
# upstream b15281024 silently re-shaped binary events and killed every plugin
# terminal/download stream against app 0.6.2.
#
# 2026-09-26: bc0043183 moved crates/dbx-core/src/plugins/host.rs into
# crates/dbx-plugin-runtime — the watch went blind on that path until this
# listing was fixed. The existence assertions below make that class of
# blindness fail loudly instead.
CONTRACT_PATHS=(
  "apps/desktop/src/lib/plugins/pluginHostBridge.ts"
  "apps/desktop/src/lib/plugins/pluginFieldConditions.ts"
  "apps/desktop/src/lib/plugins/pluginAppearance.ts"
  "apps/desktop/src/lib/plugins/pluginFilesystem.ts"
  "apps/desktop/src/lib/plugins/frontendPlugin.ts"
  "crates/dbx-plugin-runtime/src/plugins"
  "plugins/sdk/rust/dbx-plugin-sdk"
)

assert_contract_paths_exist() {
  local p
  for p in "${CONTRACT_PATHS[@]}"; do
    if ! git cat-file -e "HEAD:$p" 2>/dev/null; then
      echo "CONTRACT WATCH ERROR: watched path does not exist on HEAD: $p" >&2
      echo "  upstream likely moved/renamed it — update CONTRACT_PATHS in scripts/host-sync.sh," >&2
      echo "  otherwise contract changes stay invisible (bc0043183 did exactly this)." >&2
      exit 1
    fi
  done
}

contract_watch() {
  local BASE="${1:-}"
  cd "$HOST_DIR"
  log "host contract watch"
  assert_contract_paths_exist
  if [ -n "$BASE" ]; then
    local CHANGED
    CHANGED="$(git diff --stat "$BASE..HEAD" -- "${CONTRACT_PATHS[@]}" || true)"
    if [ -n "$CHANGED" ]; then
      echo "$CHANGED"
      echo
      echo "plugin-facing contract changed upstream — align the shared adapter layer before use:"
      echo "  1. binary 事件形状（forwardBinary + 沙箱 SDK 'binary' 分支；data vs dataBase64）"
      echo "     → 单点修 shared/frontend/ 公共适配层，插件引用处自然生效（勿各插件抄一份）"
      echo "  2. 沙箱 API 面（invoke/notify/sendBinary/fileTransfer/workbenchState/decodeBase64）"
      echo "  3. manifest 字段与 visible_when 等条件字段（dbx-plugin-runtime manifest.rs/host.rs、pluginFieldConditions）"
      echo "  4. sidecar 契约面：SUPPORTED_PLUGIN_HOST_API_VERSION / *_FEATURES / *_PERMISSIONS、"
      echo "     initialize 载荷形状、session 超时（crates/dbx-plugin-runtime/src/plugins/）"
      echo "  5. 各插件 mock（mockDbxHost/mockHost）必须镜像真实桥形状，防单测脱节"
      echo "  6. 装进真实 DBX.app 复验受影响的流（终端回显、下载分块、拖放）"
    else
      echo "no plugin-facing contract change in this sync"
    fi
    echo
  fi
  echo "current bridge contract snapshot (pluginHostBridge.ts):"
  grep -n "BRIDGE_VERSION = " apps/desktop/src/lib/plugins/pluginHostBridge.ts | head -1 | sed 's/^/    /'
  grep -n "message.type === 'binary'" -A2 apps/desktop/src/lib/plugins/pluginHostBridge.ts | sed 's/^/    /'
}

case "$CMD" in
  init) init_submodule ;;
  fetch) host_present || { echo "host checkout not found — run: scripts/host-sync.sh init" >&2; }
         fetch_upstream ;;
  sync) sync_upstream ;;
  contract) host_present || { echo "host checkout not found — run: scripts/host-sync.sh init" >&2; exit 1; }
            contract_watch ;;
  *) show_status ;;
esac
