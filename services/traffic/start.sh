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
TARGET="${TRAFFIC_TARGET:-http://go-api.go-api.svc.cluster.local:8080/health}"
RPS="${TRAFFIC_RPS:-10}"
DURATION="${TRAFFIC_DURATION:-}"
K6_IMAGE="${K6_IMAGE:-grafana/k6:0.50.0}"
RW_URL="${K6_PROMETHEUS_RW_SERVER_URL:-}"

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
            - name: K6_PROMETHEUS_RW_SERVER_URL
              value: "$RW_URL"
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
echo "Traffic generator started."
echo "  Follow output:  kubectl logs -f job/traffic-k6 -n $NAMESPACE"
echo "  Stop:           labctl traffic stop"
echo "  Watch impact in Grafana next to the app's request-rate panels."
