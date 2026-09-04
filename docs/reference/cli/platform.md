# Platform

Platform components are the infrastructure the lab runs on — ingress,
monitoring, logging, tracing, mesh, data, secrets, security, autoscaling, cost,
gitops, chaos and the dashboard. Each lives in
`platform/<category>/<component>/` with a single `values.yaml` and a chart
version pinned in `config/versions.env`.

A **target** is either a category (whose provider comes from an env var, or is
the only one) or an explicit `category/provider` for additive categories such
as `data`.

```bash
labctl platform up                          # the full stack
MESH_PROVIDER=istio labctl platform up mesh # a category — provider from the env var
labctl platform up data/kafka               # a specific provider (additive category)
labctl platform up data/postgres
```

```bash
labctl platform down                        # uninstall everything
MESH_PROVIDER=istio labctl platform down mesh
labctl platform down data/kafka
```

```bash
labctl platform status                      # from the run history — fast
labctl platform status --live               # probe the cluster via each provider's status.sh
labctl platform status mesh
labctl platform status data/postgres
```

Which components install is driven by the provider variables in `.env` —
`INGRESS_PROVIDER`, `METRICS_PROVIDER`, `LOGGING_PROVIDER`, `TRACING_PROVIDER`,
`MESH_PROVIDER` and so on. When a category has several providers and none is
selected, the command lists the choices and names the variable to set.

## `labctl platform teardown`

Removes exactly the components the inventory records as installed, in reverse
order, and reports anything it could not remove instead of exiting 0 and hoping.
Each uninstall is a recorded, cancellable run. Components installed outside
labctl are not in the inventory and are left alone.

```bash
labctl platform teardown
```

REST: `GET /api/v2/platform`, `POST /api/v2/platform/{up,down}`,
`GET /api/v2/platform/component/{category}/{name}`.

Adding a component — including the CRD and StatefulSet traps that will bite you
— is [R05](../../runbooks/R05-platform-components.md).
