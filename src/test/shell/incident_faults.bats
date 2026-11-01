#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# What each fault's inject.sh and resolve.sh do to a lab: the change inject
# makes, that a second inject makes none, that resolve puts back what inject
# recorded, that a failing kubectl fails the script, and that nothing inject
# prints or writes on the learner's objects gives the answer away.

load 'helpers/stub'

setup() {
  stub_setup
  ROOT="$(project_root)"
  export TARGET_NAMESPACE=shop TARGET_WORKLOAD=cart
  export WORKLOAD_NAMESPACE=shop WORKLOAD_NAME=cart
  export MONITORING_NAMESPACE=monitoring FAULT_WAIT_SECONDS=0 FAULT_SAMPLE_SECONDS=0
  export DOMAIN_SUFFIX=lab.test INGRESS_URL_SUFFIX=lab.test
  stub_command sleep
  lab_stub
}

teardown() {
  stub_teardown
}

# lab_stub is a kubectl that answers as one small lab. Exported variables set
# its state; a variable left unset is the healthy lab before any fault.
#   FAIL_ON=<args>   the call whose arguments contain this fails
#   RECORD_<key>     a value in the fault's record (dashes as underscores)
lab_stub() {
  stub_script kubectl <<'STUB'
if [ -n "${FAIL_ON:-}" ]; then
  case "$*" in *"$FAIL_ON"*) echo "error: stubbed failure" >&2; exit 1 ;; esac
fi
healthy_selector='{"app.kubernetes.io/name":"cart","tier":"web"}'
healthy_probe='{"httpGet":{"path":"/health","port":"http"}}'
healthy_refs="$(printf 'PORT||\nAPI_TOKEN|cart-secret|api-token')"
case "$*" in
  *"apply -n monitoring -f -"*) cat >>"$STUB_DIR/applied-monitoring.yaml" ;;
  *"apply"*"-f -"*) cat >>"$STUB_DIR/applied.yaml" ;;
  *"create configmap"*) echo "kind: ConfigMap" ;;
  *"get configmap"*)
    args="$*"
    key="${args##*jsonpath=\{.data.}"
    key="${key%%\}*}"
    var="RECORD_$(printf '%s' "$key" | tr '-' '_')"
    printf '%s' "${!var:-}"
    ;;

  # service-selector-broken
  *"get svc"*"jsonpath={.spec.selector}"*) printf '%s' "${SELECTOR-$healthy_selector}" ;;
  *"get svc"*'selector.app\.kubernetes\.io/name}'*) printf '%s' "${SVC_LABEL-cart}" ;;
  *"get deploy"*'labels.app\.kubernetes\.io/name}'*) printf '%s' "${POD_LABEL-cart}" ;;
  *"get deploy"*"jsonpath={.spec.selector.matchLabels}"*) printf '%s' "${MATCH_LABELS-}" ;;

  # crashloop-bad-config
  *"get deploy"*'env[?(@.name=="PORT")].value}'*) printf '%s' "${PORT_NOW-}" ;;
  *"get deploy"*"ports[0].containerPort}"*) printf '%s' "${CONTAINER_PORT-8080}" ;;
  *"get deploy"*"jsonpath={.metadata.generation}"*) printf '7' ;;
  *"get deploy"*"jsonpath={.status.observedGeneration}"*) printf '7' ;;
  *"get deploy"*'deployment\.kubernetes\.io/revision}'*) printf '5' ;;
  *"get rs"*"ownerReferences"*) printf 'cart-old cart 4\ncart-new cart 5\nother-old other 4\n' ;;

  # bad-deploy-rollout
  *"get deploy"*"containers[0].image}"*) printf '%s' "${IMAGE-registry.lab:5000/shop/cart:v1.4.0}" ;;
  *"get deploy"*"containers[0].name}"*) printf 'app' ;;
  *"get pods"*"containerStatuses[0].ready"*) [ -z "${SERVING-}" ] || printf '%s\n' "$SERVING" ;;

  # oom-kill
  *"top pod"*)
    # TOP_SEQ: successive CPU readings, the last one repeating.
    if [ -n "${TOP_SEQ-}" ]; then
      echo x >>"$STUB_DIR/top.calls"
      i="$(wc -l <"$STUB_DIR/top.calls" | tr -d ' ')"
      cpu="$(printf '%s\n' $TOP_SEQ | sed -n "${i}p")"
      [ -n "$cpu" ] || cpu="$(printf '%s\n' $TOP_SEQ | tail -1)"
      echo "cart-abc $cpu 20Mi"
    else
      echo "cart-abc ${TOP_CPU-5m} ${WORKING_SET-20Mi}"
    fi
    ;;
  *"get deploy"*"resources.limits.memory}"*) printf '%s' "${MEM_LIMIT-}" ;;
  *"get deploy"*"resources.requests.memory}"*) printf '%s' "${MEM_REQUEST-}" ;;

  # network-blackhole: "<policyTypes>|<ingress rules>" of the policy, if any
  *"get networkpolicy default-deny-ingress -o jsonpath"*)
    [ -n "${BLACKHOLE_POLICY-}" ] || exit 1
    printf '%s' "$BLACKHOLE_POLICY"
    ;;
  *"get networkpolicy default-deny-ingress"*) [ -n "${BLACKHOLE_POLICY-}" ] || exit 1 ;;

  # pending-unschedulable
  *"get deploy"*"resources.requests.cpu}"*) printf '%s' "${CPU_REQUEST-50m}" ;;
  *"get deploy"*"resources.limits.cpu}"*) printf '%s' "${CPU_LIMIT-200m}" ;;
  *"get deploy"*"jsonpath={.spec.replicas}"*) printf '%s' "${REPLICAS-1}" ;;
  *"get deploy"*'go-template={{with index (index .spec.template.spec.containers 0).ports 0}}'*) printf '%s' "${PORT_SPEC-http 8080}" ;;
  *"get deploy"*"go-template"*) printf 'app.kubernetes.io/name=cart,' ;;
  *"get hpa"*) [ -z "${HPA_TARGET-}" ] || printf '%s\n' "$HPA_TARGET" ;;
  *"get nodes"*"allocatable.cpu"*) printf '%s' "${NODE_ALLOCATABLE-4 3800m }" ;;
  *"get pods"*"spec.nodeName="*) [ -z "${UNSCHEDULED_PODS-}" ] || printf '%s\n' "$UNSCHEDULED_PODS" ;;

  # liveness-probe-kill
  *"get deploy"*"livenessProbe.httpGet.path}"*) printf '%s' "${LIVENESS_PATH-/health}" ;;
  *"get deploy"*"livenessProbe.httpGet.port}"*) printf 'http' ;;
  *"get deploy"*"livenessProbe.httpGet.scheme}"*) printf 'HTTP' ;;
  *"get deploy"*"livenessProbe}"*) printf '%s' "${LIVENESS_PROBE-$healthy_probe}" ;;
  *"get deploy"*'ports[?(@.name=="http")].containerPort}'*) printf '8080' ;;
  *"get pods"*"status.phase=Running"*) printf '%s' "${RUNNING_PODS-cart-abc}" ;;
  *"get --raw"*"pods/cart-old:"*"/proxy/ready"*) printf '{"status":"ready"}' ;;
  *"get --raw"*"/proxy/ready"*)
    [ "${POD_GET:-ok}" = ok ] || exit 1
    printf '%s' "${READY_BODY-{\"status\":\"ready\",\"upstream\":\"ok\"\}}"
    ;;
  *"get --raw"*) [ "${POD_GET:-ok}" = ok ] || exit 1 ;;
  *"get svc cart"*"ports[0].port}"*) printf '8080' ;;

  # missing-secret-key: ENV_REFS is "<env>|<secret>|<key>" per variable
  *"get deploy"*"secretKeyRef.key}"*) printf '%s\n' "${ENV_REFS-$healthy_refs}" ;;
  *"get secret"*"go-template"*) printf '%s\n' "${SECRET_KEYS-api-token}" ;;
  *"get secret"*"jsonpath={.data."*) printf 'dG9rZW4=' ;;

  # dns-egress-blocked: EGRESS_POLICY is the policy's egress rules, if any
  *"get networkpolicy default-deny-egress -o jsonpath"*)
    [ -n "${EGRESS_POLICY-}" ] || exit 1
    printf '%s' "$EGRESS_POLICY"
    ;;
  *"get networkpolicy default-deny-egress"*) [ -n "${EGRESS_POLICY-}" ] || exit 1 ;;
  *"get deploy"*'env[?(@.name=="UPSTREAM_URL")].value}'*) printf '%s' "${UPSTREAM_NOW-}" ;;

  # stray-pod-behind-service
  *"get pod cart-debug"*) if [ -n "${STRAY_POD-}" ]; then echo "pod/cart-debug"; else exit 1; fi ;;
  *"get deploy"*'containers[0].image}'*) printf '%s' "${IMAGE-registry.lab:5000/shop/cart:v1.4.0}" ;;

  # noisy-neighbor
  *"get pods"*"custom-columns=NAME"*) [ -z "${VICTIM_PODS-set}" ] || echo "${VICTIM_PODS-cart-abc node-1 Running}" ;;
  *"get nodes"*) echo "node/node-1" ;;
  *"get node "*) printf '%s' "${NODE_CPU-4}" ;;
  *"get ns"*"jsonpath={.status.phase}"*) printf '%s' "${NS_PHASE-}" ;;
  *"get ns"*) [ -z "${NS_PHASE-}" ] && exit 1 ;;
