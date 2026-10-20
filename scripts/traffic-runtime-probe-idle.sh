#!/usr/bin/env bash
# Scale the T3/T5/T7 probe service to zero. Does not touch serving.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=traffic-runtime-env.sh
source "${ROOT}/scripts/traffic-runtime-env.sh"
SERVICE="${PROBE_SERVICE}"
# shellcheck source=traffic-runtime-probe-deploy.sh
source "${ROOT}/scripts/traffic-runtime-probe-deploy.sh"
export PATH="${HOME}/google-cloud-sdk/bin:${PATH}"

require_probe_service
scale_probe_idle
