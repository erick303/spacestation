#!/usr/bin/env bash
# Build a sanitized scan root for the VHS demo so the recording doesn't
# show real ~/projects repo names. Files are sparse (`mkfile -n`) — they
# report their declared size to stat() but occupy ~0 actual disk bytes,
# so spacestation reports realistic sizes without filling /tmp.
#
# Each artifact's .bulk file gets an old mtime, which is what spacestation's
# recency walker reads as "last touched".

set -euo pipefail

ROOT="/tmp/spacestation-demo"
rm -rf "$ROOT"
mkdir -p "$ROOT"

mkfake() {
  local dir="$1" size="$2" age_days="$3"
  mkdir -p "$ROOT/$dir"
  mkfile -n "$size" "$ROOT/$dir/.bulk" >/dev/null
  touch -t "$(date -v-"${age_days}"d +%Y%m%d0000)" "$ROOT/$dir/.bulk"
}

# Node.js
mkfake "acme-api/node_modules"        2g    87
mkfake "demo-portal/node_modules"     1500m 14
mkfake "widget-store/node_modules"    1200m 73
mkfake "analytics/node_modules"       640m  45

# JS Build Output
mkfake "acme-api/.next"               350m  21
mkfake "dashboard-ui/.next"           420m  9
mkfake "dashboard-ui/dist"            14m   9
mkfake "widget-store/.turbo"          80m   14

# Python
mkfake "data-pipeline/.venv"          230m  22
mkfake "data-pipeline/__pycache__"    30m   22
mkfake "ml-experiments/.venv"         580m  30

# Rust + JVM
mkfake "rust-tooling/target"          220m  8
mkfake "mobile-app/build"             180m  14

echo "Fixture tree ready: $ROOT"
