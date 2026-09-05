#!/usr/bin/env bash
# Local, connection-testable Kafka environment for the dbx-kafka plugin.
#
# Wraps docker-compose.kafka-test.yml (apache/kafka KRaft single node on
# 127.0.0.1:9092 + redpanda Schema Registry on 127.0.0.1:19081) so a user can
# stand the cluster up and immediately test a plugin connection from the DBX
# host:
#
#   bash scripts/dev-cluster.sh up      # start + wait for real metadata
#                                       # readiness + seed topics + print
#                                       # connection parameters
#   bash scripts/dev-cluster.sh status  # containers/ports + live metadata
#                                       # probe + connection parameters
#   bash scripts/dev-cluster.sh seed    # idempotently (re)create seed topics
#   bash scripts/dev-cluster.sh down    # compose down -v (tears everything
#                                       # down including volumes)
#
# Readiness mirrors scripts/smoke_container.py: a real metadata request
# (kafka-topics.sh --list inside the container), never a bare port probe.
# PLAINTEXT only: no SASL/TLS, no credentials anywhere in this setup.
set -euo pipefail

ROOT="$(cd "$(dirname "$0")/../.." && pwd)"
COMPOSE_FILE="$ROOT/kafka/docker-compose.kafka-test.yml"
SEED_SCRIPT="$ROOT/kafka/scripts/kafka-seed/seed-topics.sh"
CONTAINER="${KAFKA_TEST_CONTAINER:-dbx-kafka-test}"
SR_CONTAINER="dbx-kafka-sr-test"
HOST="${KAFKA_TEST_HOST:-127.0.0.1}"
PORT="${KAFKA_TEST_PORT:-9092}"
INNER_BOOTSTRAP="localhost:9092"
READY_TIMEOUT_SECS=120

compose() { docker compose -f "$COMPOSE_FILE" "$@"; }

metadata_probe() { # real metadata/list request inside the container
  docker exec "$CONTAINER" /opt/kafka/bin/kafka-topics.sh \
    --bootstrap-server "$INNER_BOOTSTRAP" --list
}

container_running() {
  [ "$(docker inspect -f '{{.State.Running}}' "$1" 2>/dev/null)" = "true" ]
}

host_port_open() {
  (exec 3<>"/dev/tcp/${HOST}/${PORT}") 2>/dev/null
}

wait_ready() {
  echo "==> waiting for metadata readiness on ${HOST}:${PORT} (timeout ${READY_TIMEOUT_SECS}s)"
  local deadline=$((SECONDS + READY_TIMEOUT_SECS))
  while [ "$SECONDS" -lt "$deadline" ]; do
    if metadata_probe >/dev/null 2>&1; then
      sleep 1 # let the listener settle, same as smoke_container.py
      echo "==> broker answers metadata requests"
      return 0
    fi
    sleep 2
  done
  echo "ERROR: no metadata answer on ${HOST}:${PORT} after ${READY_TIMEOUT_SECS}s — check 'docker compose -f ${COMPOSE_FILE} logs'" >&2
  return 1
}

print_connection() {
  cat <<EOF

Connection parameters (fill into the DBX host Kafka connection form):
  Bootstrap servers : ${HOST}:${PORT}
  Security protocol : PLAINTEXT
  Auth              : none (no SASL/TLS — the cluster has no credentials)
  Schema Registry   : http://${HOST}:19081 (optional; Confluent-compatible,
                      used by smoke scenario S11)

The plugin sidecar dials through the host runtime; DBX.app runs on this same
machine, so ${HOST}:${PORT} is reachable directly — no SSH tunnel or proxy
needed. Seed topics: dbx-smoke-events, dbx-smoke-binary, dbx-smoke-filter,
dbx-smoke-stream, dbx-smoke-export.
EOF
}

cmd_up() {
  echo "==> docker compose up -d (${COMPOSE_FILE})"
  compose up -d
  wait_ready
  echo "==> seeding topics (idempotent)"
  bash "$SEED_SCRIPT"
  if container_running "$SR_CONTAINER"; then
    echo "==> schema registry sidecar ${SR_CONTAINER} running (http://${HOST}:19081)"
  fi
  print_connection
  echo
  echo "Cluster is up and left running for connection testing."
  echo "Tear it down later with: bash $(basename "$(dirname "$0")")/$(basename "$0") down"
}

cmd_status() {
  echo "==> containers"
  compose ps
  echo
  echo "==> host port ${HOST}:${PORT}"
  if host_port_open; then
    echo "open (TCP)"
  else
    echo "closed/no listener"
  fi
  echo
  echo "==> metadata probe (kafka-topics --list inside ${CONTAINER})"
  if topics="$(metadata_probe 2>/dev/null)"; then
    echo "OK — broker answered; topics:"
    printf '%s\n' "$topics" | sed 's/^/  /'
  else
    echo "FAIL — broker not answering metadata requests"
  fi
  print_connection
}

cmd_seed() {
  container_running "$CONTAINER" || {
    echo "container ${CONTAINER} is not running — run 'up' first" >&2
    exit 1
  }
  bash "$SEED_SCRIPT"
}

cmd_down() {
  echo "==> docker compose down -v (${COMPOSE_FILE})"
  compose down -v
  echo "==> cluster torn down (containers + volumes removed)"
}

usage() {
  cat <<EOF
Usage: bash scripts/dev-cluster.sh <up|status|seed|down>

  up      start the compose stack, wait for real metadata readiness, seed the
          deterministic topics, print connection parameters (leaves it running)
  status  container/port state + live metadata probe + connection parameters
  seed    idempotently (re)create the seed topics on a running cluster
  down    compose down -v (remove containers + volumes)

Environment overrides: KAFKA_TEST_CONTAINER / KAFKA_TEST_HOST / KAFKA_TEST_PORT
(same names as scripts/smoke_test.py).
EOF
}

case "${1:-}" in
  up) cmd_up ;;
  status) cmd_status ;;
  seed) cmd_seed ;;
  down) cmd_down ;;
  *) usage >&2; exit 2 ;;
esac
