#!/bin/sh
# SPDX-License-Identifier: Apache-2.0
#
# Binds a scenario script to the app it acts on. Source it first thing:
#
#   . "$(dirname "$0")/../../_lib/workload.sh"
#
# A learner passes --app/--namespace (the command on the scenario page already
# carries both); the engine exports WORKLOAD_NAME/WORKLOAD_NAMESPACE instead.
# The flags are removed from "$@", so the script sees only its own arguments.

_workload_app=""
_workload_ns=""
_workload_argc=$#
while [ "$_workload_argc" -gt 0 ]; do
  _workload_arg=$1
  shift
  _workload_argc=$((_workload_argc - 1))
  case $_workload_arg in
    --app | --namespace)
      if [ "$_workload_argc" -eq 0 ]; then
        echo "ERROR: ${_workload_arg} needs a value." >&2
        exit 2
      fi
      if [ "$_workload_arg" = "--app" ]; then _workload_app=$1; else _workload_ns=$1; fi
      shift
      _workload_argc=$((_workload_argc - 1))
      ;;
    --app=*) _workload_app=${_workload_arg#--app=} ;;
    --namespace=*) _workload_ns=${_workload_arg#--namespace=} ;;
    *) set -- "$@" "$_workload_arg" ;;
  esac
done

# An explicit --app names a different workload, so the engine's namespace does
# not carry over to it.
if [ -n "$_workload_app" ]; then
  WORKLOAD_NAME=$_workload_app
  WORKLOAD_NAMESPACE=${_workload_ns:-$_workload_app}
elif [ -n "$_workload_ns" ]; then
  WORKLOAD_NAMESPACE=$_workload_ns
fi

if [ -z "${WORKLOAD_NAME:-}" ]; then
  echo "ERROR: $(basename "$0") needs to know which app to act on." >&2
  echo "       Pass --app <app> --namespace <namespace>, or copy the command from the" >&2
  echo "       scenario page or 'labctl scenario info <scenario>', which fills both in." >&2
  exit 2
fi
WORKLOAD_NAMESPACE=${WORKLOAD_NAMESPACE:-$WORKLOAD_NAME}
export WORKLOAD_NAME WORKLOAD_NAMESPACE
unset _workload_app _workload_ns _workload_argc _workload_arg
