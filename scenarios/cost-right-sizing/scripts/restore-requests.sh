#!/usr/bin/env bash
set -euo pipefail

# restore-requests.sh — the inverse of inflate.sh, run by `scenario down`.
#
# inflate.sh mutates a Deployment that every other scenario shares. Without this
# teardown, `down` leaves the workload over-provisioned (or at whatever the
# learner right-sized it to), and the next scenario starts from a state its
# author never intended.

NAMESPACE="${NAMESPACE:-${WORKLOAD_NAMESPACE:-go-api}}"
APP="${APP:-${WORKLOAD_NAME:-go-api}}"
MARK="snowops.net/cost-right-sizing-original"

if ! kubectl get deployment "$APP" -n "$NAMESPACE" >/dev/null 2>&1; then
  echo "Deployment ${NAMESPACE}/${APP} is gone; nothing to restore."
  exit 0
fi

# The dots in "snowops.net/..." are jsonpath field separators unless they are
# escaped, so an unescaped path walks into a field called "snowops" and returns
# nothing. Every read looked like "the workload had no such setting", and the
# restore below then stripped requests and limits from a Deployment that every
# other scenario shares.
read_mark() {
  esc_mark="$(printf '%s' "$MARK" | sed 's/\./\\./g')"
  kubectl -n "$NAMESPACE" get deploy "$APP" \
    -o "jsonpath={.metadata.annotations.$esc_mark-$1}" 2>/dev/null || true
}

# "none" means the workload had no such setting; `set resources` spells that 0.
restore_value() {
  case "$1" in
    "" | none) echo "0" ;;
    *) echo "$1" ;;
  esac
}

RAW_CPU_REQ="$(read_mark cpu-request)"
RAW_MEM_REQ="$(read_mark mem-request)"
RAW_CPU_LIM="$(read_mark cpu-limit)"
RAW_MEM_LIM="$(read_mark mem-limit)"

# "none" is a value we recorded and means the workload genuinely had no such
# setting; ALL FOUR empty means we never recorded anything, which is not the
# same thing. Zeroing a shared workload's resources on that basis is worse than
# leaving the drill's sizing in place, so do nothing and say so.
if [ -z "$RAW_CPU_REQ$RAW_MEM_REQ$RAW_CPU_LIM$RAW_MEM_LIM" ]; then
  echo "No recorded original sizing on ${NAMESPACE}/${APP}; leaving its resources alone."
  echo "  (If this scenario inflated it, re-deploy to reset: labctl app deploy ${APP})"
  exit 0
fi

CPU_REQ="$(restore_value "$RAW_CPU_REQ")"
MEM_REQ="$(restore_value "$RAW_MEM_REQ")"
CPU_LIM="$(restore_value "$RAW_CPU_LIM")"
MEM_LIM="$(restore_value "$RAW_MEM_LIM")"

echo "Restoring ${NAMESPACE}/${APP} to the sizing it had before the drill..."
kubectl -n "$NAMESPACE" set resources deployment "$APP" \
  --requests="cpu=${CPU_REQ},memory=${MEM_REQ}" \
  --limits="cpu=${CPU_LIM},memory=${MEM_LIM}"

kubectl -n "$NAMESPACE" annotate deploy "$APP" \
  "$MARK-cpu-request-" "$MARK-mem-request-" \
  "$MARK-cpu-limit-" "$MARK-mem-limit-" --overwrite >/dev/null 2>&1 || true

echo "Restored ${APP} to requests cpu=${CPU_REQ}, memory=${MEM_REQ} (limits ${CPU_LIM}/${MEM_LIM})."
