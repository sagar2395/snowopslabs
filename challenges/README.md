# Challenges — Timed, Auto-graded Skills Assessments

Challenges wrap real production faults and scenarios into timed
assessments: start the clock, diagnose and fix the problem, submit for
an automatic grade.

## Score formula

```
base = 100
hint_deduction  = hints_used × hint_penalty (default 5%)
time_deduction  = max(0, (elapsed − par_time) / par_time × 20)   (capped at 20)
raw_score       = base − hint_deduction − time_deduction
final_score     = raw_score × (checks_passed / checks_total)      (scaled by check ratio)
```

Examples: finishing in par with no hints → 100. Two hints at 5% + 10% over par → 75. Zero checks pass → 0.

## challenge.yaml schema

```yaml
name: restore-broken-deploy        # must match directory name
displayName: "Restore the broken deploy"
description: "What the practitioner experiences"
category: workload                 # workload | network | resources | config
parTime: "10m"                     # Go duration string; omit for unscored time
setup:
  type: incident                   # incident | scenario
  ref: bad-deploy-rollout          # fault name or scenario name
grading:
  useDetectionCheck: true          # use the fault's own detection check as the grade check
  # OR explicit checks:
  # checks:
  #   - name: api-healthy
  #     type: http
  #     url: "http://go-api.k3d.local/health"
  #     expectStatus: 200
hintPenalty: 5                     # % score deducted per hint (default 5)
```

## Current challenges

| Challenge | Category | Par | Setup |
|-----------|----------|-----|-------|
| `restore-broken-deploy` | workload | 10m | `bad-deploy-rollout` incident |
| `find-the-memory-leak` | resources | 8m | `oom-kill` incident |
| `make-the-slo-green` | config | 12m | `service-selector-broken` incident |

## Using challenges

```bash
labctl challenge list
labctl challenge info restore-broken-deploy
labctl challenge start restore-broken-deploy     # injects fault + starts timer
labctl challenge status                          # show elapsed time
labctl challenge hint                            # next hint (costs score)
labctl challenge submit                          # grade your fix
labctl challenge abort                           # escape hatch, no score
labctl challenge history                         # past runs
```

## Rules for challenge authors

1. The challenge must be completable — test it end-to-end on a fresh lab.
2. `setup.ref` must name an existing fault or scenario.
3. `grading.useDetectionCheck: true` is preferred for incident challenges —
   it reuses the same detection check that the incident engine uses, so
   the grade is consistent with `labctl incident status`.
4. Set a `parTime` that's achievable with one or two lookups — not so tight
   that it punishes beginners or so loose that it doesn't reward speed.
5. One active challenge at a time — `challenge abort` cleanly undoes the setup.
