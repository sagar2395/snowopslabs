#!/usr/bin/env bash
set -euo pipefail

# Encapsulate all kubectl/helm/docker checks.
# Usage:
#   engine/check.sh tools
#   engine/check.sh cluster
#   engine/check.sh ingress

_OS="$(uname -s)" # Darwin | Linux

_docker_hint() {
  echo "ERROR: Docker daemon is not running." >&2
  if [ "$_OS" = "Darwin" ]; then
    echo "  macOS: start Docker Desktop, run 'colima start', or start OrbStack." >&2
  else
    echo "  Linux: run 'sudo systemctl start docker' and ensure your user is in the 'docker' group." >&2
  fi
}

cmd="${1:-}"
case "$cmd" in
  tools)
    echo "Checking required command-line tools..."
    for bin in kubectl helm docker k3d; do
      if ! command -v "$bin" >/dev/null 2>&1; then
        echo "ERROR: $bin not found in PATH" >&2
        exit 1
      fi
    done
    if ! docker info >/dev/null 2>&1; then
      _docker_hint
      exit 1
    fi
    echo "All required tools are available and Docker daemon is running."
    ;;
  cluster)
    echo "Verifying access to Kubernetes cluster..."
    if ! kubectl version --client >/dev/null 2>&1; then
      echo "ERROR: kubectl is not functional" >&2
      exit 1
    fi
    if ! kubectl cluster-info >/dev/null 2>&1; then
      echo "ERROR: cannot reach cluster — is the container runtime running?" >&2
      _docker_hint
      exit 1
    fi
    echo "Current context: $(kubectl config current-context)"
    ;;
  ingress)
    echo "Checking platform ingress controller..."
    if ! kubectl get pods --all-namespaces -l app=traefik 2>/dev/null | grep -q Running; then
      echo "Traefik pods are not running" >&2
      exit 1
    fi
    echo "Traefik appears to be running."
    ;;
  *)
    echo "Usage: $0 {tools|cluster|ingress}" >&2
    exit 1
    ;;
esac
