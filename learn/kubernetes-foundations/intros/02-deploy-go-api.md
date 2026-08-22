# Module 2 — Deploy go-api

## What you'll do

Build and deploy the `go-api` demo service — a small Go HTTP API that
exposes `/health`, `/metrics`, and a handful of endpoints used by later
scenarios.

## Background

The lab uses a Helm-based deploy strategy. `labctl app deploy` calls
`engine/helm/deploy.sh`, which runs `helm upgrade --install` in the app's
namespace. The app's container image is built locally via Docker buildx
and loaded directly into the cluster.

## Objective

Build and deploy go-api, then confirm it is reachable through the ingress.

```bash
bin/labctl app build go-api
bin/labctl app deploy go-api
curl http://go-api.k3d.local/health
```

**Completion check:** `GET http://go-api.${DOMAIN_SUFFIX:-k3d.local}/health`
returns HTTP 200.
