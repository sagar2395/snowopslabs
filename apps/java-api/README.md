# java-api

A second technology stack, so the workload contract can be validated against
something that is not Go — and so a scenario can be run twice and compared.

Its value is precisely that it behaves differently: slow start, GC pauses, and a
memory floor roughly 25× the Go services'. Those differences are what make a
cross-stack comparison worth doing, and they are why absolute thresholds in a
scenario grade the language rather than the engineer (see
[ADR-0014](../../docs/adr/0014-workload-binding-and-app-contract.md)).

| Endpoint | Method | Description |
|---|---|---|
| `/` | GET | Service info (name, version, uptime, runtime) |
| `/health` | GET | Liveness probe (always 200) |
| `/ready` | GET | Readiness probe (503 once toggled) |
| `/version` | GET | Name and version |
| `/toggle-failure` | POST | Flip readiness to failing — the `readiness-toggle` capability |
| `/metrics` | GET | Prometheus exposition |

## The contract

Declared in [`app.env`](app.env):

```bash
APP_PORT=8080
APP_REQUEST_METRIC=http_server_request_duration_seconds
APP_CAPABILITIES=prometheus-metrics,readiness-toggle
```

It does not claim `otlp-tracing`, so a scenario needing traces refuses it by
name rather than failing halfway through.

It has **no Helm chart of its own** — the shared workload chart deploys it from
that contract, which is the same path a user's own application takes.

## No build tool, no dependencies

The app compiles with `javac` alone. A Maven or Gradle build would resolve a
dependency tree on first build, which breaks the lab's "activate offline in
seconds" property. The trade is that the Prometheus exposition is written by
hand, in `Metrics` — about sixty lines.

**A real service would use Micrometer.** The mapping is direct: register a
`Timer` named `http.server.request.duration` with tags `http.request.method`,
`http.route` and `http.response.status_code`, and Micrometer's Prometheus
registry emits exactly the series this file writes. Spring Boot's own
`http.server.requests` is *not* the semantic-convention name, so an out-of-the-box
Spring app needs either that rename or an honest `APP_REQUEST_METRIC` declaring
what it really emits.

## JVM sizing

Two settings matter, and both are the classic container trap:

- `MaxRAMPercentage` in the Dockerfile. Without it the JVM sizes its heap from
  the **node's** memory, ignores the container limit, and is OOMKilled by the
  kernel before it ever reports memory pressure of its own.
- `MEMORY_REQUEST` / `MEMORY_LIMIT` in `app.env`. Sizing a JVM like a small Go
  service is how `oom-kill` would "pass" for the wrong reason.

## Running a scenario against it

```bash
labctl app build java-api && labctl app deploy java-api
labctl app verify java-api
labctl scenario up autoscaling-under-load --app java-api
```
