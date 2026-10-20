#!/usr/bin/env bash
# T3/T5 cloud evidence on ta-traffic-runtime-proto-probe.
# Sequential 100% deploys on the probe service only. No --tag / --no-traffic.
# Serving ta-traffic-runtime-proto is described before/after and must not move.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=traffic-runtime-env.sh
source "${ROOT}/scripts/traffic-runtime-env.sh"
SERVICE="${PROBE_SERVICE}"
# shellcheck source=traffic-runtime-pins.env
source "${ROOT}/scripts/traffic-runtime-pins.env"
# shellcheck source=traffic-runtime-probe-deploy.sh
source "${ROOT}/scripts/traffic-runtime-probe-deploy.sh"
# shellcheck source=traffic-runtime-invoke.sh
source "${ROOT}/scripts/traffic-runtime-invoke.sh"
export PATH="${HOME}/google-cloud-sdk/bin:${PATH}"

require_probe_service

IMAGE_BY_DIGEST="${IMAGE_BY_DIGEST:-}"
if [[ -z "${IMAGE_BY_DIGEST}" && -f "${ROOT}/docs/traffic-runtime/evidence/P1_DEPLOY.txt" ]]; then
  IMAGE_BY_DIGEST="$(awk -F= '/^image=/{print $2; exit}' "${ROOT}/docs/traffic-runtime/evidence/P1_DEPLOY.txt")"
fi
if [[ "${IMAGE_BY_DIGEST}" != *@sha256:* ]]; then
  echo "IMAGE_BY_DIGEST digest URI required" >&2
  exit 1
fi
export IMAGE_BY_DIGEST

EVIDENCE="${ROOT}/docs/traffic-runtime/evidence/T3_T5_GCS.txt"
: >"${EVIDENCE}"
log() { echo "$1" | tee -a "${EVIDENCE}"; }

CLEANUP_AUTH=""
trap 'rm -f ${CLEANUP_AUTH}' EXIT

record_serving_baseline
log "utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
log "sha=$(git -C "${ROOT}" rev-parse HEAD)"
log "image=${IMAGE_BY_DIGEST}"
log "serving_service=${SERVING_SERVICE}"
log "serving_before=${SERVING_REV_BASELINE}"
log "probe_service=${PROBE_SERVICE}"
log "probe_mode=separate-service-100"

IAM_RESULT=denied
if gcloud storage buckets add-iam-policy-binding "gs://${BUCKET}" \
  --member="serviceAccount:${RUNTIME_SA}" \
  --role="roles/storage.objectViewer" \
  --project "${PROJECT_ID}" \
  --quiet >/tmp/ta-iam-gcs.out 2>/tmp/ta-iam-gcs.err; then
  IAM_RESULT=ok
fi
log "runtime_sa_objectViewer=${IAM_RESULT}"
if [[ "${IAM_RESULT}" == denied ]]; then
  log "iam_error=$(tr '\n' ' ' </tmp/ta-iam-gcs.err | head -c 300)"
fi

BASE_ENV="PROJECT_ID=${PROJECT_ID},PUBSUB_TOPIC=ta-traffic-runtime-proto-events,PROTOTYPE_MODE=false,TRAFFIC_POINTER_POLL=0"

http_probe() {
  local url="$1" path="$2"
  : >/tmp/ta-probe.body
  : >/tmp/ta-probe.hdr
  if ! prepare_auth "${url}"; then
    echo "000"
    return 0
  fi
  curl_auth -D /tmp/ta-probe.hdr -o /tmp/ta-probe.body -w '%{http_code}' "${url}${path}" || echo "000"
}

probe_location() {
  awk 'BEGIN{IGNORECASE=1} /^Location:/{sub(/\r$/,""); sub(/^Location:[[:space:]]*/,""); print; exit}' /tmp/ta-probe.hdr
}

probe_activation() {
  python3 -c 'import json
try:
    print(json.load(open("/tmp/ta-probe.body")).get("activation",""))
except Exception:
    print("")'
}

assert_code() {
  local got="$1" want="$2" name="$3"
  if [[ "${got}" != "${want}" ]]; then
    log "${name}=FAIL got=${got} want=${want} body=$(tr '\n' ' ' </tmp/ta-probe.body | head -c 120)"
    return 1
  fi
  log "${name}=${got}"
  return 0
}