esac
exit 0
STUB
}

inject() {
  run bash "$ROOT/incidents/$1/inject.sh"
}

resolve() {
  run bash "$ROOT/incidents/$1/resolve.sh"
}

# Each fault with the kubectl call that makes its change.
faults() {
  cat <<'EOF'
service-selector-broken|patch svc cart
crashloop-bad-config|set env deploy/cart
bad-deploy-rollout|set image deploy/cart
oom-kill|set resources deploy/cart
network-blackhole|rollout restart deploy/cart
noisy-neighbor|apply -f -
pending-unschedulable|patch deploy cart --type=strategic
liveness-probe-kill|patch deploy cart --type=json
missing-secret-key|patch secret cart-secret --type=json
cpu-throttling|set resources deploy/cart --limits=cpu=
dns-egress-blocked|apply -n shop -f -
stray-pod-behind-service|run cart-debug
EOF
}

# Faults that keep a record of what resolve must undo.
recording_faults() {
  faults | grep -v -e '^noisy-neighbor'
}

# Faults that change the workload's own namespace. noisy-neighbor runs in a
# namespace of its own instead.
in_namespace_faults() {
  faults | grep -v -e '^noisy-neighbor'
}

# line_of <args> - the position in the call log of the first matching call.
line_of() {
  calls_for kubectl | grep -n -- "$1" | head -1 | cut -d: -f1
}

# ── Every fault ───────────────────────────────────────────────────────────────

@test "inject makes its change on a healthy lab" {
  while IFS='|' read -r fault change; do
    reset_calls
    inject "$fault"
    [ "$status" -eq 0 ] || { echo "$fault: $output"; return 1; }
    assert_called kubectl "$change" || { echo "$fault"; return 1; }
  done < <(faults)
}

@test "inject output names neither the fault nor the lab's own marker" {
  while IFS='|' read -r fault _; do
    inject "$fault"
    case "$output" in
      *labfault* | *"$fault"*) echo "$fault gives itself away: $output"; return 1 ;;
    esac
  done < <(faults)
}

@test "stray-pod-behind-service: inject output does not name the mechanism" {
  inject stray-pod-behind-service
  [ "$status" -eq 0 ]
  [[ "$output" == *"Injecting the fault into shop..."* ]] || false
  [[ "$output" != *"diagnostic pod"* ]] || false
}

@test "inject writes no annotation, label or labfault name on the workload's objects" {
  while IFS='|' read -r fault _; do
    reset_calls
    inject "$fault"
    refute_called kubectl "annotate" || { echo "$fault"; return 1; }
    refute_called kubectl " label " || { echo "$fault"; return 1; }
    if calls_for kubectl | grep -e '-n shop' | grep -q labfault; then
      echo "$fault writes labfault into the workload's namespace:"
      calls_for kubectl | grep -e '-n shop' | grep labfault
      return 1
    fi
  done < <(faults)
}

@test "nothing inject applies in the workload's namespace is named or labelled for the lab" {
  while IFS='|' read -r fault _; do
    : >"$STUB_DIR/applied.yaml"
    inject "$fault"
    if grep -q -e labfault -e "$fault" "$STUB_DIR/applied.yaml"; then
      echo "$fault applies an object that names itself:"
      grep -n -e labfault -e "$fault" "$STUB_DIR/applied.yaml"
      return 1
    fi
  done < <(in_namespace_faults)
}

@test "inject fails when the call that makes its change fails" {
  while IFS='|' read -r fault change; do
    FAIL_ON="$change" inject "$fault"
    [ "$status" -ne 0 ] || { echo "$fault reported success over a failed change"; return 1; }
  done < <(faults)
}

@test "inject carries on without paging when the alert rule cannot be installed" {
  while IFS='|' read -r fault change; do
    reset_calls
    FAIL_ON="apply -n monitoring" inject "$fault"
    [ "$status" -eq 0 ] || { echo "$fault: $output"; return 1; }
    [[ "$output" == *"alert rule"* ]] || false
    assert_called kubectl "$change"
  done < <(faults)
}

@test "inject records what resolve needs before it changes anything" {
  while IFS='|' read -r fault change; do
    reset_calls
    inject "$fault"
    saved="$(line_of "create configmap labfault-$fault")"
    changed="$(line_of "$change")"
    [ -n "$saved" ] || { echo "$fault recorded nothing"; return 1; }
    [ "$saved" -lt "$changed" ] || { echo "$fault changed the lab before recording"; return 1; }
    assert_called kubectl "-n kube-system create configmap labfault-$fault"
  done < <(recording_faults)
}

@test "inject fails without changing anything when the record cannot be saved" {
  while IFS='|' read -r fault change; do
    reset_calls
    FAIL_ON="create configmap" inject "$fault"
    [ "$status" -ne 0 ] || { echo "$fault injected with no record"; return 1; }
    refute_called kubectl "$change"
  done < <(recording_faults)
}

@test "resolve forgets the record and disarms the page" {
  while IFS='|' read -r fault _; do
    reset_calls
    resolve "$fault"
    [ "$status" -eq 0 ] || { echo "$fault: $output"; return 1; }
    assert_called kubectl "delete prometheusrule labfault-$fault labfault-silent -n monitoring"
  done < <(faults)
  while IFS='|' read -r fault _; do
    reset_calls
    resolve "$fault"
    assert_called kubectl "-n kube-system delete configmap labfault-$fault --ignore-not-found"
  done < <(recording_faults)
}

