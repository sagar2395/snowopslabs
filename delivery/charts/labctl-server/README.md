# labctl-server Helm chart

Run the SnowOps Labs (`labctl`) simulator **inside a Kubernetes cluster** so a team
can share one lab for a game day: a shared web UI/API, authenticated users with
roles, a leaderboard, and history persisted on a PVC.

> Part of task 063 (team mode). For the local single-user experience just run
> `labctl ui` — you do not need this chart.

## What it deploys

| Resource | Purpose |
|---|---|
| Deployment (1 replica) | the labctl server (`labctl ui`) in in-cluster mode |
| Service | ClusterIP on port 3939 |
| Ingress (optional) | external access |
| PersistentVolumeClaim | persists `/app/.labctl` (history, scores, snapshots) |
| ServiceAccount + Role/ClusterRole | what the orchestration scripts need |
| Secret (optional) | `users.yaml` for authentication |

## Image

The server image bundles the repo **and** `helm`/`kubectl` (the CLI orchestrates
by shelling out to scripts), so it is intentionally larger than a single-binary
image. Build it from the repo root:

```bash
docker build -f Dockerfile.labctl-server -t <registry>/labctl-server:<tag> .
docker push <registry>/labctl-server:<tag>
```

## Install

```bash
# 1. Generate user password hashes locally
labctl users add alice --role operator   --password 'op-pw'
labctl users add bob   --role participant --password 'part-pw'
#    copy the hashes from .labctl/users.yaml into a values file:

cat > game-day.yaml <<'EOF'
image:
  repository: <registry>/labctl-server
  tag: <tag>
auth:
  enabled: true
  users:
    - name: alice
      role: operator
      passwordHash: "pbkdf2-sha256$210000$...$..."
    - name: bob
      role: participant
      passwordHash: "pbkdf2-sha256$210000$...$..."
ingress:
  enabled: true
  className: traefik
  host: labctl.your-cluster.example
EOF

# 2. Install
helm install labctl-server delivery/charts/labctl-server \
  -n labctl --create-namespace -f game-day.yaml
```

Without an ingress, reach the UI via port-forward:

```bash
kubectl -n labctl port-forward svc/labctl-server 3939:3939
```

## RBAC

`rbac.clusterWide=false` (default) creates a **namespaced** Role — enough to run
scenarios/incidents that deploy into the release namespace. To install platform
components cluster-wide (CRDs, namespaces) from the UI, set
`rbac.clusterWide=true`. That binds a broad ClusterRole — **review before using
on a shared cluster.**

## Persistence

`/app/.labctl` is backed by the PVC, so the results history and leaderboard
survive pod restarts. `podSecurityContext.fsGroup` makes the volume writable by
the non-root container user. Disable with `persistence.enabled=false` (history
then lives only for the pod's lifetime).

## Values

See `values.yaml` for the full list. Common ones: `image.*`, `auth.*`,
`persistence.*`, `rbac.clusterWide`, `ingress.*`, `env` (extra env vars such as
`DOMAIN_SUFFIX`, `MONITORING_NAMESPACE` to match the hosting cluster).