revision_overlay_log() {
  local rev="$1"
  local filter out
  filter="resource.type=\"cloud_run_revision\" AND resource.labels.revision_name=\"${rev}\" AND (jsonPayload.msg:\"keeping LKG\" OR jsonPayload.message:\"keeping LKG\" OR jsonPayload.kept_lkg=true OR textPayload:\"keeping LKG\")"
  out="$(gcloud logging read "${filter}" \
    --project "${PROJECT_ID}" --freshness=20m --limit=8 \
    --format='value(jsonPayload.msg)' 2>/tmp/ta-rev-log.err || true)"
  if [[ -n "${out}" ]]; then
    printf '%s' "${out}"
    return 0
  fi
  return 1
}

wait_overlay_log() {
  local rev="$1" name="$2"
  local i msgs=""
  for i in 1 2 3; do
    sleep 5
    if msgs="$(revision_overlay_log "${rev}")"; then
      log "${name}_overlay_log=yes"
      log "${name}_overlay_msg=$(echo "${msgs}" | tr '\n' '|' | head -c 240)"
      return 0
    fi
  done
  log "${name}_overlay_log=no"
  return 1
}

expect_lkg_302() {
  local url="$1" name="$2"
  local code loc act r1=0 r2=0 r3=0
  code="$(http_probe "${url}" /ready)"
  assert_code "${code}" "200" "${name}_ready" || r1=1
  code="$(http_probe "${url}" /contract)"
  act="$(probe_activation)"
  log "${name}_activation=${act}"
  if [[ "${act}" != "revision-pinned" ]]; then
    r2=1
  fi
  code="$(http_probe "${url}" "/?ta_source=proto-smoke")"
  loc="$(probe_location)"
  log "${name}_root=${code}"
  if [[ "${code}" != "302" || "${loc}" != *campaign=contract-smoke* ]]; then
    log "${name}_location=${loc}"
    r3=1
  fi
  if [[ "${r1}" -eq 0 && "${r2}" -eq 0 && "${r3}" -eq 0 ]]; then
    return 0
  fi
  return 1
}

T3=FAIL
T5_COLD=FAIL
T5_LKG_MISSING=FAIL
T5_LKG_CORRUPT=FAIL
T5_LKG_INCOMPAT=NOT_RUN

# --- T3: valid generation-pinned C1, no dir LKG ---
if deploy_probe_revision /ready "${BASE_ENV},ARTIFACT_SOURCE=gcs"; then
  log "t3_revision=${PROBE_REV}"
  log "t3_url_host=$(python3 -c 'import os,urllib.parse; print(urllib.parse.urlparse(os.environ.get("PROBE_URL","")).netloc)')"
  code="$(http_probe "${PROBE_URL}" /health)"
  assert_code "${code}" "200" t3_health || true
  code="$(http_probe "${PROBE_URL}" /ready)"
  if assert_code "${code}" "200" t3_ready; then
    code="$(http_probe "${PROBE_URL}" /contract)"
    if [[ "${code}" == "200" ]]; then
      act="$(probe_activation)"
      log "t3_activation=${act}"
      if [[ "${act}" == "gcs-generation" ]]; then
        code="$(http_probe "${PROBE_URL}" "/?ta_source=proto-smoke")"
        loc="$(probe_location)"
        log "t3_root=${code}"
        if [[ "${code}" == "302" && "${loc}" == *campaign=contract-smoke* ]]; then
          T3=PASS
          log "t3_location_marker=campaign=contract-smoke"
        else
          log "t3_location=${loc}"
        fi
      fi
    else
      log "t3_contract=${code}"
    fi
  fi
else
  log "t3_deploy=FAIL"
fi
log "t3_valid_gcs=${T3}"

# --- T5 cold: no dir, missing generation; process must listen without pretending ready ---
if deploy_probe_revision /health "${BASE_ENV},ARTIFACT_SOURCE=gcs,TRAFFIC_PIN_PROGRAM_GENERATION=1,TRAFFIC_PIN_WEIGHTS_GENERATION=1,TRAFFIC_PIN_RELEASE_GENERATION=1"; then
  log "t5_cold_revision=${PROBE_REV}"
  code="$(http_probe "${PROBE_URL}" /health)"
  assert_code "${code}" "200" t5_cold_health || true
  code="$(http_probe "${PROBE_URL}" /ready)"
  r1=0; assert_code "${code}" "503" t5_cold_ready || r1=1
  code="$(http_probe "${PROBE_URL}" /)"
  r2=0; assert_code "${code}" "503" t5_cold_root || r2=1
  if [[ "${r1}" -eq 0 && "${r2}" -eq 0 ]]; then
    T5_COLD=PASS
  fi
