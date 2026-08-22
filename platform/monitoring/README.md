# Monitoring Stack Setup

This directory contains the observability stack for the homelab platform, including **Prometheus** and **Grafana**.

## Architecture

```
monitoring/
├── prometheus/
│   ├── install.sh        # Install Prometheus operator + stack
│   ├── uninstall.sh      # Remove Prometheus and cleanup
│   ├── status.sh         # Check Prometheus health
│   └── values.yaml       # Helm chart configuration
│
└── grafana/
    ├── install.sh        # Install Grafana
    ├── uninstall.sh      # Remove Grafana and cleanup
    ├── status.sh         # Check Grafana health
    ├── values.yaml       # Helm chart configuration
    └── provisioning/
        ├── datasources/
        │   └── prometheus.yaml      # Prometheus datasource config
        └── dashboards/
            ├── cluster-metrics.json # Placeholder: K8s cluster metrics
            ├── pod-resources.json   # Placeholder: Pod CPU/memory usage
            └── app-requests.json    # Placeholder: HTTP request metrics
```

## Components

### Prometheus Stack (`kube-prometheus-stack`)
Includes:
- **Prometheus Operator**: CRD-based Prometheus management
- **Prometheus**: Metrics aggregation and storage (24h retention)
- **Node Exporter**: Hardware/OS metrics from each node
- **Kube-State-Metrics**: Kubernetes object metrics (pods, deployments, etc.)
- **Alertmanager**: Alert routing and aggregation (internal only)
- **Traefik Ingress**: Exposes Prometheus at `prometheus.k3d.local`

### Grafana
- **Datasource**: Auto-provisioned Prometheus datasource
- **Dashboards**: Placeholder JSON dashboards for customization
  - Cluster Metrics (CPU, memory, pod count)
  - Pod Resources (per-pod CPU/memory, network)
  - Application Requests (HTTP metrics from go-api)
- **Traefik Ingress**: Exposes Grafana at `grafana.k3d.local`
- **Admin Credentials**: `admin` / `admin` (changeable via `GRAFANA_ADMIN_PASSWORD` env var)

## Installation

### Option 1: Install monitoring only
```bash
make platform-monitoring-up
```

### Option 2: Install all platform components (ingress + monitoring)
```bash
make platform-up
```

This installs:
1. Traefik Ingress Controller (if not already running)
2. Prometheus Stack
3. Grafana

## Verification

### 1. Check Pod Status
```bash
kubectl get pods -n monitoring
```

Expected output (after ~2-3 minutes):
```
NAME                                    READY   STATUS    RESTARTS   AGE
prometheus-kube-prometheus-operator     1/1     Running   0          2m
prometheus-kube-prometheus-prometheus   2/2     Running   0          2m
prometheus-grafana                      1/1     Running   0          1m
node-exporter-xxxxx                     1/1     Running   0          2m
kube-state-metrics-xxxxx                1/1     Running   0          2m
alertmanager-main-0                     1/1     Running   0          2m
```

### 2. Verify Traefik Ingress
```bash
kubectl get ingress -n monitoring
```

Expected output:
```
NAME         CLASS     HOSTS                         ADDRESS     PORTS   AGE
prometheus   traefik   prometheus.k3d.local          172.x.x.x   80      2m
grafana      traefik   grafana.k3d.local            172.x.x.x   80      1m
```

### 3. Access Prometheus UI

**Option A: Via Traefik Ingress** (requires `/etc/hosts` entry)
```bash
# Add to /etc/hosts or use DNS:
echo "127.0.0.1 prometheus.k3d.local" | sudo tee -a /etc/hosts

# Visit:
http://prometheus.k3d.local
```

**Option B: Via Port Forward**
```bash
kubectl port-forward -n monitoring svc/prometheus-kube-prometheus-prometheus 9090:9090
# Visit: http://localhost:9090
```

### 4. Verify Prometheus Scrape Targets

In Prometheus UI, navigate to **Status > Targets**

You should see scrape jobs for:
- `prometheus` - Prometheus itself
- `kube-apiserver` - Kubernetes API server
- `kubelet` - Node kubelet metrics
- `node` - Node Exporter metrics
- `kube-state-metrics` - Kubernetes object metrics
- `alertmanager` - Alertmanager metrics
- Any pods with annotations:
  ```yaml
  prometheus.io/scrape: "true"
  prometheus.io/port: "8080"
  prometheus.io/path: "/metrics"
  ```

### 5. Access Grafana Dashboard

**Option A: Via Traefik Ingress**
```bash
# Add to /etc/hosts:
echo "127.0.0.1 grafana.k3d.local" | sudo tee -a /etc/hosts

# Visit:
http://grafana.k3d.local
```

**Option B: Via Port Forward**
```bash
kubectl port-forward -n monitoring svc/grafana 3000:80
# Visit: http://localhost:3000
```

### 6. Login to Grafana

Default credentials:
- **Username**: `admin`
- **Password**: `admin` (or set via `GRAFANA_ADMIN_PASSWORD=<password> make platform-monitoring-up`)

