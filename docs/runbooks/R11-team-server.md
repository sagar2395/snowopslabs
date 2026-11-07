# R11: Team server

**Wave:** W8 · **Time:** ~20 minutes · **Cluster needed:** a running k3d lab

Proves that a person who has not seen the project can bring up the shared
server from [Running a shared team server](../reference/cli/server.md#running-a-shared-team-server)
and have three users log in over the network, with HTTPS and auth, and that
their sessions survive a restart. Unit and contract tests cover the pieces;
only a real machine on a real network shows the URLs, the certificate and the
kubeconfig work for someone else.

---

## Preconditions

- A lab that `labctl status` reports healthy, with `go-api` deployed.
- A second machine on the same network, or this machine using its own LAN
  address in place of `localhost`.
- `jq`, `openssl` and `curl`.

## Steps

**1. Add three people.** Follow step 1 of the guide. For a scripted run, set the
password in `LABCTL_PASSWORD` for each `users add`.

```bash
$ labctl users list
```

**Expect:** `facilitator` as `operator`, `alice` and `bob` as `participant`.

**2. Refuse the network without auth.** 🔍

```bash
$ labctl ui --bind 0.0.0.0
```

**Expect:** exit 1, `refusing to bind 0.0.0.0 without authentication`, and the
command that turns auth on.

**3. Start the server** ⚠️ as in step 2 of the guide:

```bash
$ LABCTL_AUTH=true labctl ui --bind 0.0.0.0 --tls-self-signed
```

**Expect:** `Starting labctl web UI at https://<LAN address>:3939`, an `also at`
line per other address, the certificate path under
`~/.snowops/state/<cluster>/tls/`, and a SHA-256 fingerprint.

**4. Check the certificate from the network.** 🔍 From the second machine:

```bash
$ echo | openssl s_client -connect <LAN address>:3939 2>/dev/null | openssl x509 -noout -fingerprint -sha256 -ext subjectAltName
```

**Expect:** the fingerprint step 3 printed, and the LAN address among the
subject alternative names.

**5. Three logins.** From the second machine, with `server.crt` copied from
the lab machine, run step 5 of the guide for each user.

**Expect:** `auth/me` answers `"authenticated":true` with each user's role. A
wrong password answers `401 invalid_credentials`. A request with no cookie
answers `401`.

**6. Sessions survive a restart.** ⚠️ Stop the server with Ctrl-C and start it
again with the same command. Repeat `auth/me` with the cookies from step 5.

**Expect:** the same fingerprint is printed, and every user is still
authenticated. After `POST /api/v2/auth/logout` with one cookie, that cookie
answers `"authenticated":false`.

**7. Participants cannot start or end the incident.** As `bob`:

```bash
$ curl -s --cacert server.crt -b cookies -X POST https://<LAN address>:3939/api/v2/incidents/inject-random
$ curl -s --cacert server.crt -b cookies -X POST https://<LAN address>:3939/api/v2/incidents/resolve
```

**Expect:** both answer `403 forbidden_role`.

**8. kubectl for participants.** Follow step 3 of the guide on the lab machine,
copy `team.kubeconfig` to the second machine, and run:

```bash
$ KUBECONFIG=team.kubeconfig kubectl get nodes
```

**Expect:** every node `Ready`, with no TLS error and no
`--insecure-skip-tls-verify`.

**9. Dashboards.** Run the three port-forwards of step 4 of the guide with that
kubeconfig.

**Expect:** `curl localhost:3000/api/health` answers `"database": "ok"`,
`curl localhost:9090/-/ready` answers `Prometheus Server is Ready.`, and
`curl localhost:9093/-/ready` answers `OK`.

## Failure signatures

| Symptom | Cause |
|---|---|
| The browser on another machine cannot connect | The lab machine's firewall blocks port 3939, or it is on another network |
| `kubectl` reports `x509: certificate is valid for ... not <address>` | `--tls-server-name kubernetes` was left out of the kubeconfig |
| `curl: (60) SSL certificate problem` | `server.crt` was not passed with `--cacert`, or the certificate was replaced since it was copied |

## Teardown

```bash
$ labctl users remove alice
$ labctl users remove bob
$ labctl users remove facilitator
```

Stop the server with Ctrl-C and delete `team.kubeconfig` and `server.crt` from
the second machine.

## Results

| Step | Pass / fail / deviation | Notes |
|---|---|---|
| 1 | | |
| 2 | | |
| 3 | | |
| 4 | | |
| 5 | | |
| 6 | | |
| 7 | | |
| 8 | | |
| 9 | | |
