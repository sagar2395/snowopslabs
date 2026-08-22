# Module 4 — Your first incident

## What you'll do

Inject a real production fault (`service-selector-broken`) and fix it by
hand. This is a classic Kubernetes gotcha: everything looks healthy, but
the service has no endpoints because the selector doesn't match any pods.

## Background

The incident engine injects faults via shell scripts that mutate live
Kubernetes resources, records your time-to-detect and MTTR, and confirms
resolution through a machine-verifiable check.

## Objective

Inject the fault, diagnose it, fix it, and confirm resolution.

```bash
# Inject
bin/labctl incident inject service-selector-broken
curl http://go-api.k3d.local/health   # 503 — broken

# Diagnose
kubectl get pods -n go-api            # healthy — suspicious
kubectl get endpoints go-api -n go-api   # <none> — there's your lead

# Fix
kubectl -n go-api patch svc go-api \
  -p '{"spec":{"selector":{"app.kubernetes.io/name":"go-api"}}}'

# Verify
bin/labctl incident status            # RESOLVED
```

**Completion check:** `labctl incident status` reports RESOLVED or no
active incident.

**Note:** use `bin/labctl incident hint` if you are stuck, and
`bin/labctl incident resolve` as the escape hatch.
