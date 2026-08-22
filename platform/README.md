# Platform Components

The `platform/` directory contains infrastructure components organized by category. Each component is a provider that can be swapped independently.

## How It Works

Components follow a standard layout:

```
platform/<category>/<provider>/
  install.sh       # Install via Helm (idempotent)
  uninstall.sh     # Remove via Helm + cleanup CRDs
  status.sh        # Health check and status output
  values.yaml      # Helm chart configuration
```

The active provider for each category is selected via environment variables in `.env`:

```bash
INGRESS_PROVIDER=traefik       # or nginx
METRICS_PROVIDER=prometheus
```

Each category also has an `_interface.yaml` file documenting the contract that all providers in that category must satisfy.

## Installation

```bash
# Install all platform components
make platform-up
# or
labctl platform up

# Check status
make platform-status
# or
labctl platform status

# Remove all
make platform-down
# or
labctl platform down
```

## Components by Category

### Ingress

Controls external traffic routing into the cluster.

| Provider | Chart | Description |
|----------|-------|-------------|
| **traefik** | `traefik/traefik` | Default for k3d. LoadBalancer service, API dashboard |
| **nginx** | `ingress-nginx/ingress-nginx` | Default for cloud. Admission webhooks, ServiceMonitor |

Switch: `INGRESS_PROVIDER=nginx` in `.env`, then `make platform-up`.

### Monitoring

#### Metrics (`monitoring/metrics/`)

| Provider | Chart | Description |
|----------|-------|-------------|
| **prometheus** | `prometheus-community/kube-prometheus-stack` | Prometheus Operator, Node Exporter, Kube-State-Metrics, Alertmanager |

See [monitoring/README.md](monitoring/README.md) for detailed setup and verification.

#### Visualization (`monitoring/grafana/`)

| Provider | Chart | Description |
|----------|-------|-------------|
| **grafana** | `grafana/grafana` | Auto-provisioned Prometheus datasource, dashboard sidecar, 5Gi PVC |

Access: `http://grafana.k3d.local` (admin/admin)

### GitOps (`gitops/`)

| Provider | Chart | Description |
|----------|-------|-------------|
| **argocd** | `argo/argo-cd` | GitOps continuous delivery. Traefik ingress at `argocd.k3d.local` |

Activated via the `gitops-cicd` scenario or manually.

### Security (`security/`)

| Subcategory | Provider | Chart | Description |
|-------------|----------|-------|-------------|
| Policy | **kyverno** | `kyverno/kyverno` | Policy enforcement (admission control) |
| TLS | **cert-manager** | `jetstack/cert-manager` | Certificate management with self-signed CA |
| Secrets | **sealed-secrets** | `sealed-secrets/sealed-secrets` | Encrypted secrets in Git |
| Network | **kubernetes-native** | N/A | NetworkPolicy manifests (default-deny + explicit allows) |

These are typically activated via the `security-compliance` scenario.

### Chaos (`chaos/`)

| Provider | Chart | Description |
|----------|-------|-------------|
| **chaos-mesh** | `chaos-mesh/chaos-mesh` | Failure injection (pod kill, network delay, stress) |

Activated via the `chaos-engineering` scenario. Includes a web dashboard (port-forward to 2333).

### Mesh (`mesh/`)

Service mesh providing mTLS, traffic management, and L7 telemetry between meshed
workloads. Swappable via `MESH_PROVIDER`.

| Provider | Charts | Inject marker | Sidecar |
|----------|--------|---------------|---------|
| **istio** | `istio/base`, `istio/istiod` | namespace label `istio-injection=enabled` | `istio-proxy` |
| **linkerd** | `linkerd/linkerd-crds`, `linkerd/linkerd-control-plane` | namespace annotation `linkerd.io/inject=enabled` | `linkerd-proxy` |

`install.sh` enrols the workload namespace (`MESH_NAMESPACE`, default `go-api`)
into the mesh and restarts its deployments so sidecars are injected;
`uninstall.sh` reverses both. Chart versions are pinned in `versions.env`
(`ISTIO_VERSION`, `LINKERD_CRDS_CHART_VERSION`,
`LINKERD_CONTROL_PLANE_CHART_VERSION`) and overridable per-install.

```bash
# Install / swap / remove on the same cluster:
MESH_PROVIDER=istio   labctl platform up mesh
MESH_PROVIDER=istio   labctl platform down mesh
MESH_PROVIDER=linkerd labctl platform up mesh
```

