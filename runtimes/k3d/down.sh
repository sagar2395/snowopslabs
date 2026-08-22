#!/usr/bin/env bash
set -euo pipefail

# Colors for output
RED='\033[0;31m'
GREEN='\033[0;32m'
YELLOW='\033[1;33m'
NC='\033[0m' # No Color

CLUSTER_NAME="${1:-${CLUSTER_NAME:-snowops}}"

echo -e "${YELLOW}Shutting down k3d cluster '${CLUSTER_NAME}'...${NC}"

if ! k3d cluster list "$CLUSTER_NAME" &>/dev/null; then
  echo -e "${YELLOW}Cluster '$CLUSTER_NAME' not found.${NC}"
  exit 0
fi

k3d cluster delete "$CLUSTER_NAME" || {
  echo -e "${RED}Failed to delete cluster: $CLUSTER_NAME${NC}"
  exit 1
}

echo -e "${YELLOW}Validating cluster shutdown...${NC}"

if ! k3d cluster list "$CLUSTER_NAME" &>/dev/null; then
  echo -e "${GREEN}✓ Cluster '$CLUSTER_NAME' has been successfully shut down.${NC}"
  exit 0
else
  echo -e "${RED}✗ Validation failed: cluster '$CLUSTER_NAME' still exists.${NC}"
  exit 1
fi
