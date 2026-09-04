# R06 — Multi-Env Promotion (Dev → Staging → Prod)

**Scenario:** `env-promotion` · **Category:** delivery · **Runtimes:** k3d, kind

## What this teaches

A real release pipeline done by hand: build immutable, versioned images, deploy
one to `dev`, and promote that **same image** forward to `staging` then `prod`
using plain `kubectl`. Promotion here is a genuine **image rollout** (a new
ReplicaSet), not a ConfigMap edit. `labctl` only stands the scenario up and
**grades** the outcome — there is no `labctl env promote` command by design, so
the muscle memory you build (`kubectl set image`, `kubectl rollout status`,
`kubectl rollout undo`) is the muscle memory you use on a real cluster.

## Model

- Three environments = three namespaces: `env-dev`, `env-staging`, `env-prod`.
- Each runs `go-api` pinned to an **explicit** image tag (`go-api:v1.0.0`) —
  never `:latest`. The version is baked into the binary (`main.version` via
  `-ldflags`), so `/version` reports exactly the tag that is running.
- `env-metadata` ConfigMap in each namespace records `declared_tag` — the
  version that environment is *supposed* to run. The verify step asserts
  `declared_tag == running image` for every env.

## Walkthrough

```bash
# 0. Set up: deploys all three namespaces at the v1.0.0 baseline.
#    Stage 0 builds go-api:v1.0.0 and loads it into the cluster automatically.
labctl scenario up env-promotion
sudo labctl hosts add                      # go-api-dev/staging/prod.<domain>

# 1. Baseline — all three serve the SAME version.
for e in dev staging prod; do echo -n "$e: "; curl -s go-api-$e.<domain>/version; echo; done

# 2. Make a visible code change, then build a NEW version.
#    Edit `releaseNote` in apps/go-api/main.go (e.g. "add request logging").
bash scenarios/env-promotion/scripts/build-image.sh v1.1.0

# 3. Deploy to DEV only — a real rollout — and record the declared tag.
kubectl -n env-dev set image deployment/go-api go-api=go-api:v1.1.0
kubectl -n env-dev rollout status deployment/go-api
kubectl -n env-dev patch cm env-metadata --type=merge -p '{"data":{"declared_tag":"v1.1.0"}}'

# 4. Prove ONLY dev changed — this is exactly what a "promote" moves.
for e in dev staging prod; do echo -n "$e: "; curl -s go-api-$e.<domain>/version; echo; done

# 5. Promote the SAME image forward: dev → staging → prod.
kubectl -n env-staging set image deployment/go-api go-api=go-api:v1.1.0
kubectl -n env-staging rollout status deployment/go-api
kubectl -n env-staging patch cm env-metadata --type=merge -p '{"data":{"declared_tag":"v1.1.0","promoted_from":"dev"}}'

kubectl -n env-prod set image deployment/go-api go-api=go-api:v1.1.0
kubectl -n env-prod rollout status deployment/go-api
kubectl -n env-prod patch cm env-metadata --type=merge -p '{"data":{"declared_tag":"v1.1.0","promoted_from":"staging"}}'

# 6. Grade it.
labctl scenario verify env-promotion
```

## Rollback drill

```bash
kubectl -n env-prod rollout undo deployment/go-api
kubectl -n env-prod rollout status deployment/go-api
kubectl -n env-prod rollout history deployment/go-api
```

Note that after a rollback the running image no longer matches `declared_tag`,
so `labctl scenario verify` will flag the drift — which is the correct lesson:
a rollback is a deliberate divergence you then reconcile (re-promote, or update
the declared tag).

## Break-it-on-purpose

Edit only the ConfigMap and skip the rollout:

```bash
kubectl -n env-staging patch cm env-metadata --type=merge -p '{"data":{"declared_tag":"v1.2.0"}}'
labctl scenario verify env-promotion     # FAILS: declared=v1.2.0 but running=v1.1.0
```

This demonstrates why editing a config value is **not** a promotion — the
workload never changed.

## Verify checks

| Check | Meaning |
|-------|---------|
| `env-{dev,staging,prod}-namespace-exists` | Namespaces present |
| `go-api-running-in-{dev,staging,prod}` | Deployment has ≥1 ready replica |
| `declared-tag-matches-running-image` | **Invariant:** declared_tag == running image, all envs |
| `newer-release-reached-prod` (pending) | A newer-than-baseline release has reached prod |

## Teardown

```bash
labctl scenario down env-promotion       # removes env-dev/staging/prod namespaces
```
