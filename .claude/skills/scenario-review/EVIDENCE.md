# Evidence recipes

The commands each phase runs. Everything here reads the live lab; nothing here
changes a scenario. Set the environment once:

```sh
SCENARIO=mesh-traffic-management
DOMAIN_SUFFIX=k3d.local                       # labctl status prints the profile
PROM=http://prometheus.$DOMAIN_SUFFIX
GRAF=http://grafana.$DOMAIN_SUFFIX
NOTES=$SCRATCHPAD/review-$SCENARIO.md         # every command and output lands here
```

Grafana's admin credentials come from the cluster, never from a guess:

```sh
kubectl -n monitoring get secret grafana -o jsonpath='{.data.admin-user}' | base64 -d
kubectl -n monitoring get secret grafana -o jsonpath='{.data.admin-password}' | base64 -d
```

---

## P1 — static

```sh
./bin/labctl validate
./bin/labctl scenario info "$SCENARIO"

# every helm component's version is pinned in config/versions.env
grep -n 'version:' "scenarios/$SCENARIO/scenario.yaml"
grep -n -i "$SCENARIO" docs/scenarios.md          # catalog entry exists
```

## P2 — cold activation

```sh
./bin/labctl scenario down "$SCENARIO" || true
time ./bin/labctl scenario up "$SCENARIO" --deploy-prereqs
./bin/labctl scenario verify "$SCENARIO" --watch --timeout 10m
```

Record the wall time and every prompt, error or manual fix. A scenario that
needs a step it never documents has a P2 defect even when it ends green.

## P3 — signal and graph

Load first, or every panel is honestly empty:

```sh
./bin/labctl traffic start --profile browse --rps 20
./bin/labctl traffic status
```

Does the claimed metric exist and have samples?

```sh
curl -s --get "$PROM/api/v1/query" --data-urlencode 'query=count(istio_requests_total)'
curl -s --get "$PROM/api/v1/query" \
  --data-urlencode 'query=sum by (destination_version) (rate(istio_requests_total[5m]))'
```

An empty `result: []` is the finding. Read
[R13](../../../docs/runbooks/R13-observability-pipeline.md) before blaming the
scenario — an empty `kube-prometheus-stack` selector or a missed relabel is
usually the cause.

Is it *plotted*, and is the panel populated? Pull the dashboard the scenario
links and probe each panel's own query:

```sh
UID=app-requests                               # from the explore.urls link
curl -s -u admin:"$GRAFANA_PW" "$GRAF/api/dashboards/uid/$UID" \
  | jq -r '[.. | objects | select(has("expr")) | .expr] | .[]'
```

Substitute any `$variable` in an expression with a concrete value (or `.*`)
before sending it to Prometheus — a template variable is not valid PromQL. Any
panel query returning no series under the scenario's own traffic is a
dimension-3 finding: the learner opens that dashboard and sees nothing.

## P4 — the learner's walk

Run every `explore.commands` entry exactly as printed and compare the output to
its label:

```sh
./bin/labctl scenario info "$SCENARIO"        # copy each command verbatim
```

Then do the objectives with real tools. Note every moment you opened the
repository to make progress — each is a dimension-8 finding.

## P5 — tamper test

For each check, break its subject, re-run `verify`, then restore and re-verify.
The pattern:

```sh
kubectl -n go-api get peerauthentication go-api-mtls -o yaml > "$SCRATCHPAD/pa.bak.yaml"

# sabotage: the scenario's stated security guarantee is now false
kubectl -n go-api patch peerauthentication go-api-mtls --type=merge \
  -p '{"spec":{"mtls":{"mode":"PERMISSIVE"}}}'

./bin/labctl scenario verify "$SCENARIO"      # MUST fail

kubectl -n go-api patch peerauthentication go-api-mtls --type=merge \
  -p '{"spec":{"mtls":{"mode":"STRICT"}}}'
./bin/labctl scenario verify "$SCENARIO"      # MUST pass again
```

Two things the pattern gets wrong if you improvise it:

- **Restore by patching back the field you changed**, not by re-applying the
  `-o yaml` backup. The backup carries a stale `resourceVersion`, and
  `kubectl apply` rejects it with "the object has been modified". Keep the
  backup as the record of what the field was; restore with a patch.
- **Index into a CR by content, not by position.** A JSON-patch path like
  `/spec/http/0/route/1/weight` assumes an ordering the manifest may not have —
  in `mesh-traffic-management`, `spec.http[0]` is the fault rule and carries a
  single unweighted route, so that patch is rejected outright. Read the object
  first (`kubectl get vs <name> -o jsonpath='{.spec.http}' | jq .`) and patch
  the element you actually meant.

Other sabotages worth having in the kit — always back up first, always restore:

| Claim | Sabotage |
|---|---|
| a mesh fault is active | `kubectl -n <ns> delete virtualservice <fault-vs>` |
| mTLS is STRICT | patch `.spec.mtls.mode` to `PERMISSIVE` |
| an autoscaler is reacting | `kubectl -n <ns> delete hpa <name>` |
| the metric is scraped | scale the exporter or ServiceMonitor target to zero |
| a rollout reached prod | `kubectl -n <ns> set image ...` back to the old tag |
| a PDB protects the workload | `kubectl -n <ns> delete pdb <name>` |

A check still green after its own subject is gone is the review's most important
finding. Say which check, what you broke, and what the replacement `promql` or
`script` check should assert.

## P6 — teardown and leaks

```sh
./bin/labctl scenario up "$SCENARIO"          # second run: converges, no error
./bin/labctl scenario reset "$SCENARIO"
./bin/labctl scenario down "$SCENARIO"

kubectl get ns
helm list -A
kubectl get pvc -A
kubectl get virtualservice,destinationrule,peerauthentication -A 2>/dev/null
```

Compare against the same sweep taken before P2. Only releases the scenario
adopted with `adopt: true` may survive.

## Cleanup

```sh
./bin/labctl traffic stop
```

Leave the lab in the state you found it, and say in the report what you left
running.
