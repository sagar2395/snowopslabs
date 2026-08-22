#!/bin/bash

# Helm operations script driven by per‑app configuration.
# The expected interface is:
#   helm.sh <command> <app-name>
# Supported commands: deploy, destroy, lint, validate
# All other variables (release name, values file, namespace) are read
# from apps/<app-name>/app.env so that makefiles can stay generic.

set -euo pipefail

COMMAND="${1:?Error: COMMAND not provided (deploy|destroy|lint|validate)}"
APP_NAME="${2:?Error: APP_NAME not provided}"

# load app configuration if it exists
if [ -f "apps/${APP_NAME}/app.env" ]; then
  set -a
  # shellcheck source=/dev/null
  . "apps/${APP_NAME}/app.env"
  set +a
fi

# expected variables from app.env
HELM_RELEASE="${HELM_RELEASE_NAME:?app.env must define HELM_RELEASE_NAME}"
HELM_VALUES="${HELM_VALUES:?app.env must define HELM_VALUES}"
NAMESPACE="${NAMESPACE:-${APP_NAME}}" # default to app name
HELM_WAIT_TIMEOUT="${HELM_WAIT_TIMEOUT:-5m}"

HELM_CHART_PATH="apps/${APP_NAME}/deploy/helm"

case "${COMMAND}" in
  deploy)
    echo "Deploying ${APP_NAME} to ${NAMESPACE} namespace..."

    echo "[lint] Linting chart with values..."
    if ! helm lint "${HELM_CHART_PATH}" -f "${HELM_CHART_PATH}/${HELM_VALUES}"; then
      echo "[lint] ERROR: chart failed lint — aborting deploy" >&2
      exit 1
    fi
    echo "[lint] OK"

    echo "[dry-run] Rendering chart templates..."
    if ! helm upgrade --install "${HELM_RELEASE}" "${HELM_CHART_PATH}" \
      -f "${HELM_CHART_PATH}/${HELM_VALUES}" \
      --namespace "${NAMESPACE}" --create-namespace \
      --set namespace.create=false \
      --dry-run 2>&1; then
      echo "[dry-run] ERROR: dry-run failed — aborting deploy" >&2
      exit 1
    fi
    echo "[dry-run] OK"

    # ensure namespace exists (kubectl apply is idempotent)
    kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null 2>&1 || true

    helm upgrade --install "${HELM_RELEASE}" "${HELM_CHART_PATH}" \
      -f "${HELM_CHART_PATH}/${HELM_VALUES}" \
      --namespace "${NAMESPACE}" --create-namespace \
      --set namespace.create=false

    echo "[rollout] Waiting for deployment to be ready (timeout: ${HELM_WAIT_TIMEOUT})..."
    if ! kubectl rollout status deployment/"${HELM_RELEASE}" \
      -n "${NAMESPACE}" \
      --timeout="${HELM_WAIT_TIMEOUT}"; then
      echo "[rollout] WARNING: rollout did not complete within ${HELM_WAIT_TIMEOUT}" >&2
      kubectl get pods -n "${NAMESPACE}" >&2
      exit 1
    fi
    echo "[rollout] OK"

    echo ""
    echo "✓ Deployment complete! Access the application:"
    echo "  - HTTP: http://${APP_NAME}.${DOMAIN_SUFFIX:-k3d.local}"
    echo "  - Metrics: http://${APP_NAME}.${DOMAIN_SUFFIX:-k3d.local}/metrics"
    echo ""
    echo "View deployment status:"
    echo "  kubectl get deployments -n ${NAMESPACE}"
    echo "  kubectl get pods -n ${NAMESPACE}"
    echo "  kubectl get svc -n ${NAMESPACE}"
    ;;

  destroy)
    echo "Uninstalling ${HELM_RELEASE} from ${NAMESPACE} namespace..."
    helm uninstall "${HELM_RELEASE}" -n "${NAMESPACE}" || true
    kubectl delete namespace "${NAMESPACE}" --ignore-not-found --timeout=60s
    echo "✓ Uninstall complete"
    ;;

  lint)
    echo "[lint] Linting Helm chart..."
    helm lint "${HELM_CHART_PATH}" -f "${HELM_CHART_PATH}/${HELM_VALUES}"
    echo "[lint] ✓ Lint complete"
    ;;

  validate)
    echo "Validating Helm chart (dry-run)..."
    echo "Using values file: ${HELM_CHART_PATH}/${HELM_VALUES}"
    helm template "${HELM_RELEASE}" "${HELM_CHART_PATH}" \
      -f "${HELM_CHART_PATH}/${HELM_VALUES}" \
      --namespace "${NAMESPACE}"
    ;;

  *)
    echo "Error: Unknown command '${COMMAND}'"
    echo "Valid commands: deploy, destroy, lint, validate"
    exit 1
    ;;
esac
