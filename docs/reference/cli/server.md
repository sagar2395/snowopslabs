# Server, metrics & auth

## `labctl ui`

Serves the web dashboard and the REST API, and opens a browser.

On WSL2, where no Linux desktop is present, `ui` opens the URL in the host
Windows browser by trying openers in order: `wslview` (wslu),
`powershell.exe -NoProfile -Command Start-Process`, `cmd.exe /c start ""`, and
finally `xdg-open`. See
[Getting started on Windows (WSL2)](../../getting-started/wsl.md#opening-the-lab-from-windows).

```bash
labctl ui                             # http://127.0.0.1:3939
labctl ui --port 8080
labctl ui --ui-dir src/ui/dist        # serve the UI live from disk during development
```

| Flag | Default | Description |
|---|---|---|
| `--port` | `3939` | port to serve on |
| `--bind` | `127.0.0.1` | address to bind. `0.0.0.0` exposes it on the network and requires auth |
| `--ui-dir` | `$LABCTL_UI_DIR` | serve the UI from this directory instead of the embedded bundle |
| `--tls-cert` | - | TLS certificate (PEM); enables HTTPS when set with `--tls-key` |
| `--tls-key` | - | TLS private key (PEM) |
| `--tls-self-signed` | `false` | serve HTTPS with a self-signed certificate for this machine's host name and addresses. It is made once, kept in `~/.snowops/state/<cluster>/tls/`, and replaced when it would expire within a week or no longer covers an address. Cannot be combined with `--tls-cert` |

The server refuses a network-exposed bind while authentication is off, because
that would put cluster-control endpoints on the network. On a network bind it
prints the address of every interface other machines can reach, and warns when
it serves plain HTTP.

The dashboard shows cluster status, platform component health, applications
with deploy and destroy actions, scenarios with activate and deactivate
controls, and live updates over WebSocket. The UI is built into the binary by
`make cli-build`; during development, `make ui-dev` serves it from disk with no
Go rebuild.

## The API

Everything the UI does is available under **`/api/v2`** - the only API surface.
There is no unversioned `/api`; a request there gets the SPA shell, not JSON
([ADR-0013](../../adr/0013-remove-unversioned-api.md)).

Conventions, from [ADR-0006](../../adr/0006-api-conventions.md):

- **Errors are `application/problem+json`.** Branch on the `type` slug, never on
  the message. The human-readable cause is in `detail`. A path under `/api/v2`
  that no route serves answers `404` with `not_found`, and a route called with a
  method it does not accept answers `405` with `method_not_allowed`; neither
  gets the SPA shell. A route that names an app, scenario, service, runtime,
  challenge or learning path that does not exist answers `404 not_found`
  before it accepts any work, and no `detail` repeats a path on the server's
  disk.
- **Request bodies are JSON of at most 1 MiB.** A larger one answers
  `413 request_too_large`, and a body that is not valid JSON answers
  `400 invalid_request`; neither starts any work. A route whose body is
  optional, such as `scenarios/{name}/up`, accepts an empty one.
- **List collections are paginated**: `{items, nextCursor}`, with `?limit` (max
  200) and an opaque `?cursor`. Page until `nextCursor` comes back empty.
- **Catalog reads carry an `ETag`** and honour `If-None-Match` (→ 304).
- **Every request carries `X-Request-ID`**, echoed back and present on every log
  line - a sane inbound value is honoured, otherwise one is minted.
- **Events stream** over WebSocket at `/api/v2/ws`, with an SSE fallback at
  `/api/v2/stream`. Both are cursor-based: pass `?after=<seq>` to resume without
  a gap.

```bash
curl -s localhost:3939/api/v2/scenarios | jq '.items[].name'
curl -s 'localhost:3939/api/v2/runs?limit=10' | jq '.nextCursor'
```

## Metrics

An optional Prometheus endpoint, **off by default**:

```bash
LABCTL_METRICS=true labctl ui --port 3939
curl http://localhost:3939/metrics
```

When unset it adds no scrape surface at all. When enabled it serves the standard
text format with no auth, so a local Prometheus can scrape it.

| Metric | Type | Labels | Meaning |
|---|---|---|---|
| `labctl_http_requests_total` | counter | `method`, `route`, `status` | API requests handled; `route` is the path template, e.g. `/api/v2/apps/{name}/build` |
| `labctl_http_request_duration_seconds` | histogram | `method`, `route` | API request latency |
| `labctl_runs_total` | counter | `kind`, `status` | run-engine runs by kind and terminal outcome |
| `labctl_run_duration_seconds` | histogram | `kind` | run-engine execution time |
| `labctl_runs_in_flight` | gauge | - | runs currently executing |
| `labctl_build_info` | gauge | `version` | always 1; carries the build version |

Metrics and access logs are installed as middleware around the whole mux, so a
new route cannot ship unmeasured.

## `labctl users`: team mode

Accounts for the API and UI. Authentication is **off by default**; the server
enforces it only when started with `LABCTL_AUTH=true`. These commands edit
`~/.snowops/state/<cluster>/users.yaml` (Argon2id hashes, mode 0600) either way. Older
PBKDF2-HMAC-SHA256 hashes still verify, so an existing file keeps working.

Two roles: `operator` (full control) and `participant` (run challenges,
scenarios and learning paths, work an incident: check its status and reveal
hints; read status). A participant cannot change the platform, runtime, lab,
apps, services or traffic, and cannot inject an incident or end one with the
escape hatch: the lab runs one incident for the whole team, so starting and
ending it is the operator's.

```bash
labctl users add <name> --role operator --password '<password>'
labctl users add <name> --role participant                         # prompts for the password
LABCTL_PASSWORD='<password>' labctl users add <name> --role participant # scripted
labctl users list                                                  # names and roles, never hashes
labctl users remove <name>
```

Password precedence for `users add`: `--password`, then `LABCTL_PASSWORD`, then
an stdin prompt.

Enable auth:

```bash
LABCTL_AUTH=true labctl ui --port 3939
```

The UI then shows a login screen, and the API requires a session cookie or
`Authorization: Bearer <token>` from `POST /api/v2/auth/login`. Participants get
**403** on operator-only mutations, and scored runs are attributed to the
authenticated user.

Sessions last 12 hours and are kept in the lab database
(`~/.snowops/snowops.db`), so restarting `labctl ui` keeps everyone logged in;
logging out ends the session on the server. The database holds a SHA-256 hash
of each session token, never the token, so a copy of it cannot be used to log
in. Each request is checked against `users.yaml` as the server read it at
startup: a session whose user was removed is refused, and a user whose role
changed gets the new role. If the database cannot be opened, the server logs a
warning at startup and every login fails until it can.

> OIDC and SSO are out of scope. Serve behind TLS for anything beyond localhost - the
> cookie is `HttpOnly` and `SameSite=Strict`, and `Secure` only when the request
> arrived over TLS. See the
> [security posture](../../architecture/ARCHITECTURE.md#security-posture) for
> what is and is not implemented.

## Running a shared team server

One machine runs the lab and serves it to everyone: the web UI and API over
HTTPS with logins, and the cluster's API for `kubectl`. It is the setup a
[game day](../../game-day.md) uses. It serves **one team on one cluster**:
everyone shares the lab, one incident runs at a time, and nothing is isolated
between users. A participant can read everything an operator can, and anyone
with the kubeconfig below is a cluster admin. Two teams that compete need a
lab each.

SnowOps Labs ships no container image or Helm chart for `labctl` itself; the
`incluster` [runtime profile](../../runtime-profiles.md) points the lab at a
cluster you already have, but `labctl` still runs on a machine, as here.

**1. Add the people.** Run on the lab machine: one `operator` account for
the facilitator, and one `participant` account per person, each under the
name they will log in with. Each command asks for the password; share it with
that person out of band.

```bash
labctl users add <facilitator> --role operator
labctl users add <participant> --role participant   # once per participant
labctl users list
```

The server reads `users.yaml` when it starts, so restart it after adding or
removing someone. Sessions survive the restart.

**2. Start the server** with auth on, bound to the network, with HTTPS:

```bash
LABCTL_AUTH=true labctl ui --bind 0.0.0.0 --tls-self-signed
```

It prints a URL for each of the machine's addresses and the certificate's
SHA-256 fingerprint:

```
Starting labctl web UI at https://192.0.2.10:3939
  also at https://localhost:3939
HTTPS uses a self-signed certificate (~/.snowops/state/<cluster>/tls/server.crt), valid until 2027-02-05.
Browsers warn the first time; accept only if the certificate's SHA-256 fingerprint is:
  5C:1E:...:9A
```

`192.0.2.10` stands for the lab machine's address on your network; the first
URL is the one to send.

Send people the first URL and the fingerprint. Their browser warns about the
certificate once; they compare the fingerprint in the certificate details and
accept. To skip the warning, serve a certificate your team already trusts with
`--tls-cert` and `--tls-key` instead. Keep the terminal open, or run the
command under `tmux` or `nohup`: the server stops when it exits.

**3. Give people `kubectl`.** The k3d cluster's API listens on every interface
of the lab machine, but its certificate does not name the machine's address,
so the kubeconfig names the address and checks the certificate against
`kubernetes`, a name it does carry. Set `IP` to the address in the first URL
`labctl ui` printed, and `CLUSTER` to the lab's `CLUSTER_NAME` (`snowops`
unless you changed it):

```bash
IP=<lab machine address>
CLUSTER=<cluster name>
k3d kubeconfig get "$CLUSTER" > team.kubeconfig
PORT=$(kubectl --kubeconfig team.kubeconfig config view -o jsonpath='{.clusters[0].cluster.server}' | sed 's/.*://')
kubectl --kubeconfig team.kubeconfig config set-cluster "k3d-$CLUSTER" --server "https://$IP:$PORT" --tls-server-name kubernetes
kubectl --kubeconfig team.kubeconfig get nodes
```

Share `team.kubeconfig` with participants. It holds the cluster's admin
credentials, so treat it like a password and delete it after the session.
Each participant runs `export KUBECONFIG=$PWD/team.kubeconfig`.

**4. Dashboards.** Lab hostnames end in `.localhost`, which every browser
resolves to its own machine, so `grafana.<cluster>.localhost` works only on the
lab machine. Participants reach Grafana and Prometheus with `kubectl
port-forward` instead:

```bash
kubectl -n monitoring port-forward svc/grafana 3000:80
kubectl -n monitoring port-forward svc/prometheus-kube-prometheus-prometheus 9090:9090
kubectl -n monitoring port-forward svc/prometheus-kube-prometheus-alertmanager 9093:9093
```

Then open `http://localhost:3000` (Grafana, `admin` / `admin`),
`http://localhost:9090` (Prometheus) and `http://localhost:9093`
(Alertmanager). These are the names with the default `METRICS_PROVIDER`;
`kubectl -n monitoring get svc` lists them for another.

**5. Check it.** Each person opens the URL, checks the fingerprint, and logs
in; the sidebar shows their name and role. From a terminal, with the
certificate copied from `~/.snowops/state/<cluster>/tls/server.crt` on the lab
machine so `curl` verifies it:

```bash
curl -s --cacert server.crt -c cookies -H 'Content-Type: application/json' \
  -d '{"username":"<name>","password":"<password>"}' https://<lab machine address>:3939/api/v2/auth/login
curl -s --cacert server.crt -b cookies https://<lab machine address>:3939/api/v2/auth/me
```

`auth/me` answers `{"authEnabled":true,"authenticated":true,"user":"<name>","role":"participant"}`.

**Stopping.** Ctrl-C stops the server. `labctl users remove <name>` and a
restart end a person's access to the UI; deleting `team.kubeconfig` from every
machine ends their cluster access.
