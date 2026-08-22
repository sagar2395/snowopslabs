# Module 1 — Start the cluster

## What you'll do

Spin up a local k3d Kubernetes cluster. This is the base for everything
in the lab — all platform components, apps, and scenarios run here.

## Background

k3d wraps k3s (a lightweight Kubernetes distribution) in Docker containers.
Your lab is fully contained: no cloud account needed, and you can tear it
down with a single command.

## Objective

Run `labctl runtime up` and verify the cluster is reachable.

```bash
bin/labctl runtime up
kubectl cluster-info
```

**Completion check:** `kubectl cluster-info` exits 0 (the API server is
reachable).
