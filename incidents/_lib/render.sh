#!/usr/bin/env bash
# Shared helpers for incident scripts.
#
# Sourced, not executed:
#   . "$PROJECT_ROOT/incidents/_lib/render.sh"

# render_targeted <file> — print <file> with the workload placeholders replaced.
#
# A fault's alert rule names the namespace it watches, but the workload a fault
# breaks is a binding now (ADR-0014), so the rule has to follow it. The engine
# resolves ${WORKLOAD_NAME:-go-api} in fault.yaml and in the manifests it applies
# itself; this file is applied by the inject script instead, so the substitution
# happens here.
#
# The placeholders are spelled like the environment variables they take their
# values from, so a reader does not have to learn a third templating syntax:
#   ${TARGET_NAMESPACE}  ${TARGET_WORKLOAD}
render_targeted() {
  sed \
    -e "s|\${TARGET_NAMESPACE}|${TARGET_NAMESPACE:-}|g" \
    -e "s|\${TARGET_WORKLOAD}|${TARGET_WORKLOAD:-}|g" \
    "$1"
}
