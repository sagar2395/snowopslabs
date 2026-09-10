#!/usr/bin/env bash
# Proves STRICT mTLS is actually enforced, by being refused.
#
# Reading the PeerAuthentication back only proves a CR exists. The enforcement
# lives in the sidecar, so the only honest test is to send a plaintext request
# from a client with no workload identity and require it to fail.
set -euo pipefail

NS="${WORKLOAD_NAMESPACE:-go-api}"
APP="${WORKLOAD_NAME:-go-api}"
JOB="${APP}-mtls-probe"
TARGET="http://${APP}-canary.${NS}.svc.cluster.local"

cleanup() {
  kubectl -n "$NS" delete job "$JOB" --ignore-not-found --wait=false >/dev/null 2>&1 || true
}
trap cleanup EXIT

kubectl -n "$NS" delete job "$JOB" --ignore-not-found --wait=true >/dev/null 2>&1 || true

# sidecar.istio.io/inject=false is what makes this a valid negative control: the
# pod is in the meshed namespace but deliberately outside the mesh.
# sidecar.istio.io/inject=false is what makes this a valid negative control: the
# pod sits in the meshed namespace but deliberately outside the mesh, so it has
# no identity to present.
#
# The probe is wget, not k6: k6's client is reset by the sidecar even under
# PERMISSIVE, so a k6-based probe passes whether or not STRICT is in force —
# a check that cannot fail. wget is served under PERMISSIVE and refused under
# STRICT, which is the distinction being graded. The image is the one the load
# client already pulls, so this adds no new dependency.
#
# The container inverts the result: exit 0 (Job complete) means refused.
kubectl apply -f - >/dev/null <<YAML
apiVersion: batch/v1
kind: Job
metadata:
  name: ${JOB}
  namespace: ${NS}
spec:
  backoffLimit: 0
  ttlSecondsAfterFinished: 120
  template:
    metadata:
      annotations:
        sidecar.istio.io/inject: "false"
      labels:
        app: ${JOB}
    spec:
      restartPolicy: Never
      containers:
        - name: probe
          image: grafana/k6:0.50.0
          command: ["sh", "-c"]
          args:
            - |
              if wget -q -T 8 -O- "\$TARGET/version" >/dev/null 2>&1; then
                echo "SERVED: plaintext from an unmeshed client was accepted"
                exit 1
              fi
              echo "REFUSED: the connection was rejected"
              exit 0
          env:
            - name: TARGET
              value: "${TARGET}"
YAML

if ! kubectl -n "$NS" wait --for=condition=complete "job/${JOB}" --timeout=90s >/dev/null 2>&1; then
  echo "FAIL: a plaintext client without a sidecar was NOT refused by the canary." >&2
  echo "  STRICT mTLS is not in force. Probe output:" >&2
  kubectl -n "$NS" logs "job/${JOB}" --tail=20 2>/dev/null | sed 's/^/    /' >&2 || true
  echo "  Apply the PeerAuthentication from 'labctl scenario info mesh-traffic-management'." >&2
  exit 1
fi

echo "OK: an unmeshed plaintext client was refused — STRICT mTLS is enforced."
