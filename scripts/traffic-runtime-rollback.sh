#!/usr/bin/env bash
# Rollback prototype serving traffic to a previously Ready revision. Code rollback only.
# Does not operate on the T3/T5/T7 probe service.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=traffic-runtime-env.sh
source "${ROOT}/scripts/traffic-runtime-env.sh"
if [[ "${SERVICE}" != "${SERVING_SERVICE}" ]]; then
  echo "rollback only applies to ${SERVING_SERVICE}, got ${SERVICE}" >&2
  exit 1
fi
export PATH="${HOME}/google-cloud-sdk/bin:${PATH}"

PREVIOUS_REVISION="${1:-${PREVIOUS_REVISION:-}}"
if [[ -z "${PREVIOUS_REVISION}" ]]; then
  echo "usage: $0 <previous-ready-revision>" >&2
  exit 1
fi

gcloud run services update-traffic "${SERVICE}" \
  --project "${PROJECT_ID}" --region "${REGION}" \
  --to-revisions="${PREVIOUS_REVISION}=100" \
  --quiet

echo "traffic set to ${PREVIOUS_REVISION}=100"
