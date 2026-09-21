# Applications

This directory contains the applications deployed to the lab cluster. Each app is self-contained with its source code, container definition, Helm chart, and configuration.

## Current Applications

| App | Language | Endpoints | Dependencies |
|-----|----------|-----------|-------------|
| **go-api** | Go 1.24 | `/health`, `/ready`, `/metrics`, `/toggle-failure`, `/` | None |
| **echo-server** | Go 1.24 | `/health`, `/ready`, `/echo`, `/cache`, `/metrics` | Redis (optional) |
| **java-api** | Java 21 | `/health`, `/ready`, `/metrics`, `/toggle-failure`, `/` | None |

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

**Rebuilds under a mutable tag.** `labctl app deploy` threads the local image
ID into a pod annotation (`snowops.net/image-id`). Without it, rebuilding under
an unchanged tag leaves the Deployment spec identical, `helm upgrade` is a no-op,
and the running pods keep serving the previous build while deploy reports
success. A chart brought by a BYO app should carry the same annotation — set
`image.id` on the pod template — or iterating on that app will appear to do
nothing.

**`APP_VERSION`** is part of the workload contract: when it is set, the app must
report it as the version on `/version`, overriding any build-time stamp. Content
relies on this to run two Deployments of one image as distinct versions — a mesh
canary splits on it, and `/version` is how a learner sees which subset served
them. Both reference apps honour it (`go-api`, `java-api`).

## The workload contract

`app.env` also declares the **contract**: the interface this app promises a
scenario. It is what lets one scenario run against many applications — the
scenario states what it needs, rather than naming an app. See
[ADR-0014](../docs/adr/0014-workload-binding-and-app-contract.md).

```bash
# === Workload contract ===
APP_PORT=8080
# APP_HEALTH_PATH=/health        # optional; these are the defaults
# APP_READY_PATH=/ready
# APP_METRICS_PATH=/metrics
APP_REQUEST_METRIC=http_server_request_duration_seconds
APP_CAPABILITIES=prometheus-metrics,otlp-tracing,readiness-toggle
```

| Key | Default | Meaning |
|---|---|---|
| `APP_IMAGE` | *(none)* | Published image, required by `BUILD_STRATEGY=image` |
| `APP_WRITABLE_PATHS` | `/tmp` | Extra paths to keep writable under the shared chart |
| `APP_PORT` | `8080` | Port serving HTTP |
| `APP_HEALTH_PATH` | `/health` | Liveness probe path |
| `APP_READY_PATH` | `/ready` | Readiness probe path |
| `APP_METRICS_PATH` | `/metrics` | Prometheus exposition path |
| `APP_REQUEST_METRIC` | `http_server_request_duration_seconds` | The request-duration histogram's name |
| `APP_CAPABILITIES` | *(none)* | Optional behaviours this app claims |

Only `APP_CAPABILITIES` has no default. The base interface is assumed; a
capability is claimed explicitly or the app does not have it.

`APP_REQUEST_METRIC` is **named rather than assumed**, because each language's
instrumentation library picks its own — Micrometer emits
`http_server_requests_seconds`, prom-client emits whatever you configure. The
contract mandates the OpenTelemetry semantic-convention name for new apps, so an
already-instrumented app arrives close to conformant.

> Semconv defines **no separate request counter**. The histogram's `_count`
> series is the request count, and `http_response_status_code` is one of its
> labels, so an error rate derives from the same series:
> `rate(http_server_request_duration_seconds_count{http_response_status_code=~"5.."}[5m])`.

### Capabilities

Run `labctl app capabilities` for the current list.

| Capability | The app… |
|---|---|
| `prometheus-metrics` | serves the metrics path in Prometheus format, including the request-duration histogram |
| `otlp-tracing` | honours `OTEL_EXPORTER_OTLP_ENDPOINT` and exports spans |
| `readiness-toggle` | can have its readiness flipped to failing on demand |

Claim only what the app truly provides. A scenario requiring a capability the
bound app does not declare is refused at preflight, before anything installs —
which is the point. An overclaim turns that clear refusal into a confusing
failure halfway through a scenario.

### Verifying an app

```bash
labctl app verify go-api            # declaration, then evidence from the cluster
labctl app verify go-api --static   # declaration only; no cluster needed
```

A declaration is only a claim. `verify` runs the checks each claim implies
against the deployed workload — readiness, the contract port, the probe paths,
and (for `prometheus-metrics`) a Prometheus query proving the declared histogram
is actually being scraped. An app is only checked for what it claims.

