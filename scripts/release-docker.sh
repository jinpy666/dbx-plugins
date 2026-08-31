#!/usr/bin/env bash
# Build the DBX web docker image with the workspace plugins baked in.
#
# Two build contexts are used:
#   .                 -> this workspace (plugins + deploy/web)
#   host (named)      -> dbx-plugin-host-worktree, trimmed copy prepared here
#                        (target/node_modules/.git excluded to keep context small)
#
# Usage:
#   scripts/release-docker.sh                       # native arch image
#   scripts/release-docker.sh --platform linux/amd64  # cross build (QEMU, slow)
#   scripts/release-docker.sh --tag myrepo/dbx-plugins:v1 --push
#   scripts/release-docker.sh --skip-host-sync      # reuse previous trimmed host copy
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
HOST_WORKTREE="${DBX_HOST_WORKTREE:-$ROOT/../dbx-plugin-host-worktree}"
HOST_CONTEXT="${DBX_DOCKER_HOST_CONTEXT:-/tmp/dbx-docker-host}"
TAG="${DBX_WEB_IMAGE:-dbx-web-plugins:latest}"
PLATFORM=""
PUSH=0
SKIP_HOST_SYNC=0

while [ $# -gt 0 ]; do
  case "$1" in
    --tag) TAG="$2"; shift ;;
    --platform) PLATFORM="$2"; shift ;;
    --push) PUSH=1 ;;
    --skip-host-sync) SKIP_SYNC=1 ;;
    *) echo "unknown option: $1" >&2; exit 2 ;;
  esac
  shift
done

log() { printf '\n\033[1;36m==> %s\033[0m\n' "$*"; }

# ---------------------------------------------------------------------------
log "step 1/3: prepare trimmed host build context ($HOST_CONTEXT)"
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
log "step 2/3: build image $TAG"
BUILD_ARGS=(docker buildx build
  --build-context "host=$HOST_CONTEXT"
  -f "$ROOT/deploy/web/Dockerfile"
  -t "$TAG")
[ -n "$PLATFORM" ] && BUILD_ARGS+=(--platform "$PLATFORM")
[ "$PUSH" = 1 ] && BUILD_ARGS+=(--push) || BUILD_ARGS+=(--load)
cd "$ROOT"
"${BUILD_ARGS[@]}" .

# ---------------------------------------------------------------------------
log "step 3/3: smoke check"
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

echo
echo "done. image: $TAG"
echo "server deploy: docker compose -f deploy/web/docker-compose.yml up -d (after pushing the image)"