> **k3d resource reality:** give the cluster at least 4Gi memory (6Gi is
> comfortable with a meshed app running). Linkerd needs `openssl` on the host
> for portable mTLS identity cert generation — no `step` CLI required.

### Data (`data/`)

Stateful data infrastructure driven by Kubernetes operators. Unlike most
categories these are **additive sub-components** (like `monitoring/*`): `kafka`
and `postgres` coexist. Address each as `data/<provider>`.

| Provider | Operator chart | Cluster | Ready signal |
|----------|----------------|---------|--------------|
| **kafka** | `strimzi/strimzi-kafka-operator` | Kafka (KRaft) + KafkaNodePool, 1 dual-role node, ephemeral | `kafka/<name>` condition `Ready=True` |
| **postgres** | `cnpg/cloudnative-pg` | `Cluster`, 2 instances (1 primary + 1 replica) | `readyInstances == spec.instances` |

Each provider owns its own namespace (kafka → `kafka`, postgres → `postgres`;
CNPG's operator lives in `cnpg-system`) so installing/removing one never
disturbs the other. Chart/app versions are pinned in `versions.env`
(`STRIMZI_VERSION`, `KAFKA_VERSION`, `CNPG_CHART_VERSION`) and overridable.

```bash
labctl platform up   data/kafka       # Strimzi operator + 1-broker Kafka
labctl platform up   data/postgres    # CNPG operator + 2-instance Postgres
labctl platform status data/postgres  # CR readiness + per-pod roles
labctl platform down data/kafka       # remove operator + CR + PVCs + namespace
# or via make: make platform-data-up   (both)   /  make platform-data-kafka-up
```

CNPG's built-in failover is a ready-made day-2 drill — delete the primary pod
and watch a replica promote (see the runbook). Kafka ships a kcat
produce/consume smoke test in the runbook.

### Secrets Management (`secrets/`)

Centralised secrets: a **Vault** backend plus the **External Secrets Operator
(ESO)** that syncs Vault values into native Kubernetes Secrets. Selected by
`SECRETS_PROVIDER`, or addressed explicitly as `secrets/<provider>`.

> **Not** to be confused with `security/secrets/` (sealed-secrets) — a different,
> Git-encryption approach activated via the `security-compliance` scenario.

| Provider | Chart | Role |
|----------|-------|------|
| **vault** | `hashicorp/vault` | Secrets backend (dev mode for the lab; seeds `secret/go-api`, UI via ingress) |
| **external-secrets** | `external-secrets/external-secrets` | Syncs `secret/go-api` → `ExternalSecret` → k8s Secret `go-api-secrets` |

ESO **prerequires** Vault — its `install.sh` preflights for the Vault service and
errors (rather than auto-installing) if it's missing. The full chain demonstrates
`Vault KV → ExternalSecret → k8s Secret → go-api env var`, and rotating the value
in Vault propagates within the 15s refresh interval (the rotation exercise).

```bash
labctl platform up secrets/vault            # backend + demo secret + UI
labctl platform up secrets/external-secrets # operator + SecretStore + ExternalSecret
# or both, in order:  make platform-secrets-up
labctl platform status secrets/external-secrets
labctl platform down secrets/external-secrets && labctl platform down secrets/vault
```

> **No secrets in git, ever.** The Vault dev root token comes from the
> environment (`VAULT_DEV_ROOT_TOKEN`, defaulting to Vault's well-known dev value
> `root`); the token ESO uses is created in-cluster from that env value. Dev-mode
> Vault is in-memory and wiped on restart — rotation, not durability, is the point.
> Versions are pinned in `versions.env` (`VAULT_CHART_VERSION`, `ESO_CHART_VERSION`).

### Autoscaling (`autoscaling/`)

Event/metric-driven horizontal autoscaling beyond CPU/memory HPA. Selected by
`AUTOSCALING_PROVIDER`.

| Provider | Chart | Drives |
|----------|-------|--------|
| **keda** | `kedacore/keda` | A `ScaledObject` → HPA, scaling on Prometheus queries, Kafka lag, queue depth, … |

The provider installs only the autoscaler; the **scaling rule** (a `ScaledObject`)
is declared by scenarios/apps, not the provider. The flagship demo is the
`autoscaling-under-load` scenario: a traffic spike drives go-api from 1 to
several replicas on Prometheus RPS, then cooldown scales it back.

```bash
AUTOSCALING_PROVIDER=keda labctl platform up autoscaling
labctl scenario up autoscaling-under-load        # installs the ScaledObject + dashboard
labctl traffic start --profile spike --rps 10    # drive the spike
labctl scenario verify autoscaling-under-load    # asserts the scaled-up state
AUTOSCALING_PROVIDER=keda labctl platform down autoscaling
```

Version pinned in `versions.env` (`KEDA_CHART_VERSION`), overridable per-install.

### Cost Visibility (`cost/`)

Per-namespace and per-workload cost estimation using Prometheus resource metrics
and configurable pricing models. Selected by `COST_PROVIDER`. Requires the
`monitoring/metrics` Prometheus to be installed first.

| Provider | Chart | Description |
|----------|-------|-------------|
| **opencost** | `opencost/opencost` | Real-time cost monitoring; UI on port 9090; on-prem pricing defaults for k3d |

```bash
labctl platform up monitoring/metrics   # OpenCost reads from Prometheus
COST_PROVIDER=opencost labctl platform up cost
kubectl -n opencost port-forward svc/opencost 9090 &   # open the UI
labctl scenario up cost-right-sizing    # inflate go-api requests to demonstrate cost
labctl scenario verify cost-right-sizing   # fails while over-provisioned
kubectl -n go-api set resources deployment go-api --requests=cpu=50m,memory=32Mi
labctl scenario verify cost-right-sizing   # passes after right-sizing
COST_PROVIDER=opencost labctl platform down cost
```

> **k3d note:** no real billing API is wired — OpenCost uses on-prem pricing
> defaults (~$0.048/CPU-hr). Cost numbers are relative, not real invoices.

Version pinned in `versions.env` (`OPENCOST_CHART_VERSION`), overridable per-install.

## Provider Interface Contracts

Each category has an `_interface.yaml` documenting:

```yaml
category: ingress
description: Routes external HTTP/HTTPS traffic to cluster services
provides:
  - IngressClass resource
  - LoadBalancer or NodePort service
requires:
  - Kubernetes cluster
env_vars:
  INGRESS_CLASS: traefik | nginx
implementations:
  - name: traefik
    chart: traefik/traefik
  - name: nginx
    chart: ingress-nginx/ingress-nginx
```

## Adding a New Provider

1. Create directory: `platform/<category>/<provider-name>/`
2. Create the four required files: `install.sh`, `uninstall.sh`, `status.sh`, `values.yaml`
3. Follow the `_interface.yaml` contract for the category
4. Update `_interface.yaml` to list the new implementation
5. The CLI's platform registry will auto-discover it

### Script Template

```bash
#!/usr/bin/env bash
set -euo pipefail

NAMESPACE="${NAMESPACE:-my-namespace}"
RELEASE_NAME="my-provider"
CHART="repo/chart-name"

echo "Installing $RELEASE_NAME..."
helm repo add myrepo https://charts.example.com
helm repo update

kubectl create namespace "$NAMESPACE" --dry-run=client -o yaml | kubectl apply -f -

helm upgrade --install "$RELEASE_NAME" "$CHART" \
  --namespace "$NAMESPACE" \
  --values "$(dirname "$0")/values.yaml" \
  --wait --timeout 5m

echo "$RELEASE_NAME installed."
```

## Directory Structure

```
platform/
  ingress/
    _interface.yaml
    traefik/              install.sh, uninstall.sh, status.sh, values.yaml
    nginx/                install.sh, uninstall.sh, status.sh, values.yaml
  monitoring/
    README.md             Detailed monitoring setup guide
    metrics/
      _interface.yaml
      prometheus/         install.sh, uninstall.sh, status.sh, values.yaml
    grafana/
      _interface.yaml
      install.sh, uninstall.sh, status.sh, values.yaml
  gitops/
    _interface.yaml
    argocd/               install.sh, uninstall.sh, status.sh, values.yaml
  security/
    policy/
      _interface.yaml
      kyverno/            install.sh, uninstall.sh, status.sh, values.yaml
    tls/
      _interface.yaml
      cert-manager/       install.sh, uninstall.sh, status.sh, values.yaml, cluster-issuer.yaml
    secrets/
      _interface.yaml
      sealed-secrets/     install.sh, uninstall.sh, status.sh, values.yaml
    network-policies/
      _interface.yaml
      install.sh, uninstall.sh, status.sh
      default-deny.yaml, allow-dns.yaml, allow-monitoring.yaml, allow-ingress.yaml
  chaos/
    _interface.yaml
    chaos-mesh/           install.sh, uninstall.sh, status.sh, values.yaml
```
