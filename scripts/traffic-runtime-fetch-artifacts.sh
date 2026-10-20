#!/usr/bin/env bash
# Download generation-pinned Program/Weights/Release from GCS. No R2.
# Verifies size and sha256 of raw bytes. Does not print object contents.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=traffic-runtime-env.sh
source "${ROOT}/scripts/traffic-runtime-env.sh"
# shellcheck source=traffic-runtime-pins.env
source "${ROOT}/scripts/traffic-runtime-pins.env"
export PATH="${HOME}/google-cloud-sdk/bin:${PATH}"

OUT="${PINNED_OUT:-${ROOT}/docs/traffic-runtime/evidence/pinned}"
mkdir -p "${OUT}"
EVIDENCE="${ROOT}/docs/traffic-runtime/evidence/P2_PINNED.txt"

# Owner wrote gs://.../traffic-runtime/v1/{kind}/sha256/{sha}.json
# Also try job/artifactSet prefixes if the short path 404s.
path_candidates() {
  local kind="$1" sha="$2"
  local object="traffic-runtime/v1/${kind}/sha256/${sha}.json"
  printf '%s\n' \
    "${object}" \
    "jobs/${COMPILER_JOB}/artifact-sets/${ARTIFACT_SET}/${object}" \
    "jobs/${COMPILER_JOB}/artifactSets/${ARTIFACT_SET}/${object}" \
    "${COMPILER_JOB}/${ARTIFACT_SET}/${object}" \
    "artifact-sets/${ARTIFACT_SET}/${object}"
}

download_one() {
  local kind="$1" sha="$2" gen="$3" size="$4"
  local dest="${OUT}/${kind}.json"
  local err
  err="$(mktemp)"
  local found=""
  local uri=""
  while IFS= read -r rel; do
    uri="gs://${BUCKET}/${rel}#${gen}"
    if gcloud storage cp "${uri}" "${dest}" --quiet >/dev/null 2>"${err}"; then
      found="${uri}"
      break
    fi
    if gsutil cp "${uri}" "${dest}" >/dev/null 2>>"${err}"; then
      found="${uri}"
      break
    fi
  done < <(path_candidates "${kind}" "${sha}")
  if [[ -z "${found}" ]]; then
    echo "FAILED ${kind}: none of the candidate URIs worked" >&2
    cat "${err}" >&2 || true
    rm -f "${err}"
    return 1
  fi
  rm -f "${err}"
  local actual_size actual_sha
  actual_size="$(wc -c < "${dest}" | tr -d ' ')"
  actual_sha="$(sha256sum "${dest}" | awk '{print $1}')"
  if [[ "${actual_size}" != "${size}" ]]; then
    echo "FAILED ${kind} size got=${actual_size} want=${size}" >&2
    return 1
  fi
  if [[ "${actual_sha}" != "${sha}" ]]; then
    echo "FAILED ${kind} sha256 got=${actual_sha} want=${sha}" >&2
    return 1
  fi
  echo "OK ${kind} uri=${found} size=${actual_size} sha256=${actual_sha}"
  python3 - "${dest}" "${kind}" <<'PY'
import json, sys
path, kind = sys.argv[1], sys.argv[2]
with open(path, "rb") as f:
    raw = f.read()
data = json.loads(raw.decode("utf-8"))
if isinstance(data, dict):
    keys = ",".join(sorted(data.keys()))
    print(f"shape {kind} type=object keys={keys}")
elif isinstance(data, list):
    print(f"shape {kind} type=array len={len(data)}")
else:
    print(f"shape {kind} type={type(data).__name__}")
PY
}

{
  echo "utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "bucket=${BUCKET}"
  echo "compiler_job=${COMPILER_JOB}"
  echo "artifact_set=${ARTIFACT_SET}"
  echo "r2=not_used"
} > "${EVIDENCE}"

fail=0
download_one program "${PROGRAM_SHA256}" "${PROGRAM_GENERATION}" "${PROGRAM_SIZE}" | tee -a "${EVIDENCE}" || fail=1
download_one weights "${WEIGHTS_SHA256}" "${WEIGHTS_GENERATION}" "${WEIGHTS_SIZE}" | tee -a "${EVIDENCE}" || fail=1
download_one release "${RELEASE_SHA256}" "${RELEASE_GENERATION}" "${RELEASE_SIZE}" | tee -a "${EVIDENCE}" || fail=1

if [[ "${fail}" -ne 0 ]]; then
  echo "pinned download incomplete" >&2
  exit 1
fi
echo "wrote ${OUT} and ${EVIDENCE}"
