# Running a game day

A game day is a few timed rounds in which a facilitator breaks the lab on
purpose and a team finds and fixes the fault with real tools: `kubectl`,
Grafana, Prometheus and a pager. Everything here runs on one lab machine that
the team reaches over the network. Plan two to three hours for three or four
rounds and the review.

## How a round works

1. The facilitator injects a fault in silent mode, so nobody, the facilitator's
   own tools included, names it until it is fixed.
2. The team diagnoses it from the symptoms: the app's errors, pod state,
   dashboards and the page that fires.
3. Someone fixes it with `kubectl` and presses **Check if resolved** in the web
   UI. The fault's detection check decides whether it is really fixed; hiding
   the symptom (scaling to zero, relabelling) does not pass.
4. The round ends when the check passes, or when the facilitator calls time and
   uses the escape hatch, which puts the lab back and counts as not solved.

The results go to the leaderboard and the run history, which you review at the
end.

## Roles

| Role | Account | Does |
|---|---|---|
| Facilitator | `operator`, on the lab machine | Prepares the lab, injects each fault, keeps time, decides on hints, ends a round with the escape hatch, runs the review |
| Participants | `participant` | Diagnose and fix with `kubectl`, check status and reveal hints in the UI |
| On call (optional) | `participant` | Watches the pager and says when it fires and what it names |

A participant cannot inject a fault or use the escape hatch: those requests
answer `403 this action requires the operator role`, so one person cannot end
a round for everyone. The web UI still shows the buttons.

**One lab is one team.** A lab runs one incident at a time and everyone on it
shares that incident. Teams that compete need a lab each, on a machine each;
inject the same fault with the same seed on every lab and compare their
exports afterwards.

## Size the machine

The lab machine runs the cluster, the monitoring stack and the app the faults
break: the one `APP_NAME` names in `.env` (`go-api` by default), written
`<app>` below. `labctl doctor` prints what Docker has.

| Docker size | Game day |
|---|---|
| 2 CPU / 4 GB | Works for the faults that change config or networking. `oom-kill`, `cpu-throttling` and `noisy-neighbor` start load and leave little headroom |
| 4 CPU / 8 GB | Any fault, with room for people to run `kubectl top`, port-forwards and Grafana at once |

[Resources](resources.md) has the details and how to give Docker more. The
machine needs to stay awake and on one network for the session: participants
reach it on port `3939` (the web UI) and on the cluster's API port. On a laptop,
plug it in and turn off sleep.

## The day before

On the lab machine, as the facilitator:

```bash
labctl init                                    # bring the lab up, or back after a restart
labctl doctor                                  # tools and Docker size
labctl status                                  # cluster, platform and apps healthy
labctl app build <app> && labctl app deploy <app>    # only if status lists the app as not deployed
labctl service up pager                        # the in-cluster pager the alerts page
```