@test "a silent inject arms its page under a neutral rule and group name" {
  while IFS='|' read -r fault _; do
    : >"$STUB_DIR/applied-monitoring.yaml"
    FAULT_SILENT=1 inject "$fault"
    [ "$status" -eq 0 ] || { echo "$fault: $output"; return 1; }
    rule="$STUB_DIR/applied-monitoring.yaml"
    if grep -v '^ *#' "$rule" | grep -q "$fault"; then
      echo "$fault names itself in its rule:"
      grep -n "$fault" "$rule"
      return 1
    fi
    [ "$(grep -c -e 'name: labfault-silent$' "$rule")" -eq 2 ] || { echo "$fault: rule and group not renamed"; return 1; }
    grep -q 'alert: LabFault' "$rule" || { echo "$fault lost its alert"; return 1; }
  done < <(faults)
}

@test "an inject that is not silent keeps the rule named after the fault" {
  while IFS='|' read -r fault _; do
    : >"$STUB_DIR/applied-monitoring.yaml"
    inject "$fault"
    [ "$(grep -c -e "name: labfault-$fault\$" "$STUB_DIR/applied-monitoring.yaml")" -eq 2 ] || { echo "$fault"; return 1; }
  done < <(faults)
}

@test "inject and resolve act on the bound workload, never on the default app" {
  while IFS='|' read -r fault _; do
    reset_calls
    inject "$fault"
    resolve "$fault"
    if grep -q 'go-api' "$STUB_LOG" "$STUB_DIR/applied.yaml" 2>/dev/null; then
      echo "$fault names go-api although bound to shop/cart:"
      grep 'go-api' "$STUB_LOG" "$STUB_DIR/applied.yaml"
      return 1
    fi
  done < <(faults)
}

# ── service-selector-broken ───────────────────────────────────────────────────

@test "service-selector-broken: inject points the Service at a renamed label and records the whole selector" {
  inject service-selector-broken
  [ "$status" -eq 0 ]
  assert_called kubectl 'patch svc cart -p {"spec":{"selector":{"app.kubernetes.io/name":"cart-v2"}}}'
  assert_called kubectl '--from-literal=original-selector={"app.kubernetes.io/name":"cart","tier":"web"}'
}

@test "service-selector-broken: a second inject changes nothing" {
  SVC_LABEL=cart-v2 inject service-selector-broken
  [ "$status" -eq 0 ]
  [[ "$output" == *"already injected"* ]] || false
  refute_called kubectl "patch"
  refute_called kubectl "create configmap"
}

@test "service-selector-broken: the selector inject writes is one the check rejects" {
  inject service-selector-broken
  written="$(calls_for kubectl | sed -n 's/.*patch svc cart -p {"spec":{"selector":\(.*\)}}$/\1/p')"
  [ -n "$written" ]
  stub_script curl <<'STUB'
printf '200'
STUB
  export CHECK_SELECTOR="$written"
  stub_script kubectl <<'STUB'
case "$*" in
  *"jsonpath={.spec.selector}"*) printf '%s' "$CHECK_SELECTOR" ;;
  *"jsonpath={.spec.template.metadata.labels}"*) printf '%s' '{"app.kubernetes.io/name":"cart","tier":"web"}' ;;
esac
STUB
  run bash "$ROOT/incidents/service-selector-broken/checks/resolved.sh"
  [ "$status" -ne 0 ]
}

@test "service-selector-broken: resolve replaces the selector with the recorded one" {
  RECORD_original_selector='{"app.kubernetes.io/name":"cart","tier":"web"}' SVC_LABEL=cart-v2 \
    resolve service-selector-broken
  [ "$status" -eq 0 ]
  assert_called kubectl 'patch svc cart --type=json'
  assert_called kubectl '"op":"replace","path":"/spec/selector","value":{"app.kubernetes.io/name":"cart","tier":"web"}}'
}

@test "service-selector-broken: resolve with no record on a healthy Service changes nothing" {
  resolve service-selector-broken
  [ "$status" -eq 0 ]
  [[ "$output" == *"Nothing to resolve"* ]] || false
  refute_called kubectl "patch"
}

@test "service-selector-broken: resolve after a fix made on the Deployment's labels leaves the Service alone" {
  RECORD_original_selector='{"app.kubernetes.io/name":"cart","tier":"web"}' SVC_LABEL=cart-v2 POD_LABEL=cart-v2 \
    resolve service-selector-broken
  [ "$status" -eq 0 ]
  refute_called kubectl "patch"
  assert_called kubectl "delete configmap labfault-service-selector-broken"
  assert_called kubectl "delete prometheusrule labfault-service-selector-broken"
}

@test "service-selector-broken: resolve with no record on a broken Service restores from the Deployment" {
  SVC_LABEL=cart-v2 MATCH_LABELS='{"app.kubernetes.io/name":"cart"}' resolve service-selector-broken
  [ "$status" -eq 0 ]
  assert_called kubectl '"value":{"app.kubernetes.io/name":"cart"}}]'
}

@test "service-selector-broken: resolve fails with the command to run when nothing says what to restore" {
  SVC_LABEL=cart-v2 resolve service-selector-broken
  [ "$status" -ne 0 ]
  [[ "$output" == *"restore it by hand"* ]] || false
  refute_called kubectl "patch"
}

# ── stray-pod-behind-service ─────────────────────────────────────────────────

@test "stray-pod-behind-service: inject creates a labelled debug pod and records its name" {
  inject stray-pod-behind-service
  [ "$status" -eq 0 ]
  assert_called kubectl "run cart-debug"
  assert_called kubectl "--labels=app.kubernetes.io/name=cart"
  assert_called kubectl "--env=UPSTREAM_URL=http://no-such-upstream.invalid:8080"
  assert_called kubectl "--from-literal=pod=cart-debug"
}

@test "stray-pod-behind-service: inject names the pod's port as the Deployment does, so a Service with a named targetPort routes to it" {
  inject stray-pod-behind-service
  [ "$status" -eq 0 ]
  assert_called kubectl '"ports":.{"name":"http","containerPort":8080}'
  assert_called kubectl "--override-type=strategic"
}

@test "stray-pod-behind-service: inject keeps an unnamed port unnamed" {
  PORT_SPEC=" 9090" inject stray-pod-behind-service
  [ "$status" -eq 0 ]
  assert_called kubectl '"ports":.{"containerPort":9090}'
}

@test "stray-pod-behind-service: inject stops when the Deployment declares no port" {
  PORT_SPEC="" inject stray-pod-behind-service
  [ "$status" -ne 0 ]
  refute_called kubectl " run cart-debug"
}

@test "stray-pod-behind-service: a second inject changes nothing" {
  STRAY_POD=yes RECORD_pod=cart-debug inject stray-pod-behind-service
  [ "$status" -eq 0 ]
  [[ "$output" == *"already injected"* ]] || false
  refute_called kubectl " run cart-debug"
  refute_called kubectl "create configmap"
}

@test "stray-pod-behind-service: resolve deletes the recorded pod and disarms the alert" {
  RECORD_pod=cart-debug resolve stray-pod-behind-service
  [ "$status" -eq 0 ]
  assert_called kubectl "delete pod cart-debug --ignore-not-found"
  assert_called kubectl "delete prometheusrule labfault-stray-pod-behind-service"
  assert_called kubectl "delete configmap labfault-stray-pod-behind-service"
}

