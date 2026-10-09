#!/usr/bin/env bats
# SPDX-License-Identifier: Apache-2.0
#
# The service-selector-broken and network-blackhole checks prove the fix by
# calling the workload through the ingress. They build the URL from
# INGRESS_URL_SUFFIX, which keeps the port of a lab whose ingress is not on 80.

load 'helpers/stub'

setup() {
  stub_setup
  ROOT="$(project_root)"
  export TARGET_NAMESPACE=go-api TARGET_WORKLOAD=go-api
  # A healthy lab: the Service selects the template's labels, no policy denies.
  cat >"$STUB_BIN/kubectl" <<'EOF'
#!/usr/bin/env bash
case "$*" in
  *spec.selector*|*template.metadata.labels*) echo '{"app.kubernetes.io/name":"go-api"}' ;;
esac
EOF
  # curl records the URL it was given and answers $CODE, or fails to connect.
  cat >"$STUB_BIN/curl" <<'EOF'
#!/usr/bin/env bash
for a in "$@"; do last="$a"; done
echo "$last" >"$STUB_DIR/url"
if [ "${CODE:-200}" = "000" ]; then printf '000'; exit 7; fi
printf '%s' "${CODE:-200}"
EOF
  chmod +x "$STUB_BIN/kubectl" "$STUB_BIN/curl"
}

teardown() {
  stub_teardown
}

checks() {
  echo "$ROOT/incidents/service-selector-broken/checks/resolved.sh"
  echo "$ROOT/incidents/network-blackhole/checks/resolved.sh"
}

@test "the probe keeps the ingress port from INGRESS_URL_SUFFIX" {
  for check in $(checks); do
    DOMAIN_SUFFIX=snowops.localhost INGRESS_URL_SUFFIX=snowops.localhost:8081 run bash "$check"
    [ "$status" -eq 0 ]
    [ "$(cat "$STUB_DIR/url")" = "http://go-api.snowops.localhost:8081/health" ]
  done
}

@test "without INGRESS_URL_SUFFIX the probe falls back to DOMAIN_SUFFIX" {
  for check in $(checks); do
    DOMAIN_SUFFIX=lab.internal run bash "$check"
    [ "$status" -eq 0 ]
    [ "$(cat "$STUB_DIR/url")" = "http://go-api.lab.internal/health" ]
  done
}

@test "a probe that cannot connect reports 000 once" {
  for check in $(checks); do
    CODE=000 INGRESS_URL_SUFFIX=snowops.localhost:8081 run bash "$check"
    [ "$status" -eq 1 ]
    [[ "$output" == *"returned 000."* ]]
  done
}