### 7. Verify Datasource

In Grafana:
1. Click **Configuration** (gear icon) > **Data Sources**
2. Should see **Prometheus** datasource
3. Click to test: Should show "Data source is working"

### 8. View Dashboards

In Grafana:
1. Click **Dashboards** (home icon) > **Manage**
2. Under folder "Kubernetes", you should see:
   - **Cluster Metrics**: Node CPU/memory, pod count, network I/O
   - **Pod Resources**: Per-pod CPU/memory usage by namespace
   - **Application Request Metrics**: HTTP request rate, latency, errors (from go-api)

### 9. Verify Go-API Metrics

If you have deployed go-api, verify its metrics are scraped:

```bash
# Deploy go-api if not already running
make deploy-app APP_NAME=go-api

# In Prometheus UI, search for these metrics:
# - http_requests_total
# - http_request_duration_seconds
# - up{job="go-api"}

# Or in terminal:
kubectl logs -n go-api -l app=go-api | grep "Building metrics"
```

In Grafana dashboard "Application Request Metrics", you should see:
- HTTP request rate
- Request latency (p95, p99)
- HTTP response codes (2xx, 4xx, 5xx)
- Error rate

## Customization

### Change Grafana Admin Password

```bash
GRAFANA_ADMIN_PASSWORD=mySecurePassword make platform-monitoring-up
```

### Customize Dashboards

All dashboards are stored in `platform/monitoring/grafana/provisioning/dashboards/`

1. Export dashboard from Grafana UI as JSON
2. Replace the corresponding JSON file
3. Re-install: `bash platform/monitoring/grafana/install.sh`

### Add Custom Prometheus Scrape Jobs

Edit `platform/monitoring/prometheus/values.yaml` and add under `additionalScrapeConfigs`:

```yaml
additionalScrapeConfigs:
  - job_name: custom-job
    static_configs:
      - targets: ['localhost:9090']
```

Then re-install: `bash platform/monitoring/prometheus/install.sh`

### Modify Retention Period

In `platform/monitoring/prometheus/values.yaml`, search for `retention`:

```yaml
prometheusSpec:
  retention: 24h  # Change this (default: 24h for homelab)
  retentionSize: "2GB"  # Max storage size
```

Then re-install.

## Uninstallation

### Remove monitoring stack only
```bash
make platform-monitoring-down
```

### Remove all platform components
```bash
make platform-down
```

This uninstalls:
1. Grafana
2. Prometheus Stack
3. Traefik Ingress (if defined in make/platform.mk)

## Troubleshooting

### Prometheus not starting
```bash
kubectl logs -n monitoring deployment/prometheus-kube-prometheus-prometheus
```

### Grafana not connecting to Prometheus
```bash
# Check datasource in Grafana UI
# Or check network access:
kubectl run -ti --rm debug --image=curlimages/curl --restart=Never -- \
  curl -v http://prometheus-kube-prometheus-prometheus.monitoring:9090
```

### High memory usage
- Reduce `retention` period in prometheus/values.yaml (default 24h)
- Reduce `retentionSize` limit
- Limit scrape targets

### Dashboards not showing data
- Verify Prometheus is scraping targets (check **Status > Targets**)
- Check PromQL queries in dashboard for syntax errors
- Ensure metric names match what Prometheus collects (use **Status > Targets** to see available metrics)

### Port forwarding issues
```bash
# Kill existing port-forwards:
killall kubectl

# Try again:
kubectl port-forward -n monitoring svc/grafana 3000:80
```

## Makefile Targets

```bash
# Install both Prometheus and Grafana
make platform-monitoring-up

# Remove both
make platform-monitoring-down

# Check status of both
make platform-monitoring-status

# Individual scripts
bash platform/monitoring/prometheus/install.sh
bash platform/monitoring/prometheus/uninstall.sh
bash platform/monitoring/prometheus/status.sh

bash platform/monitoring/grafana/install.sh
bash platform/monitoring/grafana/uninstall.sh
bash platform/monitoring/grafana/status.sh
```

## Integration with Go-API

Go-API exposes metrics in Prometheus format at `GET /metrics` (port 8080 by default).

Metrics include:
- `http_requests_total`: Total HTTP requests by endpoint
- `http_request_duration_seconds`: Request latency distribution (histogram)
- Standard Go runtime metrics

Pod annotations in [apps/go-api/deploy/helm/templates/deployment.yaml](../../../apps/go-api/deploy/helm/templates/deployment.yaml):
```yaml
prometheus.io/scrape: "true"
prometheus.io/port: "8080"
prometheus.io/path: "/metrics"
```

These enable automatic Prometheus scraping without needing ServiceMonitor CRDs.

## References

- [kube-prometheus-stack Helm Chart](https://github.com/prometheus-community/helm-charts/tree/main/charts/kube-prometheus-stack)
- [Grafana Helm Chart](https://github.com/grafana/helm-charts/tree/main/charts/grafana)
- [Prometheus Operator](https://prometheus-operator.dev/)
- [Grafana Documentation](https://grafana.com/docs/)
