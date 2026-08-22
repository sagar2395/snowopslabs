# Go API Helm Chart

This is a Helm chart for deploying the Go API application on k3d (Kubernetes in Docker).

## Prerequisites

- k3d cluster running
- kubectl configured
- Helm 3.x installed

## Features

- **Deployment**: Multi-replica deployment with rolling updates
- **Service**: ClusterIP service exposure
- **Ingress**: Traefik-based ingress with hostname routing
- **Health Checks**: Liveness and readiness probes configured
- **Resource Management**: CPU and memory limits/requests
- **Autoscaling**: Horizontal Pod Autoscaler (HPA) with CPU and memory metrics
- **Monitoring**: Prometheus metrics exposure via `/metrics` endpoint
- **Security**: Pod security context with non-privileged configuration
- **Pod Anti-Affinity**: Spreads pods across nodes when possible

## Installation

### 1. Build and Tag the Docker Image

First, ensure your Docker image is available in k3d:

```bash
cd apps/go-api

# Build the Docker image
docker build -t go-api:latest .

# Import into k3d cluster
k3d image import go-api:latest -c <cluster-name>
```

### 2. Install Helm Chart

From the repository root:

```bash
# Basic installation with defaults
helm install go-api apps/go-api/deploy/helm

# Or with custom values
helm install go-api apps/go-api/deploy/helm -f custom-values.yaml

# Or using Make target (when implemented)
make deploy-go-api PROFILE=k3d
```

### 3. Verify Deployment

```bash
# Check deployment status
kubectl get deployments -n go-api
kubectl get pods -n go-api
kubectl get svc -n go-api

# Check ingress
kubectl get ingress -n go-api

# View logs
kubectl logs -n go-api -l app=go-api -f
```

## Configuration

### Key Values Parameters

Edit `values.yaml` to customize:

| Parameter | Default | Description |
|-----------|---------|-------------|
| `deployment.replicaCount` | 2 | Number of pod replicas |
| `image.repository` | go-api | Docker image repository |
| `image.tag` | latest | Docker image tag |
| `port` | 8080 | Application port |
| `ingress.enabled` | true | Enable ingress |
| `ingress.hosts[0].host` | go-api.k3d.local | Ingress hostname |
| `autoscaling.enabled` | true | Enable HPA |
| `autoscaling.minReplicas` | 2 | Minimum pod replicas |
| `autoscaling.maxReplicas` | 5 | Maximum pod replicas |
| `prometheus.enabled` | true | Enable Prometheus metrics |

### Using Custom Values

Create a `custom-values.yaml`:

```yaml
deployment:
  replicaCount: 3

resources:
  requests:
    cpu: 200m
    memory: 128Mi
  limits:
    cpu: 700m
    memory: 512Mi

ingress:
  hosts:
    - host: my-api.example.com
      paths:
        - path: /
          pathType: Prefix

autoscaling:
  minReplicas: 3
  maxReplicas: 10
  targetCPUUtilizationPercentage: 70
```

Install with custom values:

```bash
helm install go-api apps/go-api/deploy/helm/go-api -f custom-values.yaml
```

## Accessing the Application

### Via Ingress (Recommended)

Add to your `/etc/hosts`:

```
127.0.0.1 go-api.k3d.local
```

Then access via:
```
http://go-api.k3d.local/health
http://go-api.k3d.local/ready
http://go-api.k3d.local/metrics
```

### Via Port Forward

```bash
kubectl port-forward -n go-api svc/go-api 8080:8080
```

Then access via: `http://localhost:8080`

## Monitoring

### Prometheus Metrics

The application exposes Prometheus metrics at `/metrics` endpoint with:
- `http_requests_total` - Total HTTP requests counter
- `http_request_duration_seconds` - HTTP request duration histogram

### Enable ServiceMonitor (if using Prometheus Operator)

Edit `values.yaml`:

```yaml
serviceMonitor:
  enabled: true
  namespace: monitoring  # Your Prometheus namespace
  interval: 30s
  scrapeTimeout: 10s
```

## Helm Operations

### List Releases

```bash
helm list -n go-api
```

### Upgrade Release

```bash
helm upgrade go-api apps/go-api/deploy/helm/go-api
```

### Rollback Release

```bash
helm rollback go-api 1  # Rollback to previous release
```

### Uninstall Release

```bash
helm uninstall go-api -n go-api
```

### Dry Run (Preview Changes)

```bash
helm install go-api apps/go-api/deploy/helm/go-api --dry-run --debug
```

## Environment Variables

Configure via `values.yaml`:

```yaml
env:
  PORT: "8080"
  READINESS_FAILURE: "false"  # Simulate readiness failure for testing
  SHUTDOWN_TIMEOUT: "30s"
```

## Health Checks

- **Liveness Probe**: `/health` - Always returns OK
- **Readiness Probe**: `/ready` - Can be controlled via `READINESS_FAILURE` env var

## Volume Support (Future Enhancement)

Configure persistent storage by extending the Helm chart:

```yaml
# Add to values.yaml
persistence:
  enabled: false
  storageClass: local-path  # k3d default storage class
  size: 1Gi
```

## Troubleshooting

### Pods not starting

```bash
# Check pod events
kubectl describe pod -n go-api <pod-name>

# View logs
kubectl logs -n go-api <pod-name>
```

### Image not found

```bash
# List available images in k3d
k3d image list -c <cluster-name>

# Import image into k3d
k3d image import go-api:latest -c <cluster-name>
```

### Ingress not working

```bash
# Check ingress status
kubectl describe ingress -n go-api

# Check traefik pod logs
kubectl logs -n kube-system -l app=traefik
```

## Chart Structure

```
go-api/
├── Chart.yaml                 # Chart metadata
├── values.yaml               # Default values
├── templates/
│   ├── _helpers.tpl          # Template helpers/macros
│   ├── namespace.yaml        # Kubernetes Namespace
│   ├── deployment.yaml       # Deployment resource
│   ├── service.yaml          # Service resource
│   ├── ingress.yaml          # Ingress resource
│   ├── hpa.yaml              # HorizontalPodAutoscaler
│   ├── configmap.yaml        # ConfigMap (optional)
│   ├── secret.yaml           # Secret (optional)
│   └── servicemonitor.yaml   # ServiceMonitor for Prometheus Operator (optional)
└── charts/                   # Dependency charts (if any)
```

## Development

### Testing the Chart

```bash
# Validate chart syntax
helm lint apps/go-api/deploy/helm/go-api

# Generate manifest without installing
helm template go-api apps/go-api/deploy/helm/go-api > manifest.yaml

# Review generated manifests
cat manifest.yaml
```

### Updating the Chart

Edit the chart files and test with:

```bash
helm upgrade go-api apps/go-api/deploy/helm/go-api --dry-run --debug
```

## Notes

- The chart includes pod anti-affinity to spread pods across nodes
- Security context is configured for non-privileged containers
- Autoscaling is enabled by default but can be disabled in values
- The Ingress uses k3d's built-in Traefik controller
- Prometheus metrics are always exposed; monitoring integration is optional
