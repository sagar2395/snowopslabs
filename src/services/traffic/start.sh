#!/usr/bin/env bash
# Start (or restart) the k6 traffic generator with the selected profile.
# Config via env (set by labctl or the caller):
#   TRAFFIC_PROFILE   steady | spike | soak            (default: steady)
#   TRAFFIC_TARGET    URL to load                      (default: go-api health, in-cluster)
#   TRAFFIC_RPS       requests/sec (baseline for spike) (default: 10)
#   TRAFFIC_DURATION  run length, k6 duration syntax    (default: profile-specific)
#   TRAFFIC_NAMESPACE namespace for the generator       (default: traffic)
#   K6_IMAGE          k6 container image                (default: grafana/k6:0.50.0)
#   K6_PROMETHEUS_RW_SERVER_URL  optional remote-write endpoint for k6 metrics
set -euo pipefail

NAMESPACE="${TRAFFIC_NAMESPACE:-traffic}"
PROFILE="${TRAFFIC_PROFILE:-steady}"
# Default to the app root ("/") rather than "/health": the root is access-logged
# at Info, so traffic is visible in `kubectl logs deploy/go-api` and its request
# metrics move in Grafana. Multi-endpoint profiles (browse/write/errors) treat
# TRAFFIC_TARGET as a BASE origin and append their own paths.
TARGET="${TRAFFIC_TARGET:-http://go-api.go-api.svc.cluster.local:8080/}"
RPS="${TRAFFIC_RPS:-10}"
DURATION="${TRAFFIC_DURATION:-}"
METHOD="${TRAFFIC_METHOD:-GET}"
K6_IMAGE="${K6_IMAGE:-grafana/k6:0.50.0}"
RW_URL="${K6_PROMETHEUS_RW_SERVER_URL:-}"

# k6's own client-side metrics (requests sent, latency as the CLIENT saw it,
# failures) are the other half of evaluating traffic: the app's /metrics only
# counts requests that arrived. Push them to Prometheus when a receiver is
# actually there — enabling the output without one fails every flush.
# Set TRAFFIC_METRICS=off to skip the probe, or pass the URL explicitly.
PROM_NAMESPACE="${MONITORING_NAMESPACE:-monitoring}"
PROM_SERVICE="${PROMETHEUS_SERVICE:-prometheus-kube-prometheus-prometheus}"
if [ -z "$RW_URL" ] && [ "${TRAFFIC_METRICS:-auto}" = "auto" ]; then
  if [ -n "$(kubectl get svc "$PROM_SERVICE" -n "$PROM_NAMESPACE" -o name 2>/dev/null)" ]; then
    RW_URL="http://${PROM_SERVICE}.${PROM_NAMESPACE}.svc.cluster.local:9090/api/v1/write"
  fi
fi

SCRIPT_DIR="$(cd "$(dirname "$0")" && pwd)"
PROFILE_FILE="$SCRIPT_DIR/profiles/$PROFILE.js"

if [ ! -f "$PROFILE_FILE" ]; then
  echo "Unknown traffic profile '$PROFILE'. Available profiles:"
  for f in "$SCRIPT_DIR"/profiles/*.js; do
    basename "$f" .js
  done
  exit 1
fi

echo "Starting traffic generator"
echo "  profile:  $PROFILE"
echo "  target:   $TARGET"
echo "  rps:      $RPS"
echo "  duration: ${DURATION:-(profile default)}"
echo "  method:   $METHOD"
if [ -n "$RW_URL" ]; then
  echo "  metrics:  k6 -> Prometheus remote write ($RW_URL)"
else
  echo "  metrics:  k6 client-side metrics stay in the pod log (no Prometheus receiver found)"
fi

# Namespace + profile script (idempotent)
kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -
kubectl create configmap traffic-profile \
  --from-file=script.js="$PROFILE_FILE" \
  --namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

# Replace any previous run: starting while running restarts with new settings.
kubectl delete job traffic-k6 --namespace "$NAMESPACE" --ignore-not-found --wait=true

# Optional k6 → Prometheus remote write output.
K6_ARGS='["run", "/scripts/script.js"]'
if [ -n "$RW_URL" ]; then
  K6_ARGS='["run", "-o", "experimental-prometheus-rw", "/scripts/script.js"]'
fi

kubectl apply -f - <<EOF
apiVersion: batch/v1
kind: Job
metadata:
  name: traffic-k6
  namespace: $NAMESPACE
  labels:
    app: traffic-k6
    traffic-profile: $PROFILE
spec:
  backoffLimit: 0
  ttlSecondsAfterFinished: 600
  template:
    metadata:
      labels:
        app: traffic-k6
    spec:
      restartPolicy: Never
      containers:
        - name: k6
          image: $K6_IMAGE
          args: $K6_ARGS
          env:
            - name: TRAFFIC_TARGET
              value: "$TARGET"
            - name: TRAFFIC_RPS
              value: "$RPS"
            - name: TRAFFIC_DURATION
              value: "$DURATION"
            - name: TRAFFIC_METHOD
              value: "$METHOD"
            - name: K6_PROMETHEUS_RW_SERVER_URL
              value: "$RW_URL"
            # Export latency percentiles, not just the mean, so the k6-side
            # panels line up with the app's histogram quantiles.
            - name: K6_PROMETHEUS_RW_TREND_STATS
              value: "p(50),p(95),p(99),max"
          resources:
            requests:
              cpu: 100m
              memory: 128Mi
            limits:
              memory: 512Mi
          volumeMounts:
            - name: profile
              mountPath: /scripts
              readOnly: true
      volumes:
        - name: profile
          configMap:
            name: traffic-profile
EOF

echo ""
echo "Traffic generator applied. Verifying it actually starts..."

# Surface the common failure modes instead of leaving the user guessing why
# "nothing is happening": image can't be pulled, or the target is unreachable.
if kubectl wait --for=condition=Ready pod -l app=traffic-k6 -n "$NAMESPACE" --timeout=90s 2>/dev/null; then
  echo "✓ k6 pod is running. First output (should show it hitting ${TARGET}):"
  echo "----------------------------------------------------------------------"
  kubectl logs job/traffic-k6 -n "$NAMESPACE" --tail=20 2>/dev/null || echo "  (no logs yet — k6 is warming up)"
  echo "----------------------------------------------------------------------"
  echo "If you see 'dial: connection refused' or timeouts above, the TARGET is"
  echo "not reachable from the cluster — check the service exists and the URL."
else
  echo "⚠ k6 pod did NOT become Ready within 90s. Diagnosis:"
  kubectl get pods -n "$NAMESPACE" -l app=traffic-k6 -o wide 2>/dev/null || true
  echo "--- recent events ---"
  kubectl get events -n "$NAMESPACE" --sort-by=.lastTimestamp 2>/dev/null | tail -8 || true
  echo ""
  echo "Likely causes:"
  echo "  • ImagePullBackOff → the cluster can't pull ${K6_IMAGE}."
  echo "      Pre-load it:  docker pull ${K6_IMAGE} && k3d image import ${K6_IMAGE} -c \${CLUSTER_NAME:-snowops}"
  echo "  • Pending/Unschedulable → not enough CPU/memory on the node."
fi

echo ""
echo "  Follow live output:  kubectl logs -f job/traffic-k6 -n $NAMESPACE"
echo "  Check status:        labctl traffic status"
echo "  Stop:                labctl traffic stop"
echo "  See it in go-api's logs:  kubectl -n go-api logs deploy/go-api -f | grep request"
echo "  Watch impact in Grafana next to the app's request-rate panels."