@test "stray-pod-behind-service: resolve with no record removes the fixed debug pod" {
  resolve stray-pod-behind-service
  [ "$status" -eq 0 ]
  assert_called kubectl "delete pod cart-debug --ignore-not-found"
}

# ── crashloop-bad-config ──────────────────────────────────────────────────────

@test "crashloop-bad-config: inject sets the bad port and records that there was none" {
  inject crashloop-bad-config
  [ "$status" -eq 0 ]
  assert_called kubectl "set env deploy/cart -c app PORT=80800"
  assert_called kubectl "--from-literal=original-port=none"
}

@test "crashloop-bad-config: inject records the port the workload set itself" {
  PORT_NOW=9090 inject crashloop-bad-config
  assert_called kubectl "--from-literal=original-port=9090"
}

@test "crashloop-bad-config: inject retires this Deployment's older ReplicaSets only" {
  inject crashloop-bad-config
  assert_called kubectl "scale rs cart-old --replicas=0"
  refute_called kubectl "scale rs cart-new"
  refute_called kubectl "scale rs other-old"
}

@test "crashloop-bad-config: a second inject changes nothing" {
  PORT_NOW=80800 inject crashloop-bad-config
  [ "$status" -eq 0 ]
  [[ "$output" == *"already injected"* ]] || false
  refute_called kubectl "set env"
  refute_called kubectl "create configmap"
}

@test "crashloop-bad-config: resolve restores the port in every recorded case" {
  # recorded port | port now | the call resolve must make ("" for none)
  while IFS='|' read -r recorded now want; do
    reset_calls
    RECORD_original_port="$recorded" PORT_NOW="$now" resolve crashloop-bad-config
    [ "$status" -eq 0 ] || { echo "recorded=$recorded now=$now: $output"; return 1; }
    if [ -n "$want" ]; then
      assert_called kubectl "$want" || { echo "recorded=$recorded now=$now"; return 1; }
    else
      refute_called kubectl "set env" || { echo "recorded=$recorded now=$now"; return 1; }
    fi
  done <<'EOF'
9090|80800|set env deploy/cart -c app PORT=9090
none|80800|set env deploy/cart -c app PORT-
none|7070|set env deploy/cart -c app PORT-
|80800|set env deploy/cart -c app PORT=8080
9090|9090|
none||
||
EOF
}

# ── bad-deploy-rollout ────────────────────────────────────────────────────────

@test "bad-deploy-rollout: inject swaps the tag and keeps the registry port" {
  inject bad-deploy-rollout
  [ "$status" -eq 0 ]
  assert_called kubectl "set image deploy/cart app=registry.lab:5000/shop/cart:v99.9.9"
  assert_called kubectl "--from-literal=original-image=registry.lab:5000/shop/cart:v1.4.0"
}

@test "bad-deploy-rollout: inject tags an image that had no tag" {
  IMAGE=registry.lab:5000/shop/cart inject bad-deploy-rollout
  assert_called kubectl "set image deploy/cart app=registry.lab:5000/shop/cart:v99.9.9"
}

@test "bad-deploy-rollout: a second inject changes nothing" {
  IMAGE=registry.lab:5000/shop/cart:v99.9.9 inject bad-deploy-rollout
  [ "$status" -eq 0 ]
  [[ "$output" == *"already injected"* ]] || false
  refute_called kubectl "set image"
  refute_called kubectl "create configmap"
}

@test "bad-deploy-rollout: resolve restores the recorded image" {
  RECORD_original_image=registry.lab:5000/shop/cart:v1.4.0 IMAGE=registry.lab:5000/shop/cart:v99.9.9 \
    resolve bad-deploy-rollout
  [ "$status" -eq 0 ]
  assert_called kubectl "set image deploy/cart app=registry.lab:5000/shop/cart:v1.4.0"
}

@test "bad-deploy-rollout: resolve with no record recovers the image from the pod still serving" {
  IMAGE=registry.lab:5000/shop/cart:v99.9.9 \
    SERVING="$(printf 'false registry.lab:5000/shop/cart:v99.9.9\ntrue registry.lab:5000/shop/cart:v1.3.0')" \
    resolve bad-deploy-rollout
  [ "$status" -eq 0 ]
  assert_called kubectl "set image deploy/cart app=registry.lab:5000/shop/cart:v1.3.0"
}

@test "bad-deploy-rollout: resolve with nothing to recover from says how to roll back" {
  IMAGE=registry.lab:5000/shop/cart:v99.9.9 resolve bad-deploy-rollout
  [[ "$output" == *"rollout undo deploy/cart"* ]] || false
  refute_called kubectl "set image"
}

@test "bad-deploy-rollout: resolve on a healthy lab changes no image" {
  resolve bad-deploy-rollout
  [ "$status" -eq 0 ]
  refute_called kubectl "set image"
}

@test "bad-deploy-rollout: resolve after a hand fix leaves the release the learner shipped" {
  RECORD_original_image=registry.lab:5000/shop/cart:v1.4.0 IMAGE=registry.lab:5000/shop/cart:v1.5.0 \
    resolve bad-deploy-rollout
  [ "$status" -eq 0 ]
  refute_called kubectl "set image"
  assert_called kubectl "delete configmap labfault-bad-deploy-rollout"
}

# ── oom-kill ──────────────────────────────────────────────────────────────────

@test "oom-kill: inject sizes the limit from the working set and records the originals" {
  MEM_LIMIT=256Mi MEM_REQUEST=128Mi inject oom-kill
  [ "$status" -eq 0 ]
  assert_called kubectl "set resources deploy/cart --limits=memory=60Mi --requests=memory=30Mi"
  assert_called kubectl "--from-literal=original-limit=256Mi --from-literal=original-request=128Mi"
}

@test "oom-kill: inject records an unset or zero memory setting as none" {
  MEM_LIMIT=0 inject oom-kill
  assert_called kubectl "--from-literal=original-limit=none --from-literal=original-request=none"
}

@test "oom-kill: inject sizes from the request when there are no metrics" {
  WORKING_SET="" MEM_REQUEST=128Mi inject oom-kill
  assert_called kubectl "set resources deploy/cart --limits=memory=32Mi --requests=memory=16Mi"
}

@test "oom-kill: a second inject changes nothing" {
  RECORD_original_limit=256Mi MEM_LIMIT=60Mi inject oom-kill
  [ "$status" -eq 0 ]
  [[ "$output" == *"already injected"* ]] || false
  refute_called kubectl "set resources"
}

@test "oom-kill: a record left by a hand fix does not stop a new inject" {
  RECORD_original_limit=256Mi MEM_LIMIT=256Mi inject oom-kill
  [ "$status" -eq 0 ]
  assert_called kubectl "set resources deploy/cart --limits=memory=60Mi"
}

@test "oom-kill: resolve restores the recorded limit and request" {
  RECORD_original_limit=256Mi RECORD_original_request=128Mi resolve oom-kill
  [ "$status" -eq 0 ]
  assert_called kubectl "set resources deploy/cart --limits=memory=256Mi"
  assert_called kubectl "set resources deploy/cart --requests=memory=128Mi"
}

@test "oom-kill: resolve removes settings the workload never had instead of writing zero" {
  RECORD_original_limit=none RECORD_original_request=none resolve oom-kill
  [ "$status" -eq 0 ]
  assert_called kubectl '"op":"remove","path":"/spec/template/spec/containers/0/resources/limits/memory"'
  assert_called kubectl '"op":"remove","path":"/spec/template/spec/containers/0/resources/requests/memory"'
  refute_called kubectl "set resources"
}

