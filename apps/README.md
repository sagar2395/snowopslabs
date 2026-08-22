# Applications

This directory contains the applications deployed to the lab cluster. Each app is self-contained with its source code, container definition, Helm chart, and configuration.

## Current Applications

| App | Language | Endpoints | Dependencies |
|-----|----------|-----------|-------------|
| **go-api** | Go 1.24 | `/health`, `/ready`, `/metrics`, `/toggle-failure`, `/` | None |
| **echo-server** | Go 1.24 | `/health`, `/ready`, `/echo`, `/cache`, `/metrics` | Redis (optional) |

## App Directory Structure

Each app follows this layout:

```
apps/<name>/
  app.env                  # Build/deploy configuration
  main.go                  # Application source
  go.mod / go.sum          # Go module files
  Dockerfile               # Multi-stage container build
  README.md                # App-specific documentation (optional)
  deploy/
    helm/
      Chart.yaml           # Helm chart metadata
      values.yaml          # Default chart values
      values-dev.yaml      # Local k3d profile
      values-prod-like.yaml # Production simulation profile
      values-cloud.yaml    # Cloud runtime profile (AKS/EKS)
      values-test.yaml     # CI testing profile
      templates/
        deployment.yaml
        service.yaml
        ingress.yaml
        hpa.yaml
        pdb.yaml
        ...
        tests/
          test-connection.yaml  # Helm test (curl /health)
```

## Configuration (`app.env`)

Every app must have an `app.env` file. This is the contract between the app and the build/deploy engine:

```bash
# Required
APP_NAME=my-app                # Must match directory name
BUILD_STRATEGY=docker          # docker | acr | ecr
DEPLOY_STRATEGY=helm           # helm

# Required for Helm
HELM_RELEASE_NAME=my-app       # Release name in cluster
HELM_VALUES=values-dev.yaml    # Which values file to use
# NAMESPACE=my-app             # K8s namespace (defaults to APP_NAME)
```

The `app.env` file is sourced by `engine/build.sh` and `engine/deploy.sh` to select strategy scripts.

**Runtime env vars** (like `PORT`, `REDIS_URL`) are documented at the bottom of `app.env` as comments but are NOT read by the engine. They are injected into the container via Helm values (`env:` section in `values-*.yaml`).

## Build and Deploy

### Using Make

```bash
# Build container image
make build APP_NAME=go-api

# Deploy to cluster
make deploy APP_NAME=go-api

# Remove from cluster
make destroy-app APP_NAME=go-api

# Deploy all apps
make deploy-all

# Lint Helm chart
make lint APP_NAME=go-api

# Validate Helm templates
make validate APP_NAME=go-api
```

### Using labctl

```bash
labctl app list
labctl app build go-api
labctl app deploy go-api
labctl app destroy go-api
```

### Helm Test

```bash
helm test go-api -n go-api
```

## Helm Value Profiles

| Profile | Ingress | Replicas | HPA | Probes | Use Case |
|---------|---------|----------|-----|--------|----------|
| `values-dev.yaml` | traefik, `*.k3d.local` | 1 | No | Default | Local development |
| `values-prod-like.yaml` | traefik, `*.k3d.local` | 3 | Yes (3-10) | Tuned | Production testing |
| `values-cloud.yaml` | nginx, `*.cloud.local` | 2 | Yes (2-10) | Tuned | AKS/EKS deployment |
| `values-test.yaml` | traefik, `*.k3d.local` | 1 | No | Fast | CI/CD testing |

Switch profiles by changing `HELM_VALUES` in `app.env`.

## Adding a New Application

1. **Create directory**:
   ```bash
   mkdir -p apps/my-app/deploy/helm/templates
   ```

2. **Write source code** (`main.go` or your language of choice):
   - Expose `/health` for liveness probes
   - Expose `/ready` for readiness probes
   - Expose `/metrics` for Prometheus (optional)
   - Handle `SIGTERM` for graceful shutdown

3. **Create `Dockerfile`** (multi-stage recommended):
   ```dockerfile
   FROM golang:1.24-alpine AS builder
   WORKDIR /app
   COPY go.mod go.sum ./
   RUN go mod download
   COPY . .
   RUN CGO_ENABLED=0 go build -o /app/server .

   FROM alpine:latest
   RUN addgroup -S appgroup && adduser -S appuser -G appgroup
   COPY --from=builder /app/server /app/server
   USER appuser
   EXPOSE 8080
   CMD ["/app/server"]
   ```

4. **Create `app.env`**:
   ```bash
   APP_NAME=my-app
   BUILD_STRATEGY=docker
   DEPLOY_STRATEGY=helm
   HELM_RELEASE_NAME=my-app
   HELM_VALUES=values-dev.yaml
   ```

5. **Create Helm chart** (copy from `go-api/deploy/helm/` and customize):
   - Update `Chart.yaml` with your app name
   - Update `values-dev.yaml` with your app's port, image, and ingress host
   - Update templates if your app has different requirements

6. **Build and deploy**:
   ```bash
   make build APP_NAME=my-app
   make deploy APP_NAME=my-app
   curl http://my-app.k3d.local/health
   ```

The engine auto-discovers any directory in `apps/` that contains an `app.env` file.

## go-api

A Go HTTP server with health checks, metrics, OpenTelemetry tracing, and failure simulation.

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/` | GET | Service info (name, version, uptime) |
| `/health` | GET | Liveness probe (always 200) |
| `/ready` | GET | Readiness probe (toggleable via `/toggle-failure`) |
| `/metrics` | GET | Prometheus metrics |
| `/toggle-failure` | POST | Toggle readiness failure for testing |

**Features:** Structured JSON logging (slog), OpenTelemetry tracing, Prometheus labeled metrics, graceful shutdown with configurable timeout.

See [go-api/README.md](go-api/README.md) for details.

## echo-server

A Go HTTP server that echoes request details and provides Redis-backed caching.

| Endpoint | Method | Description |
|----------|--------|-------------|
| `/health` | GET | Liveness probe (always 200) |
| `/ready` | GET | Readiness probe (checks Redis if configured) |
| `/echo` | ANY | Echoes request details (headers, method, body) |
| `/cache` | GET/POST/DELETE | Redis key-value cache operations |
| `/metrics` | GET | Prometheus metrics |

**Features:** Structured JSON logging, Prometheus labeled metrics, optional Redis integration, graceful shutdown.

**Dependencies:** Redis (optional). Install via `labctl service up redis` or `make service-up SVC=redis`.

## Security

Both apps follow these security practices:

- **Non-root containers**: Run as `appuser:appgroup` (UID 65534)
- **Read-only filesystem**: `readOnlyRootFilesystem: true`
- **All capabilities dropped**: `capabilities.drop: [ALL]`
- **Graceful shutdown**: Handle SIGTERM with configurable timeout
- **No secrets in images**: Configuration via environment variables
