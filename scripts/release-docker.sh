#!/usr/bin/env bash
# Build the DBX web docker image with the workspace plugins baked in.
#
# The bundled-plugin list (ssh/ldap/files/kafka) lives in deploy/web/Dockerfile
# (Stage 2/3 COPY + packaging loops + plugins.tsv); this script passes the
# whole workspace as build context and needs no per-plugin changes.
#
# Two build contexts are used:
#   .                 -> this workspace (plugins + deploy/web)
#   host (named)      -> host/ submodule checkout, trimmed copy prepared here
#                        (target/node_modules/.git excluded to keep context small)
#
# Usage:
#   scripts/release-docker.sh                       # native arch image
#   scripts/release-docker.sh --platform linux/amd64  # cross build (QEMU, slow)
#   scripts/release-docker.sh --tag myrepo/dbx-plugins:v1 --push
#   scripts/release-docker.sh --skip-host-sync      # reuse previous trimmed host copy
#   scripts/release-docker.sh --no-clean            # skip the post-release
#                                                   #   artifact cleanup
#
# Every release ends with a cleanup step so old artifacts don't accumulate:
# stale smoke containers/volumes (also from runs killed before their EXIT trap),
# dangling images, previous images of the same repository (newest
# GC_DOCKER_KEEP kept), and buildx cache beyond GC_BUILDER_KEEP. Other projects'
# images and volumes are never touched — matching is scoped to this pipeline's
# own names and to dangling images only.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# Host checkout resolution: DBX_HOST_WORKTREE env → in-repo host/ submodule →
# legacy sibling dbx-plugin-host-worktree.
HOST_WORKTREE="${DBX_HOST_WORKTREE:-}"
if [ -z "$HOST_WORKTREE" ]; then
  for candidate in "$ROOT/host" "$ROOT/../dbx-plugin-host-worktree"; do
    if [ -e "$candidate/.git" ]; then HOST_WORKTREE="$candidate"; break; fi
  done
fi
[ -n "$HOST_WORKTREE" ] && [ -e "$HOST_WORKTREE/.git" ] || {
  echo "host checkout not found — init the host/ submodule (scripts/host-sync.sh init) or set DBX_HOST_WORKTREE" >&2
  exit 1
}
HOST_CONTEXT="${DBX_DOCKER_HOST_CONTEXT:-/tmp/dbx-docker-host}"
TAG="${DBX_WEB_IMAGE:-dbx-web-plugins:latest}"
PLATFORM=""
PUSH=0
SKIP_HOST_SYNC=0
NO_CLEAN=0
DOCKER_KEEP="${GC_DOCKER_KEEP:-3}"
BUILDER_KEEP="${GC_BUILDER_KEEP:-8g}"

while [ $# -gt 0 ]; do
  case "$1" in
    --tag) TAG="$2"; shift ;;
    --platform) PLATFORM="$2"; shift ;;
    --push) PUSH=1 ;;
    --skip-host-sync) SKIP_HOST_SYNC=1 ;;
    --no-clean) NO_CLEAN=1 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done

case "$DOCKER_KEEP" in ''|*[!0-9]*) echo "release-docker: GC_DOCKER_KEEP must be numeric, got: $DOCKER_KEEP" >&2; exit 2 ;; esac
[ -n "$BUILDER_KEEP" ] || { echo "release-docker: GC_BUILDER_KEEP must not be empty" >&2; exit 2; }

log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

# ---------------------------------------------------------------------------
log "step 1/4: prepare trimmed host build context ($HOST_CONTEXT)"
if [ "$SKIP_HOST_SYNC" = 1 ] && [ -d "$HOST_CONTEXT" ]; then
  echo "reuse existing copy (--skip-host-sync)"
else
  rm -rf "$HOST_CONTEXT"
  mkdir -p "$HOST_CONTEXT"
  rsync -a \
    --exclude target/ --exclude node_modules/ --exclude .git/ \
    --exclude dist/ --exclude .mimosa/ --exclude .omc/ \
    --exclude '*.log' --exclude .idea/ \
    "$HOST_WORKTREE"/ "$HOST_CONTEXT"/
fi
echo "host context size: $(du -sh "$HOST_CONTEXT" | cut -f1)"

# ---------------------------------------------------------------------------
log "step 2/4: build image $TAG"
BUILD_ARGS=(docker buildx build
  --build-context "host=$HOST_CONTEXT"
  -f "$ROOT/deploy/web/Dockerfile"
  -t "$TAG")