@test "oom-kill: resolve with no record leaves the workload's own memory settings alone" {
  MEM_LIMIT=256Mi MEM_REQUEST=128Mi resolve oom-kill
  [ "$status" -eq 0 ]
  [[ "$output" == *"No record of the original memory settings"* ]] || false
  refute_called kubectl "set resources"
  refute_called kubectl "patch"
}

@test "oom-kill: resolve stops the traffic inject started" {
  resolve oom-kill
  assert_called kubectl "delete job traffic-k6"
}

# ── network-blackhole ─────────────────────────────────────────────────────────

deny_all='["Ingress"]|'
opened='["Ingress"]|[{"from":[{"namespaceSelector":{}}]}]'

@test "network-blackhole: inject applies an unlabelled deny-all under the documented name and turns connections over" {
  inject network-blackhole
  [ "$status" -eq 0 ]
  grep -q 'kind: NetworkPolicy' "$STUB_DIR/applied.yaml"
  grep -q 'name: default-deny-ingress' "$STUB_DIR/applied.yaml"
  grep -q 'namespace: shop' "$STUB_DIR/applied.yaml"
  ! grep -q 'labels:' "$STUB_DIR/applied.yaml"
  ! grep -q 'TARGET_' "$STUB_DIR/applied.yaml"
  assert_called kubectl "--from-literal=policy=default-deny-ingress"
  assert_called kubectl "-n shop rollout restart deploy/cart"
}

# The lab's traffic generator calls the workload's Service directly, so its
# requests never reach the ingress: the page has to fire on what k6 sees too.
@test "network-blackhole: the page fires on the load generator's failures as well as the ingress's" {
  inject network-blackhole
  [ "$status" -eq 0 ]
  rule="$STUB_DIR/applied-monitoring.yaml"
  grep -q 'alert: LabFaultIngressErrors' "$rule"
  grep -qF 'traefik_service_requests_total{service=~"shop-cart-.*",code=~"5.."}' "$rule"
  grep -qF 'k6_http_reqs_total{url=~"https?://cart\\..*",expected_response="false"}' "$rule"
  ! grep -q 'TARGET_' "$rule"
}

@test "network-blackhole: a second inject changes nothing" {
  BLACKHOLE_POLICY="$deny_all" inject network-blackhole
  [ "$status" -eq 0 ]
  [[ "$output" == *"already injected"* ]] || false
  refute_called kubectl "apply"
  refute_called kubectl "rollout restart"
}

@test "network-blackhole: inject refuses to overwrite a policy of that name it did not create" {
  BLACKHOLE_POLICY="$opened" inject network-blackhole
  [ "$status" -ne 0 ]
  [[ "$output" == *"already exists in shop"* ]] || false
  refute_called kubectl "apply"
  refute_called kubectl "rollout restart"
}

@test "network-blackhole: inject re-arms over its own policy after the learner opened it" {
  BLACKHOLE_POLICY="$opened" RECORD_policy=default-deny-ingress inject network-blackhole
  [ "$status" -eq 0 ]
  grep -q 'name: default-deny-ingress' "$STUB_DIR/applied.yaml"
}

@test "network-blackhole: resolve removes the policy it recorded, and is safe to run twice" {
  BLACKHOLE_POLICY="$deny_all" RECORD_policy=default-deny-ingress resolve network-blackhole
  [ "$status" -eq 0 ]
  assert_called kubectl "delete networkpolicy default-deny-ingress -n shop --ignore-not-found"
  resolve network-blackhole
  [ "$status" -eq 0 ]
}

@test "network-blackhole: resolve leaves a policy of that name it has no record of creating" {
  BLACKHOLE_POLICY="$deny_all" resolve network-blackhole
  [ "$status" -eq 0 ]
  [[ "$output" == *"leaving it in place"* ]] || false
  [[ "$output" == *"kubectl -n shop delete networkpolicy default-deny-ingress"* ]] || false
  refute_called kubectl "delete networkpolicy default-deny-ingress"
}

@test "network-blackhole: resolve also removes the policy under the name older labs carry" {
  resolve network-blackhole
  assert_called kubectl "delete networkpolicy labfault-network-blackhole -n shop --ignore-not-found"
}

# ── noisy-neighbor ────────────────────────────────────────────────────────────

neighbor() {
  export TARGET_NAMESPACE=labfault-batch TARGET_WORKLOAD=report-batch
}

@test "noisy-neighbor: inject pins one burner per core to the victim's node" {
  neighbor
  inject noisy-neighbor
  [ "$status" -eq 0 ]
  grep -q 'values: \["node-1"\]' "$STUB_DIR/applied.yaml"
  grep -q 'replicas: 4' "$STUB_DIR/applied.yaml"
  grep -q 'namespace: labfault-batch' "$STUB_DIR/applied.yaml"
  ! grep -q 'BURNER_\|TARGET_' "$STUB_DIR/applied.yaml"
}

@test "noisy-neighbor: inject caps the burners at six and never runs fewer than two" {
  neighbor
  NODE_CPU=16 inject noisy-neighbor
  grep -q 'replicas: 6' "$STUB_DIR/applied.yaml"
  : >"$STUB_DIR/applied.yaml"
  NODE_CPU=1000m inject noisy-neighbor
  grep -q 'replicas: 2' "$STUB_DIR/applied.yaml"
}

@test "noisy-neighbor: inject refuses when the victim app is not running" {
  neighbor
  VICTIM_PODS="" inject noisy-neighbor
  [ "$status" -ne 0 ]
  [[ "$output" == *"labctl app deploy cart"* ]] || false
  refute_called kubectl "apply"
}

@test "noisy-neighbor: resolve deletes the batch namespace without waiting" {
  neighbor
  resolve noisy-neighbor
  [ "$status" -eq 0 ]
  assert_called kubectl "delete namespace labfault-batch --ignore-not-found --wait=false"
}

# ── pending-unschedulable ─────────────────────────────────────────────────────

@test "pending-unschedulable: inject asks for a core more than the largest node and records the originals" {
  inject pending-unschedulable
  [ "$status" -eq 0 ]
  assert_called kubectl '"replicas":4,"template":'
  assert_called kubectl '"name":"app","resources":{"requests":{"cpu":"5"},"limits":{"cpu":"5"}}}'
  assert_called kubectl "--from-literal=original-replicas=1 --from-literal=original-request=50m --from-literal=original-limit=200m --from-literal=fault-request=5 --from-literal=launch-replicas=4"
}

@test "pending-unschedulable: inject scales and resizes in one change" {
  REPLICAS=3 inject pending-unschedulable
  [ "$status" -eq 0 ]
  refute_called kubectl "scale deploy"
  refute_called kubectl "set resources"
  assert_call_count kubectl 1 "patch"
}

@test "pending-unschedulable: inject adds two replicas to a workload already at the launch size" {
  REPLICAS=4 inject pending-unschedulable
  [ "$status" -eq 0 ]
  assert_called kubectl '"replicas":6,'
}

@test "pending-unschedulable: inject sets no limit on a workload that had none" {
  CPU_LIMIT="" inject pending-unschedulable
  assert_called kubectl '"resources":{"requests":{"cpu":"5"}}}'
  assert_called kubectl "--from-literal=original-limit=none"
}

@test "pending-unschedulable: inject sizes the request from the largest node" {
  NODE_ALLOCATABLE="2 16 7500m " inject pending-unschedulable
  assert_called kubectl '"requests":{"cpu":"17"}'
}

