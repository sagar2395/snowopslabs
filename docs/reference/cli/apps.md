# Applications & services

## `labctl app`

Applications live in `apps/<name>/`, each with an `app.env` declaring its
`BUILD_STRATEGY`, `DEPLOY_STRATEGY` and `HELM_VALUES`. The CLI reads that file
and runs the matching strategy — it never hardcodes how an app is built.

```bash
labctl app list              # discovered apps with their build/deploy strategies
labctl app build go-api      # build the container image
labctl app deploy go-api     # deploy to the cluster
labctl app destroy go-api    # remove it
```

REST: `GET /api/v2/apps`, `GET /api/v2/apps/{name}/detail`,
`POST /api/v2/apps/{name}/{build,deploy,destroy}`.

## `labctl service`

Shared services that apps depend on — Redis and friends. They live outside the
platform categories because they are application dependencies, not platform
capability.

```bash
labctl service list             # available shared services
labctl service up redis         # install
labctl service down redis       # uninstall
labctl service status           # all services
labctl service status redis     # one service
```
