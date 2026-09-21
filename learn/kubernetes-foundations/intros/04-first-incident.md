# Module 4 — Your first incident

## What you'll do

Inject a real production fault (`service-selector-broken`) and fix it by
hand. This is a classic Kubernetes gotcha: everything looks healthy, but
the service has no endpoints because the selector doesn't match any pods.

## Background

The incident engine injects faults via shell scripts that mutate live
Kubernetes resources, records your time-to-detect and MTTR, and confirms
resolution through a machine-verifiable check.

This one is a classic: every health indicator stays green while the service is
completely dark. Nothing in the app is wrong, and nothing will tell you so.

## Objective

Inject the fault, diagnose it yourself, fix it, and confirm resolution.

```bash
bin/labctl incident inject service-selector-broken
curl http://go-api.k3d.local/health          # see what users see
```

Then work it the way you would on call. Two questions carry this one: *is the
thing that serves traffic actually pointing at anything*, and *what does the
cluster think should be behind it*. `kubectl get pods` will look reassuring;
keep going past it.

When you have a theory, fix it by hand — with `kubectl`, not with labctl.

**Completion check:** the most recent run of `service-selector-broken` in your
history was resolved **by hand**. `labctl incident resolve` is the escape hatch
and does not count: it undoes the fault for you, which is not the exercise.

**Note:** `bin/labctl incident hint` walks you in one rung at a time if you get
stuck, and `bin/labctl incident solution` is there when you would rather read
the answer than find it — you can always inject it again and do it properly.
