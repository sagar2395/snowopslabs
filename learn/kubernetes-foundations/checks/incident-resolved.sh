#!/usr/bin/env bash
# Passes when no incident is currently active (or the active one is resolved).
set -euo pipefail
output=$(bin/labctl incident status 2>&1 || true)
echo "$output" | grep -qE 'RESOLVED|no incident'