@test "pending-unschedulable: inject refuses a workload an autoscaler scales" {
  HPA_TARGET=cart inject pending-unschedulable
  [ "$status" -ne 0 ]
  [[ "$output" == *"HorizontalPodAutoscaler manages shop/cart"* ]] || false
  refute_called kubectl "patch"
  refute_called kubectl "create configmap"
}

@test "pending-unschedulable: inject fails with a reason when the nodes cannot be read" {
  NODE_ALLOCATABLE="" inject pending-unschedulable
  [ "$status" -ne 0 ]
  [[ "$output" == *"cannot read the nodes' allocatable CPU"* ]] || false
  refute_called kubectl "patch"
}

@test "pending-unschedulable: inject refuses when the cluster answers but the workload is missing" {
  REPLICAS="" inject pending-unschedulable
  [ "$status" -ne 0 ]
  [[ "$output" == *"deployment shop/cart not found"* ]] || false
  refute_called kubectl "patch"
}

@test "pending-unschedulable: inject refuses a workload that already asks for more CPU than any node has" {
  CPU_REQUEST=4500m inject pending-unschedulable
  [ "$status" -ne 0 ]
  [[ "$output" == *"already requests 4500m CPU"* ]] || false
  refute_called kubectl "patch"
  refute_called kubectl "create configmap"
}

@test "pending-unschedulable: a second inject changes nothing" {
  RECORD_fault_request=5 CPU_REQUEST=5 inject pending-unschedulable
  [ "$status" -eq 0 ]
  [[ "$output" == *"already injected"* ]] || false
  refute_called kubectl "patch"
  refute_called kubectl "create configmap"
}

@test "pending-unschedulable: resolve puts back the request, limit and replica count" {
  RECORD_original_replicas=1 RECORD_original_request=50m RECORD_original_limit=200m \
    RECORD_fault_request=5 RECORD_launch_replicas=4 CPU_REQUEST=5 CPU_LIMIT=5 REPLICAS=4 \
    resolve pending-unschedulable
  [ "$status" -eq 0 ]
  assert_called kubectl "set resources deploy/cart --requests=cpu=50m --limits=cpu=200m"
  assert_called kubectl "scale deploy/cart --replicas=1"
}

@test "pending-unschedulable: resolve removes CPU settings the workload never had" {
  RECORD_original_replicas=1 RECORD_original_request=none RECORD_original_limit=none \
    RECORD_fault_request=5 RECORD_launch_replicas=4 CPU_REQUEST=5 CPU_LIMIT="" REPLICAS=4 \
    resolve pending-unschedulable
  [ "$status" -eq 0 ]
  assert_called kubectl '"op":"remove","path":"/spec/template/spec/containers/0/resources/requests/cpu"'
  refute_called kubectl "set resources"
}

@test "pending-unschedulable: resolve after a hand fix keeps the learner's request and scale" {
  RECORD_original_replicas=1 RECORD_original_request=50m RECORD_original_limit=200m \
    RECORD_fault_request=5 RECORD_launch_replicas=4 CPU_REQUEST=100m CPU_LIMIT=500m REPLICAS=4 \
    resolve pending-unschedulable
  [ "$status" -eq 0 ]
  [[ "$output" == *"already fixed"* ]] || false
  refute_called kubectl "set resources"
  refute_called kubectl "scale deploy"
  assert_called kubectl "delete configmap labfault-pending-unschedulable"
}

@test "pending-unschedulable: resolve after a half fix puts back only the limit the fault raised" {
  RECORD_original_replicas=1 RECORD_original_request=50m RECORD_original_limit=200m \
    RECORD_fault_request=5 RECORD_launch_replicas=4 CPU_REQUEST=100m CPU_LIMIT=5 REPLICAS=4 \
    resolve pending-unschedulable
  [ "$status" -eq 0 ]
  assert_called kubectl "set resources deploy/cart --limits=cpu=200m"
  refute_called kubectl "--requests"
  refute_called kubectl "scale deploy"
}

@test "pending-unschedulable: resolve after a half fix that still does not schedule puts back the originals" {
  RECORD_original_replicas=1 RECORD_original_request=50m RECORD_original_limit=200m \
    RECORD_fault_request=5 RECORD_launch_replicas=4 CPU_REQUEST=4500m CPU_LIMIT=5 REPLICAS=4 \
    UNSCHEDULED_PODS=pod/cart-new-1 resolve pending-unschedulable
  [ "$status" -eq 0 ]
  assert_called kubectl "set resources deploy/cart --requests=cpu=50m --limits=cpu=200m"
  assert_called kubectl "scale deploy/cart --replicas=1"
}

@test "pending-unschedulable: resolve with no record changes nothing and says how to fix it" {
  CPU_REQUEST=5 resolve pending-unschedulable
  [ "$status" -eq 0 ]
  [[ "$output" == *"set resources deploy/cart --requests=cpu=<value>"* ]] || false
  refute_called kubectl "set resources"
  refute_called kubectl "scale deploy"
}

# ── liveness-probe-kill ───────────────────────────────────────────────────────

@test "liveness-probe-kill: inject renames the probe path and records the original probe" {
  inject liveness-probe-kill
  [ "$status" -eq 0 ]
  assert_called kubectl '"path":"/spec/template/spec/containers/0/livenessProbe/httpGet/path","value":"/healthz"'
  assert_called kubectl "--from-literal=original-path=/health"
  assert_called kubectl '--from-literal=original-probe={"httpGet":{"path":"/health","port":"http"}}'
}

@test "liveness-probe-kill: inject starts traffic at the workload's Service" {
  inject liveness-probe-kill
  assert_called kubectl "delete job traffic-k6"
  grep -q 'http://cart.shop.svc.cluster.local:8080/' "$STUB_DIR/applied.yaml"
}

@test "liveness-probe-kill: inject refuses a workload with no HTTP liveness probe" {
  LIVENESS_PATH="" inject liveness-probe-kill
  [ "$status" -ne 0 ]
  [[ "$output" == *"has no HTTP liveness probe"* ]] || false
  refute_called kubectl "patch"
  refute_called kubectl "create configmap"
}

@test "liveness-probe-kill: a second inject changes nothing" {
  LIVENESS_PATH=/healthz inject liveness-probe-kill
  [ "$status" -eq 0 ]
  [[ "$output" == *"already injected"* ]] || false
  refute_called kubectl "patch"
  refute_called kubectl "create configmap"
}

@test "liveness-probe-kill: resolve puts back the recorded path while the probe fails" {
  RECORD_original_path=/health LIVENESS_PATH=/healthz POD_GET=fail resolve liveness-probe-kill
  [ "$status" -eq 0 ]
  assert_called kubectl '"op":"replace","path":"/spec/template/spec/containers/0/livenessProbe/httpGet/path","value":"/health"'
}

@test "liveness-probe-kill: resolve after a half fix to a path that still fails puts back the original" {
  RECORD_original_path=/health LIVENESS_PATH=/healthy POD_GET=fail resolve liveness-probe-kill
  [ "$status" -eq 0 ]
  assert_called kubectl '"value":"/health"'
}

@test "liveness-probe-kill: resolve leaves a path the learner chose that the app answers" {
  RECORD_original_path=/health LIVENESS_PATH=/ready resolve liveness-probe-kill
  [ "$status" -eq 0 ]
  [[ "$output" == *"already fixed"* ]] || false
  refute_called kubectl "patch"
  assert_called kubectl "delete configmap labfault-liveness-probe-kill"
}

