#!/usr/bin/env bash
# Install the lab pager: a tiny in-cluster webhook receiver that records
# every alert Alertmanager sends it, so on-call drills work with zero
# external accounts. View pages via 'labctl service status pager' or
# kubectl logs. Point Alertmanager elsewhere with ALERT_WEBHOOK_URL
# (re-run platform up monitoring/metrics after changing it).
set -euo pipefail

NAMESPACE="pager"
PAGER_IMAGE="${PAGER_IMAGE:-python:3.12-alpine}"

echo "Installing the lab pager (alert webhook receiver)..."

kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

kubectl apply -f - <<'PYEOF'
apiVersion: v1
kind: ConfigMap
metadata:
  name: pager-app
  namespace: pager
data:
  pager.py: |
    # Minimal Alertmanager webhook sink: stores pages in memory, prints each
    # one as a PAGE log line (the durable record), serves GET / as JSON.
    from http.server import BaseHTTPRequestHandler, HTTPServer
    import json, datetime

    PAGES = []

    class Handler(BaseHTTPRequestHandler):
        def do_POST(self):
            length = int(self.headers.get('Content-Length', 0))
            body = self.rfile.read(length)
            try:
                payload = json.loads(body)
            except Exception:
                payload = {"raw": body.decode("utf-8", "replace")}
            entry = {
                "receivedAt": datetime.datetime.now(datetime.timezone.utc).isoformat(),
                "payload": payload,
            }
            PAGES.append(entry)
            print("PAGE " + json.dumps(entry), flush=True)
            self.send_response(200)
            self.end_headers()

        def do_GET(self):
            self.send_response(200)
            self.send_header('Content-Type', 'application/json')
            self.end_headers()
            self.wfile.write(json.dumps(PAGES, indent=2).encode())

        def log_message(self, *args):
            pass

    print("lab pager listening on :8080", flush=True)
    HTTPServer(("", 8080), Handler).serve_forever()
PYEOF

cat <<EOF | kubectl apply -f -
apiVersion: apps/v1
kind: Deployment
metadata:
  name: pager
  namespace: $NAMESPACE
  labels:
    app: pager
spec:
  replicas: 1
  selector:
    matchLabels:
      app: pager
  template:
    metadata:
      labels:
        app: pager
    spec:
      containers:
        - name: pager
          image: $PAGER_IMAGE
          command: ["python3", "/app/pager.py"]
          ports:
            - containerPort: 8080
          resources:
            requests:
              cpu: 10m
              memory: 32Mi
            limits:
              memory: 64Mi
          volumeMounts:
            - name: app
              mountPath: /app
              readOnly: true
      volumes:
        - name: app
          configMap:
            name: pager-app
---
apiVersion: v1
kind: Service
metadata:
  name: pager
  namespace: $NAMESPACE
spec:
  selector:
    app: pager
  ports:
    - port: 8080
      targetPort: 8080
EOF

echo "Waiting for the pager to be ready..."
kubectl rollout status deploy/pager -n "$NAMESPACE" --timeout=120s || true

echo "Pager installed."
echo "  Webhook URL (Alertmanager default): http://pager.pager.svc.cluster.local:8080/alert"
echo "  View pages: kubectl logs deploy/pager -n $NAMESPACE | grep ^PAGE"