else
  log "t5_cold_deploy=FAIL"
fi
log "t5_cold=${T5_COLD}"

# --- T5 LKG + missing GCS generation ---
if deploy_probe_revision /ready "${BASE_ENV},PINNED_DIR=/pinned,ARTIFACT_SOURCE=gcs,TRAFFIC_PIN_PROGRAM_GENERATION=1,TRAFFIC_PIN_WEIGHTS_GENERATION=1,TRAFFIC_PIN_RELEASE_GENERATION=1"; then
  log "t5_lkg_missing_revision=${PROBE_REV}"
  http_ok=1
  if expect_lkg_302 "${PROBE_URL}" t5_lkg_missing; then
    http_ok=0
  fi
  log_ok=1
  if wait_overlay_log "${PROBE_REV}" t5_lkg_missing; then
    log_ok=0
  fi
  if [[ "${http_ok}" -eq 0 && "${log_ok}" -eq 0 ]]; then
    T5_LKG_MISSING=PASS
  elif [[ "${http_ok}" -eq 0 ]]; then
    T5_LKG_MISSING=PARTIAL
  fi
else
  log "t5_lkg_missing_deploy=FAIL"
fi
log "t5_lkg_missing=${T5_LKG_MISSING}"

# --- T5 LKG + corrupt sha (real C1 object path, wrong digest) ---
BAD_SHA="aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
CORRUPT_ENV="${BASE_ENV},PINNED_DIR=/pinned,ARTIFACT_SOURCE=gcs,TRAFFIC_PIN_PROGRAM_SHA256=${BAD_SHA},TRAFFIC_PIN_PROGRAM_OBJECT=${PROGRAM_OBJECT},TRAFFIC_PIN_PROGRAM_GENERATION=${PROGRAM_GENERATION},TRAFFIC_PIN_PROGRAM_SIZE=${PROGRAM_SIZE}"
if deploy_probe_revision /ready "${CORRUPT_ENV}"; then
  log "t5_lkg_corrupt_revision=${PROBE_REV}"
  http_ok=1
  if expect_lkg_302 "${PROBE_URL}" t5_lkg_corrupt; then
    http_ok=0
  fi
  log_ok=1
  if wait_overlay_log "${PROBE_REV}" t5_lkg_corrupt; then
    log_ok=0
  fi
  if [[ "${http_ok}" -eq 0 && "${log_ok}" -eq 0 ]]; then
    T5_LKG_CORRUPT=PASS
  elif [[ "${http_ok}" -eq 0 ]]; then
    T5_LKG_CORRUPT=PARTIAL
  fi
else
  log "t5_lkg_corrupt_deploy=FAIL"
fi
log "t5_lkg_corrupt=${T5_LKG_CORRUPT}"

# --- T5 LKG + incompatible executor (optional upload) ---
INCOMPAT_DIR="$(mktemp -d)"
OVERLAY="${PINNED_OVERLAY:-${ROOT}/docs/traffic-runtime/evidence/pinned}"
if [[ ! -f "${OVERLAY}/program.json" ]]; then
  OVERLAY="${ROOT}/internal/runtimeloader/testdata/pinned"