[ -n "$PLATFORM" ] && BUILD_ARGS+=(--platform "$PLATFORM")
[ "$PUSH" = 1 ] && BUILD_ARGS+=(--push) || BUILD_ARGS+=(--load)
cd "$ROOT"
"${BUILD_ARGS[@]}" .

# ---------------------------------------------------------------------------
log "step 3/4: smoke check"
CONTAINER="dbx-web-plugins-smoke-$$"
VOLUME="dbx-web-plugins-smoke-$$"
docker volume create "$VOLUME" >/dev/null
docker run -d --rm --name "$CONTAINER" -e DBX_DISABLE_PASSWORD=1 -v "$VOLUME":/app/data "$TAG" >/dev/null
trap 'docker rm -f "$CONTAINER" >/dev/null 2>&1; docker volume rm "$VOLUME" >/dev/null 2>&1' EXIT
sleep 5
echo "---- bundled plugin store inside container ----"
docker exec "$CONTAINER" sh -c 'ls /app/plugins-builtin/ && cat /app/plugins-builtin/plugins.tsv'
echo "---- installed plugins after entrypoint ----"
docker exec "$CONTAINER" sh -c 'ls "$DBX_DATA_DIR/plugins" 2>/dev/null || ls /app/data/plugins'
echo "---- dbx-web HTTP ----"
IP="$(docker inspect -f '{{range .NetworkSettings.Networks}}{{.IPAddress}}{{end}}' "$CONTAINER")"
curl -sf -o /dev/null -w "GET / -> %{http_code}\n" "http://$IP:4224/" || echo "WARN: http check failed (server may still be starting)"
docker logs "$CONTAINER" 2>&1 | grep -i "plugin" | head -10 || true

# ---------------------------------------------------------------------------
log "step 4/4: reclaim space from old release artifacts"
if [ "$NO_CLEAN" = 1 ]; then
  echo "skip cleanup (--no-clean)"
else
  # Our step-3 smoke container is still running; its checks are done, so the
  # prefix sweep below stopping it is expected (the EXIT trap then no-ops).
  # Matching is scoped to this pipeline's unique smoke prefix — leftovers from
  # killed runs and other projects' containers/volumes can never match.
  SMOKE_PREFIX="dbx-web-plugins-smoke-"
  while IFS= read -r c; do
    [ -n "$c" ] || continue
    docker rm -f "$c" >/dev/null 2>&1 || true
  done < <(docker ps -aq --filter "name=^/${SMOKE_PREFIX}")
  while IFS= read -r v; do
    [ -n "$v" ] || continue
    docker volume rm "$v" >/dev/null 2>&1 || true
  done < <(docker volume ls -q --filter "name=^${SMOKE_PREFIX}")

  # Dangling layers/images only — never a tagged image of another project.
  docker image prune -f >/dev/null

  # Previous images of THIS repository (older release tags): keep the newest
  # DOCKER_KEEP, drop the rest. Untagged rows are handled by the prune above.
  REPO="${TAG%:*}"
  docker images "$REPO" --format "{{.CreatedAt}}$(printf '\t'){{.Repository}}:{{.Tag}}" \
    | sort -r | tail -n +"$((DOCKER_KEEP + 1))" \
    | while IFS= read -r row; do
        img="${row#*$'\t'}"
        case "$img" in *":<none>"*) continue ;; esac
        echo "  removing old image: $img"
        docker rmi "$img" >/dev/null 2>&1 || true
      done

  # Buildx layer cache: keep roughly BUILDER_KEEP so repeat builds stay fast
  # and drop the remainder (each release build grows the cache). Flag names
  # differ across docker versions — newer buildx uses --max-used-space,
  # classic docker builder prune used --keep-storage.
  if docker buildx prune --help 2>&1 | grep -q -- --max-used-space; then
    docker buildx prune -f --max-used-space "$BUILDER_KEEP" >/dev/null
  elif docker builder prune --help 2>&1 | grep -q -- --keep-storage; then
    docker builder prune -f --keep-storage "$BUILDER_KEEP" >/dev/null
  else
    docker builder prune -f >/dev/null || true
  fi

  echo "docker disk usage after cleanup:"
  docker system df
fi

echo
echo "done. image: $TAG"
echo "server deploy: docker compose -f deploy/web/docker-compose.yml up -d (after pushing the image)"
