# R07: Game day

**Wave:** W4 · **Time:** ~60 minutes · **Cluster needed:** a running k3d lab
and the [R11](R11-team-server.md) team server

Proves that [Running a game day](../game-day.md) runs as written: silent rounds
the team cannot read the answer out of, credit for the person whose check
passed, a page that arrives, a rescued round that counts for nobody, and a
review that shows only the session.

---

## Preconditions

- R11 passed and its server is running, with its `<facilitator>`,
  `<participant-1>` and `<participant-2>` accounts.
- `team.kubeconfig` on a participant machine.

## Steps

**1. The day before.** ⚠️ Run the guide's "The day before" commands, including
the rehearsal of one fault.

**Expect:** `labctl doctor` ends `Ready to run SnowOps Labs`, `labctl service up
pager` ends `Service pager installed`, and the rehearsal's `resolve` ends
`The lab should be healthy again`.

**2. A silent round.** ⚠️ Note the time, then:

```bash
$ labctl incident inject --random --silent --seed 7
```

**Expect:** only `An incident has been injected. Good luck.` In the UI and in
`GET /api/v2/incidents/status`, the fault is `(hidden)` and the check is only
`"pass": false`. `labctl incident status` on the lab machine says
`(hidden - silent mode)` and `NOT RESOLVED`.

**3. No name in a refusal.** As the facilitator, try a second inject from the
API and from `labctl incident inject <any fault>`.

**Expect:** `409` and exit 1, each saying the incident was injected silently
and is not named.

**4. Fix and credit.** As `<participant-2>`, reveal a hint. As
`<participant-1>`, fix the fault with
`kubectl` and press **Check if resolved**.

**Expect:** the check passes and names the fault. `labctl results export --kind
incident` has a row for it with `user` `<participant-1>`, `hintsUsed` `1`,
`resolvedBy` `fix` and `workload` set to the lab's app (`APP_NAME`, `go-api`
by default). `kubectl get prometheusrule -A`
lists no `labfault` rule.

**5. The page.** ⚠️ Inject `service-selector-broken --silent`. Watch
`kubectl -n pager logs deploy/pager -f | grep ^PAGE`.

**Expect:** a `firing` page within about three minutes, naming
`LabFaultServiceEndpointsEmpty` and the app's namespace, and not the fault.

**6. Rescue.** Leave it unfixed. As the facilitator, press **Resolve** in the UI.

**Expect:** the lab is put back (`kubectl -n <app> get endpointslices` lists an
address), and the round's export row has `resolvedBy` `escape-hatch`.

**7. Review.** In the UI, open Leaderboard and choose **Last 4 hours**. Run the
guide's export and `labctl results card` commands.

**Expect:** the board lists only the people who played. `<participant-1>` has
1 incident and the round's time as Avg MTTR; the facilitator, credited with the rescued
round, has 0 incidents and no MTTR. The card for the rescued round says
`Time before it ended` and `escape hatch`, and every card ends with the
self-reported line.

## Failure signatures

| Symptom | Cause |
|---|---|
| No page after five minutes | Alertmanager is not routing to the pager: `labctl service status pager`, and check `ALERT_WEBHOOK_URL` was unset when `monitoring/metrics` was installed |
| The check passes while requests still fail | A finding against the fault's detection check; record the fix that fooled it |
| A participant's **Resolve** works | The server is running a build from before inject and resolve became operator-only |

## Teardown

Run the guide's "Clean up" section, then R11's teardown.

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