Then set up the team server: add a user for each person, start the server with
logins and HTTPS, and make the participants' kubeconfig. The steps and the
commands are in [Running a shared team server](reference/cli/server.md#running-a-shared-team-server).
Check from one participant machine that the login works and that
`kubectl get nodes` answers.

Rehearse each fault you plan to use: inject it, look at the symptoms, and
resolve it. Knowing the fault spoils nothing for the team, and it shows you the
lab can take it. Do it the day before: what you run with `labctl` on the lab
machine is recorded under your OS user name, and a rehearsal on the day would
show up in the day's standings:

```bash
labctl incident inject crashloop-bad-config
labctl incident status
labctl incident resolve
```

## Plan the rounds

Pick three or four faults, easiest first. The full list, with what each breaks,
is in [incidents/README.md](../incidents/README.md#current-faults).

| Round | Good first choices | What the team practises |
|---|---|---|
| Warm-up | `crashloop-bad-config`, `bad-deploy-rollout` | Reading pod status, logs and rollout history |
| Middle | `service-selector-broken`, `missing-secret-key`, `pending-unschedulable` | Following a request from the ingress to the pod; events and scheduling |
| Hard | `stray-pod-behind-service`, `dns-egress-blocked`, `cpu-throttling` | Partial failures, where everything looks healthy and only some requests fail |

Name the faults yourself and inject each with `--silent`: the team does not
learn the name, and every round is different. `--random --seed N` picks one
for you and gives the same pick on every lab for the same seed, which is how
two teams get the same fault. Two seeds can pick the same fault, so random
rounds can repeat.

Give each round 20 to 30 minutes, and decide before you start how many hints
you allow and whether they cost anything. Hints cost score only when a fault
runs as a challenge; in a game day they are recorded, and the review shows how
many each round used.

## Brief the team

Send each person, before the session:

- the web UI address and the certificate fingerprint `labctl ui` printed, with
  a note to compare them before accepting the browser warning;
- their username, and their password through a separate channel;
- `team.kubeconfig`, and the line `export KUBECONFIG=$PWD/team.kubeconfig`;
- the port-forward commands for Grafana, Prometheus and Alertmanager from the
  team server page.

At the start, tell them how a round runs:

- The fault is somewhere in the app the lab runs and what serves it. Fix it the way
  you would in production; the lab only checks the result.
- Press **Check if resolved** when you think it is fixed. The person whose check
  first passes is credited with the fix.
- **Reveal a hint** shows the next hint only in your own browser. Read it out to
  the team. Every hint counts for the round, whoever revealed it.
- To follow the pager, run `kubectl -n pager logs deploy/pager -f | grep ^PAGE`.
  A page names the symptom, never the fault.
- Do not change the monitoring stack, the ingress or other namespaces unless the
  fault is there.

## Run a round

1. Note the time and inject:

   ```bash
   labctl incident inject service-selector-broken --silent
   ```

   or, for a random pick: `labctl incident inject --random --silent --seed 7`.
   It prints only `An incident has been injected. Good luck.`
2. The team works. They see the incident as `(hidden)` in the UI and the API,
   and the pager gets the page once the alert has been firing for its `for:`
   period, two to three minutes after the inject for most faults.
3. Keep time and answer questions about the lab, not the fault. You can follow
   along without seeing the answer: `labctl incident status` on the lab machine
   says only `NOT RESOLVED` while the incident is silent.
4. When someone's check passes, the UI names the fault and the check, and the
   lab tidies up after the fix by itself: it removes the alert rule and the
   fault's bookkeeping.

## End or rescue a round

| Situation | What to do |
|---|---|
| The check passed | Nothing. The round is over and recorded as fixed, for the person whose check passed |
| Time is up | `labctl incident resolve` on the lab machine, or **Resolve** in the UI as the facilitator. The lab is put back, and the round is recorded as ended with the escape hatch: it counts as resolved for nobody and adds to nobody's time |
| The team fixed it but the check still fails | The check is right more often than not: hiding a symptom does not pass. Use a hint, or end the round and look at the check in the review |
| `incident status` says no incident is active, but the lab is still broken | `labctl incident resolve <fault>` restores it without the active record. You chose the fault, so you know its name; for a random pick, `labctl results export --kind incident` names it once it has ended |
| The app is broken in a way the fault did not cause | `labctl app deploy <app>` |
| A command reports a run already holds the lock | [Recovering a stuck run](troubleshooting.md#recovering-a-stuck-run) |
| The cluster stops answering | `labctl init`, which restarts it and keeps the apps. `labctl reset` rebuilds it from scratch and loses everything; save it for the end |

`labctl incident inject` refuses while another incident is active and does not
name a silent one: `an incident is already active (injected silently, so it is
not named)`. Resolve the first before the next round.

## Review the results

**Standings.** Open **Leaderboard** in the web UI and choose **Today** or
**Last 4 hours**, so the board shows the game day and not everything the lab
has recorded before. The same ranking is at `GET
/api/v2/leaderboard?since=<RFC 3339 time>`. The **Incidents** column counts
faults a person fixed, and **Avg MTTR** is their average time to resolve; a
round ended with the escape hatch counts in neither. A round's hints count for
the person credited with it, not for whoever revealed them.

**Every round.** Export what was recorded since the session started, giving
the start as an RFC 3339 time such as `2026-11-07T09:00:00Z`, or as a
duration such as `4h`:

```bash
labctl results export --kind incident --since <session start> -o csv > game-day.csv
```

Each row has the fault, who was credited, `elapsedSeconds` from inject to fix,
`hintsUsed` and `resolvedBy`, which is `fix` or `escape-hatch`. The fields are
described in [recorded results](reference/cli/incidents.md#recorded-results).

**Cards.** `labctl results card <fault>` prints a Markdown card for a round,
and `-o svg` an image, to post in the team channel. Every card says it is
self-reported.

**The conversation.** Go round by round, without blame:

- What was the first signal, and who saw it? The page, a dashboard, a failing
  request?
- When did the team form the hypothesis that turned out right, and what led
  there?
- What took time that should not have: missing access, a dashboard that did not
  show it, an alert that named the wrong thing?
- Would the alert have woken the right person, and did its text help?
- What changes in your real runbooks, alerts or dashboards because of this
  round?

Write those changes down with an owner. They are the point of the day.

## Clean up

Stop `labctl ui` with Ctrl-C first: a removed user keeps their session until
the server restarts. Then:

```bash
labctl users remove <participant>              # once per participant
labctl service down pager
```

Ask everyone to delete `team.kubeconfig`: it holds the cluster's admin
credentials. The results stay in the lab's history
for the next session's comparison.

## Limits

These do not exist today. Plan around them:

- No per-team scoring inside one lab, and no second incident at the same time:
  use a lab per team.
- Hints are counted per round, not per person.
- Participants see the **Inject** and **Resolve** buttons; pressing one answers
  that it needs the operator role.
- Grafana and the other lab dashboards are reached through `kubectl
  port-forward`, because lab hostnames end in `.localhost` and resolve on the
  lab machine only.
