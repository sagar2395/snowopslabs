#!/usr/bin/env bash
# Shared helpers for incident scripts.
#
# Sourced, not executed:
#   . "$PROJECT_ROOT/incidents/_lib/render.sh"

# render_targeted <file> - print <file> with the workload placeholders replaced.
#
# A fault's alert rule names the namespace it watches, but the workload a fault
# breaks is a binding now (ADR-0014), so the rule has to follow it. The engine
# resolves {{.WorkloadName}} in fault.yaml and in the manifests it applies
# itself; this file is applied by the inject script instead, so the substitution
# happens here.
#
# The placeholders are spelled like the environment variables they take their
# values from, so a reader does not have to learn a third templating syntax:
#   ${TARGET_NAMESPACE}  ${TARGET_WORKLOAD}
render_targeted() {
  sed \
    -e "s|\${TARGET_NAMESPACE}|${TARGET_NAMESPACE:-${WORKLOAD_NAMESPACE:-go-api}}|g" \
    -e "s|\${TARGET_WORKLOAD}|${TARGET_WORKLOAD:-${WORKLOAD_NAME:-go-api}}|g" \
    "$1"
}

# render_rule <file> - print an incident's PrometheusRule rendered for this
# lab. In a silent inject (FAULT_SILENT set) the rule and its group take the
# neutral name labfault-silent, because Prometheus' Alerts page shows both and
# the incident's name would give the drill away. resolve.sh deletes that name
# too. The alert names stay: they describe the symptom, as a real page does.
render_rule() {
  if [ -n "${FAULT_SILENT:-}" ]; then
    render_targeted "$1" | sed -e 's|^\( *\(- \)\{0,1\}name:\) labfault-.*$|\1 labfault-silent|'
  else
    render_targeted "$1"
  fi
}
