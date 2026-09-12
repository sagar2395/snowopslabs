#!/usr/bin/env bash
# Delete the 'order-processors' consumer group, if it is there.
#
# A consumer group outlives both the Deployments that joined it and the topic
# it read: Kafka keeps its committed offsets under __consumer_offsets. Left
# behind, the next activation starts against a brand-new 'orders' topic while
# the group still remembers offsets from the old one, and kafka-exporter
# reports NEGATIVE lag for several scrapes — committed ahead of the log end.
#
# Runs on both activation and teardown, and is deliberately idempotent: a
# missing group is the desired state, not an error.
set -euo pipefail

NS="${KAFKA_NAMESPACE:-kafka}"
BROKER="${KAFKA_BROKER_POD:-lab-kafka-dual-role-0}"
GROUP="order-processors"

if ! kubectl -n "$NS" get pod "$BROKER" >/dev/null 2>&1; then
  echo "Kafka broker $BROKER is not present; nothing to reset."
  exit 0
fi

# Deleting the Deployment does not wait for its pods, so on teardown a consumer
# is usually still alive here — and one that is still alive re-registers the
# group moments after it is deleted. Wait for the members to actually go before
# touching the group. On activation there are no consumer pods yet, so this
# falls through immediately.
waited=0
while [ "$waited" -lt 60 ]; do
  live="$(kubectl -n "$NS" get pods -l app=orders-consumer \
    --no-headers 2>/dev/null | grep -c . || true)"
  [ "${live:-0}" -eq 0 ] && break
  echo "  waiting for $live consumer pod(s) to terminate..."
  sleep 5
  waited=$((waited + 5))
done

# The group cannot be deleted while a member is still connected, and the
# coordinator keeps it non-empty until the last member's session times out.
# Read the outcome from the delete itself rather than listing first: each
# kubectl exec starts a JVM on the broker and costs most of ten seconds, so a
# list-then-delete pair doubles the teardown for no extra information.
attempt=1
while [ "$attempt" -le 8 ]; do
  out="$(kubectl -n "$NS" exec "$BROKER" -- \
    bin/kafka-consumer-groups.sh --bootstrap-server localhost:9092 \
    --delete --group "$GROUP" 2>&1 || true)"

  case "$out" in
    *GroupIdNotFoundException* | *"not found"*)
      echo "Consumer group $GROUP is not present."
      exit 0
      ;;
    *GroupNotEmptyException*)
      echo "  $GROUP still has connected members; retrying ($attempt/8)..."
      sleep 5
      ;;
    *)
      echo "Deleted consumer group $GROUP."
      exit 0
      ;;
  esac
  attempt=$((attempt + 1))
done

# Not fatal: a surviving group costs the next run a few scrapes of odd lag,
# which clamp_min in the checks already absorbs. Failing teardown here would be
# a worse outcome than saying so.
echo "WARNING: could not delete consumer group $GROUP; it still has members." >&2
exit 0