@test "liveness-probe-kill: resolve puts back a probe the learner deleted" {
  RECORD_original_path=/health RECORD_original_probe='{"httpGet":{"path":"/health","port":"http"}}' \
    LIVENESS_PROBE="" LIVENESS_PATH="" resolve liveness-probe-kill
  [ "$status" -eq 0 ]
  assert_called kubectl '"op":"add","path":"/spec/template/spec/containers/0/livenessProbe","value":{"httpGet":{"path":"/health","port":"http"}}'
}

@test "liveness-probe-kill: resolve with no record changes nothing and says how to fix it" {
  LIVENESS_PATH=/healthz POD_GET=fail resolve liveness-probe-kill
  [ "$status" -eq 0 ]
  [[ "$output" == *"No record of the original probe path"* ]] || false
  refute_called kubectl "patch"
}

@test "liveness-probe-kill: resolve stops the traffic inject started" {
  resolve liveness-probe-kill
  assert_called kubectl "delete job traffic-k6"
}

# ── missing-secret-key ────────────────────────────────────────────────────────

@test "missing-secret-key: inject renames the key the workload reads and replaces its pods" {
  inject missing-secret-key
  [ "$status" -eq 0 ]
  assert_called kubectl '-n shop patch secret cart-secret --type=json -p'
  assert_called kubectl '{"op":"add","path":"/data/API_TOKEN","value":"dG9rZW4="},{"op":"remove","path":"/data/api-token"}'
  assert_called kubectl "--from-literal=env=API_TOKEN --from-literal=secret=cart-secret --from-literal=original-key=api-token --from-literal=new-key=API_TOKEN"
  patched="$(line_of "patch secret cart-secret")"
  deleted="$(line_of "delete pod -l app.kubernetes.io/name=cart --wait=false")"
  [ -n "$deleted" ]
  [ "$patched" -lt "$deleted" ]
}

@test "missing-secret-key: inject gives an already variable-style key a new name" {
  ENV_REFS="TOKEN|cart-secret|TOKEN" SECRET_KEYS=TOKEN inject missing-secret-key
  assert_called kubectl '"path":"/data/TOKEN_V2"'
}

@test "missing-secret-key: inject refuses a workload that reads no Secret key" {
  ENV_REFS="PORT||" inject missing-secret-key
  [ "$status" -ne 0 ]
  [[ "$output" == *"reads no environment variable from a Secret key"* ]] || false
  refute_called kubectl "patch"
  refute_called kubectl "delete pod"
}

@test "missing-secret-key: a second inject changes nothing" {
  RECORD_new_key=API_TOKEN SECRET_KEYS=API_TOKEN inject missing-secret-key
  [ "$status" -eq 0 ]
  [[ "$output" == *"already injected"* ]] || false
  refute_called kubectl "patch"
  refute_called kubectl "delete pod"
}

record_rotation() {
  export RECORD_env=API_TOKEN RECORD_secret=cart-secret RECORD_original_key=api-token RECORD_new_key=API_TOKEN
}

@test "missing-secret-key: resolve renames the key back and restores the reference" {
  record_rotation
  SECRET_KEYS=API_TOKEN resolve missing-secret-key
  [ "$status" -eq 0 ]
  assert_called kubectl '{"op":"add","path":"/data/api-token","value":"dG9rZW4="},{"op":"remove","path":"/data/API_TOKEN"}'
  assert_called kubectl '"secretKeyRef":{"name":"cart-secret","key":"api-token","optional":null}'
}

@test "missing-secret-key: resolve after the reference was pointed at the new key leaves both alone" {
  record_rotation
  ENV_REFS="API_TOKEN|cart-secret|API_TOKEN" SECRET_KEYS=API_TOKEN resolve missing-secret-key
  [ "$status" -eq 0 ]
  [[ "$output" == *"already fixed"* ]] || false
  refute_called kubectl "patch"
  assert_called kubectl "delete configmap labfault-missing-secret-key"
}

@test "missing-secret-key: resolve after the key was put back leaves the Secret alone" {
  record_rotation
  SECRET_KEYS="$(printf 'api-token\nAPI_TOKEN')" resolve missing-secret-key
  [ "$status" -eq 0 ]
  [[ "$output" == *"already fixed"* ]] || false
  refute_called kubectl "patch"
}

@test "missing-secret-key: resolve puts back a variable the learner removed" {
  record_rotation
  ENV_REFS="PORT||" SECRET_KEYS=API_TOKEN resolve missing-secret-key
  [ "$status" -eq 0 ]
  assert_called kubectl '"env":[{"name":"API_TOKEN","value":null,"valueFrom":{"secretKeyRef":{"name":"cart-secret","key":"api-token"'
}

@test "missing-secret-key: resolve with no key to restore from says to re-apply the chart" {
  record_rotation
  SECRET_KEYS=other resolve missing-secret-key
  [ "$status" -eq 0 ]
  [[ "$output" == *"labctl app deploy cart"* ]] || false
  refute_called kubectl "patch secret"
}

@test "missing-secret-key: resolve with no record changes nothing and says how to look" {
  SECRET_KEYS=API_TOKEN resolve missing-secret-key
  [ "$status" -eq 0 ]
  [[ "$output" == *"No record of the rotated Secret key"* ]] || false
  refute_called kubectl "patch"
}

# ── cpu-throttling ────────────────────────────────────────────────────────────

@test "cpu-throttling: inject sets the limit to a third of the measured demand and records it" {
  TOP_CPU=45m inject cpu-throttling
  [ "$status" -eq 0 ]
  assert_called kubectl "set resources deploy/cart --limits=cpu=15m --requests=cpu=15m"
  assert_called kubectl "--from-literal=original-request=50m --from-literal=original-limit=200m --from-literal=demand-millicores=45 --from-literal=fault-limit=15m"
}

@test "cpu-throttling: inject measures under the steady load it starts" {
  TOP_CPU=45m inject cpu-throttling
  measured="$(line_of "top pod -n shop -l app.kubernetes.io/name=cart")"
  changed="$(line_of "set resources deploy/cart")"
  traffic="$(line_of "delete job traffic-k6")"
  [ -n "$traffic" ] && [ -n "$measured" ]
  [ "$traffic" -lt "$measured" ]
  [ "$measured" -lt "$changed" ]
}

@test "cpu-throttling: inject samples until the reading has changed three times under the load" {
  TOP_SEQ="1m 1m 8m 8m 28m 28m 36m 36m 40m" inject cpu-throttling
  [ "$status" -eq 0 ]
  assert_call_count kubectl 7 "top pod"
  assert_called kubectl "--from-literal=demand-millicores=36"
  assert_called kubectl "--limits=cpu=12m"
}

@test "cpu-throttling: inject stops sampling after 25 tries when the reading never moves" {
  TOP_CPU=30m inject cpu-throttling
  assert_call_count kubectl 26 "top pod"
}

@test "cpu-throttling: inject keeps a request already below the new limit" {
  TOP_CPU=60m CPU_REQUEST=5m inject cpu-throttling
  assert_called kubectl "set resources deploy/cart --limits=cpu=20m --requests=cpu=5m"
}

@test "cpu-throttling: inject never sets a limit below what the kernel enforces" {
  TOP_CPU=12m inject cpu-throttling
  assert_called kubectl "--limits=cpu=10m"
}

@test "cpu-throttling: inject sizes for go-api at 60 rps when there are no metrics" {
  TOP_CPU="" inject cpu-throttling
  assert_called kubectl "--from-literal=demand-millicores=40"
  assert_called kubectl "--limits=cpu=13m"
}

