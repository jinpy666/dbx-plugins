#!/usr/bin/env bash
# Seed deterministic test topics into the dbx-kafka-test container
# (docker-compose.kafka-test.yml, apache/kafka KRaft single node).
#
# Idempotent: `kafka-topics.sh --create --if-not-exists` is safe to re-run.
# No credentials: the container is PLAINTEXT, topics are created via the
# in-container kafka-topics.sh against the local listener.
set -euo pipefail

CONTAINER="${KAFKA_TEST_CONTAINER:-dbx-kafka-test}"
BOOTSTRAP="${KAFKA_TEST_BOOTSTRAP_INNER:-localhost:9092}"
KAFKA_BIN="${KAFKA_TEST_BIN_INNER:-/opt/kafka/bin}"

topics=(
  # topic:partitions:replication
  "dbx-smoke-events:3:1"
  "dbx-smoke-binary:1:1"
  "dbx-smoke-filter:1:1"
  "dbx-smoke-stream:1:1"
  "dbx-smoke-export:1:1"
)

echo "==> seeding topics into ${CONTAINER} (${BOOTSTRAP})"
for spec in "${topics[@]}"; do
  IFS=":" read -r topic partitions replication <<<"$spec"
  docker exec "$CONTAINER" "$KAFKA_BIN/kafka-topics.sh" \
    --bootstrap-server "$BOOTSTRAP" \
    --create --if-not-exists \
    --topic "$topic" \
    --partitions "$partitions" \
    --replication-factor "$replication"
done

# Configured topic for kafka/topics/config get/alter checks (idempotent alter).
docker exec "$CONTAINER" "$KAFKA_BIN/kafka-configs.sh" \
  --bootstrap-server "$BOOTSTRAP" \
  --alter --entity-type topics --entity-name dbx-smoke-events \
  --add-config retention.ms=604800000 >/dev/null

echo "==> seeded topics:"
docker exec "$CONTAINER" "$KAFKA_BIN/kafka-topics.sh" \
  --bootstrap-server "$BOOTSTRAP" --list