fi
if [[ -f "${OVERLAY}/program.json" ]] && python3 "${ROOT}/scripts/traffic-runtime-make-incompat.py" "${OVERLAY}" "${INCOMPAT_DIR}"; then
  PREFIX="traffic-runtime/proto-t5-incompat"
  if gcloud storage cp "${INCOMPAT_DIR}/program.json" "gs://${BUCKET}/${PREFIX}/program.json" --quiet \
    && gcloud storage cp "${INCOMPAT_DIR}/weights.json" "gs://${BUCKET}/${PREFIX}/weights.json" --quiet \
    && gcloud storage cp "${INCOMPAT_DIR}/release.json" "gs://${BUCKET}/${PREFIX}/release.json" --quiet; then
    PROG_META="$(gcloud storage objects describe "gs://${BUCKET}/${PREFIX}/program.json" --format='value(generation,size)')"
    W_META="$(gcloud storage objects describe "gs://${BUCKET}/${PREFIX}/weights.json" --format='value(generation,size)')"
    R_META="$(gcloud storage objects describe "gs://${BUCKET}/${PREFIX}/release.json" --format='value(generation,size)')"
    PROG_SHA="$(sha256sum "${INCOMPAT_DIR}/program.json" | awk '{print $1}')"
    W_SHA="$(sha256sum "${INCOMPAT_DIR}/weights.json" | awk '{print $1}')"
    R_SHA="$(sha256sum "${INCOMPAT_DIR}/release.json" | awk '{print $1}')"
    PROG_GEN="$(awk '{print $1}' <<<"${PROG_META}")"
    PROG_SZ="$(awk '{print $2}' <<<"${PROG_META}")"
    W_GEN="$(awk '{print $1}' <<<"${W_META}")"
    W_SZ="$(awk '{print $2}' <<<"${W_META}")"
    R_GEN="$(awk '{print $1}' <<<"${R_META}")"
    R_SZ="$(awk '{print $2}' <<<"${R_META}")"
    INC_ENV="${BASE_ENV},PINNED_DIR=/pinned,ARTIFACT_SOURCE=gcs,TRAFFIC_PIN_BUCKET=${BUCKET}"
    INC_ENV="${INC_ENV},TRAFFIC_PIN_PROGRAM_OBJECT=${PREFIX}/program.json,TRAFFIC_PIN_PROGRAM_SHA256=${PROG_SHA},TRAFFIC_PIN_PROGRAM_GENERATION=${PROG_GEN},TRAFFIC_PIN_PROGRAM_SIZE=${PROG_SZ}"
    INC_ENV="${INC_ENV},TRAFFIC_PIN_WEIGHTS_OBJECT=${PREFIX}/weights.json,TRAFFIC_PIN_WEIGHTS_SHA256=${W_SHA},TRAFFIC_PIN_WEIGHTS_GENERATION=${W_GEN},TRAFFIC_PIN_WEIGHTS_SIZE=${W_SZ}"
    INC_ENV="${INC_ENV},TRAFFIC_PIN_RELEASE_OBJECT=${PREFIX}/release.json,TRAFFIC_PIN_RELEASE_SHA256=${R_SHA},TRAFFIC_PIN_RELEASE_GENERATION=${R_GEN},TRAFFIC_PIN_RELEASE_SIZE=${R_SZ}"
    if deploy_probe_revision /ready "${INC_ENV}"; then
      log "t5_lkg_incompat_revision=${PROBE_REV}"
      http_ok=1
      if expect_lkg_302 "${PROBE_URL}" t5_lkg_incompat; then
        http_ok=0
      fi
      log_ok=1
      if wait_overlay_log "${PROBE_REV}" t5_lkg_incompat; then
        log_ok=0
      fi
      if [[ "${http_ok}" -eq 0 && "${log_ok}" -eq 0 ]]; then
        T5_LKG_INCOMPAT=PASS
      elif [[ "${http_ok}" -eq 0 ]]; then
        T5_LKG_INCOMPAT=PARTIAL
      else
        T5_LKG_INCOMPAT=FAIL
      fi
    else
      T5_LKG_INCOMPAT=FAIL
      log "t5_lkg_incompat_deploy=FAIL"
    fi
  else
    T5_LKG_INCOMPAT=BLOCKED
    log "t5_incompat_upload=denied"
  fi
else
  T5_LKG_INCOMPAT=BLOCKED
  log "t5_incompat_bytes=unavailable"
fi
log "t5_lkg_incompat=${T5_LKG_INCOMPAT}"

if ! assert_serving_unmoved; then
  log "serving_after=$(serving_percent_revision)"
  log "serving_shift=FAIL"
  echo "serving revision shifted ${SERVING_REV_BASELINE} -> $(serving_percent_revision)" >&2
  exit 1
fi
log "serving_after=${SERVING_REV_BASELINE}"
log "serving_shift=no"
log "t3=${T3}"
log "t5_cold=${T5_COLD}"
log "t5_lkg_missing=${T5_LKG_MISSING}"
log "t5_lkg_corrupt=${T5_LKG_CORRUPT}"
log "t5_lkg_incompat=${T5_LKG_INCOMPAT}"
echo "T3/T5 GCS probe written to ${EVIDENCE}"
