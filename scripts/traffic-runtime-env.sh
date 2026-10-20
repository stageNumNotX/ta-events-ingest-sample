#!/usr/bin/env bash
# Shared constants for the isolated traffic-runtime prototype.
# Refuse to operate on the production ingest service.
set -euo pipefail
set +x

PROJECT_ID="${PROJECT_ID:?PROJECT_ID is required}"
REGION="${REGION:-europe-west1}"
# Serving path: one Cloud Run service, one 100% revision, no probe tags.
SERVING_SERVICE="ta-traffic-runtime-proto"
# T3/T5/T7 only. Same image and runtime SA; never steal serving traffic.
PROBE_SERVICE="ta-traffic-runtime-proto-probe"
SERVICE="${SERVICE:-${SERVING_SERVICE}}"
AR_REPO="${AR_REPO:-ta-traffic-runtime}"
RUNTIME_SA_NAME="${RUNTIME_SA_NAME:-ta-traffic-runtime-proto}"
RUNTIME_SA="${RUNTIME_SA_NAME}@${PROJECT_ID}.iam.gserviceaccount.com"
IMAGE_NAME="${REGION}-docker.pkg.dev/${PROJECT_ID}/${AR_REPO}/runtime"
EXPECTED_PROJECT_NUMBER="${EXPECTED_PROJECT_NUMBER:-}"

if [[ "${SERVICE}" == "ta-events-ingest" ]]; then
  echo "refusing to operate on production ingest service ta-events-ingest" >&2
  exit 1
fi
case "${SERVICE}" in
  "${SERVING_SERVICE}"|"${PROBE_SERVICE}") ;;
  *)
    echo "refusing unexpected service ${SERVICE}" >&2
    exit 1
    ;;
esac