`otlp-tracing` and `readiness-toggle` are reported as *declared, not
machine-checked*: both are conditional promises, so a baseline deployment looks
identical whether or not the app honours them.

## Bringing your own application

Two ways in. Both end at the same place: an app that declares a contract and
that scenarios can bind to.

### With source (the built-in apps' path)

Drop a directory into `apps/` with source, a `Dockerfile`, an `app.env` and a
Helm chart under `deploy/helm/`. `BUILD_STRATEGY=docker` builds the image and
imports it into the cluster. This is how `go-api` and `echo-server` work — see
[Adding a New Application](#adding-a-new-application).

### With a pre-built image only

No source, no Dockerfile, no chart. Declare the image and the contract, and the
lab deploys it with the shared workload chart at `apps/_shared/chart`:

```bash
# apps/checkout-api/app.env
APP_NAME=checkout-api
BUILD_STRATEGY=image
DEPLOY_STRATEGY=helm
HELM_RELEASE_NAME=checkout-api
APP_IMAGE=ghcr.io/acme/checkout-api:1.4.2

APP_PORT=8080
APP_HEALTH_PATH=/healthz
APP_READY_PATH=/readyz
APP_REQUEST_METRIC=http_server_request_duration_seconds
APP_CAPABILITIES=prometheus-metrics
```

```bash
labctl app build checkout-api    # pulls the image and imports it into the cluster
labctl app deploy checkout-api   # deploys it with the shared chart
labctl app verify checkout-api   # confirms it honours what it declared
```

`BUILD_STRATEGY=image` pulls `APP_IMAGE` and imports it under **the reference it
was published as**. Retagging it locally produces a name whose manifest digest
the cluster cannot resolve, and every pod then fails to start with the image
apparently present.

The shared chart is used automatically whenever `apps/<name>/deploy/helm` does
not exist. Everything it needs comes from the contract — port, probe paths,
metrics path and the `app: <name>` label that Prometheus relabels onto every
series — so the deployed pod and the declaration cannot disagree.

**Writable paths.** The shared chart runs your image with a read-only root
filesystem, as a non-root user, with all capabilities dropped. `/tmp` is mounted
writable because nearly every runtime needs it. If your image needs more, say
so — this is the most common reason a hardened image crashloops:

```bash
APP_WRITABLE_PATHS=/var/cache/nginx,/run
```

**Declare only what your app really does.** An app that claims nothing still
runs; it is simply refused by scenarios that need something it did not promise,
with a message naming the gap. That refusal is the feature — it happens before
anything installs, rather than as a confusing failure halfway through.

### Binding a scenario to an app

`APP_NAME` selects the app that scenarios and faults run against:

```bash
APP_NAME=echo-server labctl scenario info observability-sre
```

Content resolves the binding through `{{.WorkloadName}}` and friends, and the
port and metric come from the app's own contract — see the
[scenario schema](../docs/reference/scenario-schema.md#the-workload-variables).

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

4. **Create `app.env`**, including the [workload contract](#the-workload-contract):
   ```bash
   APP_NAME=my-app
   BUILD_STRATEGY=docker
   DEPLOY_STRATEGY=helm
   HELM_RELEASE_NAME=my-app
   HELM_VALUES=values-dev.yaml

   APP_PORT=8080
   APP_REQUEST_METRIC=http_server_request_duration_seconds
   APP_CAPABILITIES=prometheus-metrics
   ```
   Then confirm the app honours what it declares:
   ```bash
   labctl app verify my-app
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

## java-api

A second technology stack, so a scenario can be run against something that is not
Go and the two compared. Its JVM characteristics are the point: ~50Mi resident at
rest against the Go services' ~10Mi, plus slower start and GC pauses.

It has **no Helm chart of its own** — the shared workload chart deploys it from
its declared contract, which is the same path a user's own application takes. It
also has no build tool: the app is dependency-free and compiles with `javac`, so
the lab stays offline and fast.

See [java-api/README.md](java-api/README.md), including how the same metric maps
onto Micrometer for a real Spring service.

## Security

Both apps follow these security practices:

- **Non-root containers**: Run as `appuser:appgroup` (UID 65534)
- **Read-only filesystem**: `readOnlyRootFilesystem: true`
- **All capabilities dropped**: `capabilities.drop: [ALL]`
- **Graceful shutdown**: Handle SIGTERM with configurable timeout
- **No secrets in images**: Configuration via environment variables
