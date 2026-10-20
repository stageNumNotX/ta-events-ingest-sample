#!/usr/bin/env bash
# Read serving GET /contract pointer meta. Does not Activate and cannot
# prove A→B→A. Exit 2 = NOT_RUN (formal T4 stays PARTIAL).
set -euo pipefail
ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=traffic-runtime-env.sh
source "${ROOT}/scripts/traffic-runtime-env.sh"
# shellcheck source=traffic-runtime-invoke.sh
source "${ROOT}/scripts/traffic-runtime-invoke.sh"

URL="${1:-${SERVICE_URL:-}}"
if [[ -z "${URL}" ]]; then
  URL="$(gcloud run services describe "${SERVING_SERVICE}" \
    --project "${PROJECT_ID}" --region "${REGION}" \
    --format='value(status.url)')"
fi
if [[ -z "${URL}" ]]; then
  echo "service URL required" >&2
  exit 1
fi

prepare_auth "${URL}"
code="$(curl_auth -o /tmp/ta-t4-contract.json -w '%{http_code}' "${URL}/contract" || true)"
if [[ "${code}" != "200" ]]; then
  echo "GET /contract ${code}" >&2
  exit 1
fi
python3 - <<'PY'
import json
with open("/tmp/ta-t4-contract.json", encoding="utf-8") as f:
    data = json.load(f)
print(
    "activation=" + str(data.get("activation")),
    "pointerVersion=" + str(data.get("pointerVersion")),
    "pointerGeneration=" + str(data.get("pointerGeneration")),
    "artifactSetId=" + str(data.get("artifactSetId")),
    sep="\n",
)
print("t4_formal=IN_PROGRESS")
print("t4_a_to_b_to_a=NOT_RUN")
print("note=producer Activate B then A (increasing versions) required; env pin is not T4")
PY
exit 2