@test "cpu-throttling: a second inject changes nothing" {
  RECORD_fault_limit=15m CPU_LIMIT=15m inject cpu-throttling
  [ "$status" -eq 0 ]
  [[ "$output" == *"already injected"* ]] || false
  refute_called kubectl "set resources"
  refute_called kubectl "delete job traffic-k6"
}

throttle_record() {
  export RECORD_original_request=50m RECORD_original_limit=200m RECORD_demand_millicores=45 RECORD_fault_limit=15m
}

@test "cpu-throttling: resolve puts back the original CPU settings while the limit is too low" {
  throttle_record
  while read -r limit; do
    reset_calls
    CPU_LIMIT="$limit" resolve cpu-throttling
    [ "$status" -eq 0 ] || { echo "limit=$limit: $output"; return 1; }
    assert_called kubectl "set resources deploy/cart --limits=cpu=200m --requests=cpu=50m" || { echo "limit=$limit"; return 1; }
  done <<'LIMITS'
15m
40m
LIMITS
}

@test "cpu-throttling: resolve after a hand fix keeps the learner's limit" {
  throttle_record
  while read -r limit; do
    reset_calls
    CPU_LIMIT="$limit" resolve cpu-throttling
    [[ "$output" == *"already fixed"* ]] || { echo "limit=$limit: $output"; return 1; }
    refute_called kubectl "set resources" || { echo "limit=$limit"; return 1; }
  done <<'LIMITS'
100m
1
LIMITS
  CPU_LIMIT="" resolve cpu-throttling
  [[ "$output" == *"already fixed"* ]] || false
}

@test "cpu-throttling: resolve removes CPU settings the workload never had" {
  throttle_record
  RECORD_original_request=none RECORD_original_limit=none CPU_LIMIT=15m resolve cpu-throttling
  [ "$status" -eq 0 ]
  assert_called kubectl '"op":"remove","path":"/spec/template/spec/containers/0/resources/limits/cpu"'
  assert_called kubectl '"op":"remove","path":"/spec/template/spec/containers/0/resources/requests/cpu"'
  refute_called kubectl "set resources"
}

@test "cpu-throttling: resolve with no record changes nothing and says how to fix it" {
  CPU_LIMIT=15m resolve cpu-throttling
  [ "$status" -eq 0 ]
  [[ "$output" == *"No record of the original CPU settings"* ]] || false
  refute_called kubectl "set resources"
}

@test "cpu-throttling: resolve stops the traffic inject started" {
  resolve cpu-throttling
  assert_called kubectl "delete job traffic-k6"
}

# ── dns-egress-blocked ────────────────────────────────────────────────────────

no_dns='[{"ports":[{"port":8080,"protocol":"TCP"}],"to":[{"namespaceSelector":{}}]}]'
with_dns='[{"ports":[{"port":8080,"protocol":"TCP"}]},{"ports":[{"port":53,"protocol":"UDP"}]}]'
upstream=http://echo-server.echo-server.svc.cluster.local:8080/

@test "dns-egress-blocked: inject denies egress but the upstream's port, and points the app at it" {
  inject dns-egress-blocked
  [ "$status" -eq 0 ]
  grep -q 'name: default-deny-egress' "$STUB_DIR/applied.yaml"
  grep -q 'namespace: shop' "$STUB_DIR/applied.yaml"
  grep -q 'kubernetes.io/metadata.name: echo-server' "$STUB_DIR/applied.yaml"
  grep -q 'port: 8080' "$STUB_DIR/applied.yaml"
  ! grep -q -e 'port: 53' -e 'labels:' -e 'UPSTREAM_' -e 'TARGET_' "$STUB_DIR/applied.yaml"
  assert_called kubectl "set env deploy/cart UPSTREAM_URL=$upstream"
  assert_called kubectl "--from-literal=policy=default-deny-egress --from-literal=original-upstream=none --from-literal=upstream=$upstream"
}

@test "dns-egress-blocked: inject keeps an upstream the app already calls and turns its pods over" {
  UPSTREAM_NOW=http://payments.shop.svc:9000/ inject dns-egress-blocked
  [ "$status" -eq 0 ]
  refute_called kubectl "set env"
  assert_called kubectl "-n shop rollout restart deploy/cart"
  assert_called kubectl "--from-literal=original-upstream=http://payments.shop.svc:9000/ --from-literal=upstream=http://payments.shop.svc:9000/"
}

@test "dns-egress-blocked: inject undoes itself when the app does not report an upstream" {
  RECORD_policy=default-deny-egress RECORD_original_upstream=none READY_BODY='{"status":"ready"}' inject dns-egress-blocked
  [ "$status" -ne 0 ]
  [[ "$output" == *"does not call an upstream"* ]] || false
  [[ "$output" == *"labctl app build cart && labctl app deploy cart"* ]] || false
  assert_called kubectl "delete networkpolicy default-deny-egress -n shop --ignore-not-found"
  assert_called kubectl "delete configmap labfault-dns-egress-blocked"
}

@test "dns-egress-blocked: inject asks a new pod, not one still terminating after the rollout" {
  RUNNING_PODS="cart-old 2026-10-29T10:29:20Z
cart-abc " inject dns-egress-blocked
  [ "$status" -eq 0 ]
  [[ "$output" != *"predates UPSTREAM_URL"* ]] || false
  assert_called kubectl "get --raw /api/v1/namespaces/shop/pods/cart-abc:8080/proxy/ready"
}

@test "dns-egress-blocked: a second inject changes nothing" {
  EGRESS_POLICY="$no_dns" inject dns-egress-blocked
  [ "$status" -eq 0 ]
  [[ "$output" == *"already injected"* ]] || false
  refute_called kubectl "apply -n shop"
  refute_called kubectl "set env"
}

@test "dns-egress-blocked: inject refuses to overwrite a working policy of that name it did not create" {
  EGRESS_POLICY="$with_dns" inject dns-egress-blocked
  [ "$status" -ne 0 ]
  [[ "$output" == *"already exists in shop"* ]] || false
  refute_called kubectl "apply -n shop"
}

@test "dns-egress-blocked: resolve removes the recorded policy and the upstream it set" {
  RECORD_policy=default-deny-egress RECORD_original_upstream=none resolve dns-egress-blocked
  [ "$status" -eq 0 ]
  assert_called kubectl "delete networkpolicy default-deny-egress -n shop --ignore-not-found"
  assert_called kubectl "delete networkpolicy allow-dns -n shop --ignore-not-found"
  assert_called kubectl "-n shop set env deploy/cart UPSTREAM_URL-"
}

@test "dns-egress-blocked: resolve puts back the upstream the app had" {
  RECORD_policy=default-deny-egress RECORD_original_upstream=http://payments.shop.svc:9000/ resolve dns-egress-blocked
  assert_called kubectl "-n shop set env deploy/cart UPSTREAM_URL=http://payments.shop.svc:9000/"
}

@test "dns-egress-blocked: resolve leaves a policy of that name it has no record of creating" {
  EGRESS_POLICY="$no_dns" resolve dns-egress-blocked
  [ "$status" -eq 0 ]
  [[ "$output" == *"leaving it in place"* ]] || false
  refute_called kubectl "delete networkpolicy"
  refute_called kubectl "set env"
}

@test "dns-egress-blocked: resolve stops the traffic inject started" {
  resolve dns-egress-blocked
  assert_called kubectl "delete job traffic-k6"
}
