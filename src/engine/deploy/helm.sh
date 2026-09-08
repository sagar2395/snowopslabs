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
# Optional: an app deployed from the shared chart has no values file of its own.
# The per-app branch below still requires it.
HELM_VALUES="${HELM_VALUES:-}"
NAMESPACE="${NAMESPACE:-${APP_NAME}}" # default to app name
HELM_WAIT_TIMEOUT="${HELM_WAIT_TIMEOUT:-5m}"

HELM_CHART_PATH="apps/${APP_NAME}/deploy/helm"

# An app brought as a pre-built image has no chart of its own. Fall back to the
# shared workload chart, driven entirely by the app's declared contract so the
# deployed pod and the declaration cannot disagree.
SHARED_VALUES=()
if [ ! -d "${HELM_CHART_PATH}" ]; then
  HELM_CHART_PATH="apps/_shared/chart"
  if [ ! -d "${HELM_CHART_PATH}" ]; then
    echo "ERROR: no chart at apps/${APP_NAME}/deploy/helm and no shared chart at ${HELM_CHART_PATH}" >&2
    exit 1
  fi
  echo "[chart] ${APP_NAME} has no chart of its own — using the shared workload chart"
  SHARED_VALUES=(
    --set "appName=${APP_NAME}"
    --set "namespace=${NAMESPACE}"
    --set "image.reference=${APP_IMAGE:-}"
    --set "image.repository=${APP_NAME}"
    --set "image.tag=${DOCKER_IMAGE_TAG:-latest}"
    --set "port=${APP_PORT:-8080}"
    --set "probes.healthPath=${APP_HEALTH_PATH:-/health}"
    --set "probes.readyPath=${APP_READY_PATH:-/ready}"
    --set "metrics.path=${APP_METRICS_PATH:-/metrics}"
    --set "ingress.className=${INGRESS_CLASS:-traefik}"
    --set "ingress.host=${APP_NAME}.${DOMAIN_SUFFIX:-k3d.local}"
  )
  # Extra writable paths for a hardened image that needs more than /tmp.
  if [ -n "${APP_WRITABLE_PATHS:-}" ]; then
    SHARED_VALUES+=(--set "writablePaths={/tmp,${APP_WRITABLE_PATHS}}")
  fi

  # An app that does not claim prometheus-metrics must not be annotated for
  # scraping, or Prometheus logs a scrape failure for every one of its pods.
  case ",${APP_CAPABILITIES:-}," in
    *,prometheus-metrics,*) : ;;
    *) SHARED_VALUES+=(--set "metrics.enabled=false") ;;
  esac
fi

# The shared chart carries its own defaults and creates no namespace of its own;
# a per-app chart is configured by its values file and has a namespace template
# that helm.sh disables, because it creates the namespace itself just above.
if [ ${#SHARED_VALUES[@]} -gt 0 ]; then
  VALUES_ARGS=("${SHARED_VALUES[@]}")
else
  if [ -z "${HELM_VALUES}" ]; then
    echo "ERROR: apps/${APP_NAME} has its own chart, so app.env must define HELM_VALUES" >&2
    exit 1
  fi
  VALUES_ARGS=(-f "${HELM_CHART_PATH}/${HELM_VALUES}" --set namespace.create=false)
fi

case "${COMMAND}" in
  deploy)
    echo "Deploying ${APP_NAME} to ${NAMESPACE} namespace..."

    echo "[lint] Linting chart with values..."
    if ! helm lint "${HELM_CHART_PATH}" "${VALUES_ARGS[@]}"; then
      echo "[lint] ERROR: chart failed lint — aborting deploy" >&2
      exit 1
    fi
    echo "[lint] OK"

    echo "[dry-run] Rendering chart templates..."
    if ! helm upgrade --install "${HELM_RELEASE}" "${HELM_CHART_PATH}" \
      "${VALUES_ARGS[@]}" \
      --namespace "${NAMESPACE}" --create-namespace \
      --dry-run 2>&1; then
      echo "[dry-run] ERROR: dry-run failed — aborting deploy" >&2
      exit 1
    fi
    echo "[dry-run] OK"

    # ensure namespace exists (kubectl apply is idempotent)
    kubectl create namespace "${NAMESPACE}" --dry-run=client -o yaml | kubectl apply -f - >/dev/null 2>&1 || true

    helm upgrade --install "${HELM_RELEASE}" "${HELM_CHART_PATH}" \
      "${VALUES_ARGS[@]}" \
      --namespace "${NAMESPACE}" --create-namespace

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
