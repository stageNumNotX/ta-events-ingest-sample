#!/usr/bin/env bash
# T9: pure resolver bench + cloud 100 RPS then 500 RPS for 60s each.
# Never follows redirects. Diagnostic, not a production-capacity claim.
# Both phases require status_other=0, errors=0, and achieved_rps >=
# T9_ACHIEVED_FLOOR_PCT (default 80) of offered. 500 cannot OK on leftovers.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=traffic-runtime-env.sh
source "${ROOT}/scripts/traffic-runtime-env.sh"
# shellcheck source=traffic-runtime-invoke.sh
source "${ROOT}/scripts/traffic-runtime-invoke.sh"
export PATH="${HOME}/google-cloud-sdk/bin:${PATH}"

EVIDENCE="${ROOT}/docs/traffic-runtime/evidence/T9_LOAD.txt"
RESOLVER_EV="${ROOT}/docs/traffic-runtime/evidence/T9_RESOLVER.txt"
: >"${EVIDENCE}"
log() { echo "$1" | tee -a "${EVIDENCE}"; }

DURATION="${T9_DURATION:-60s}"
URL="${SERVICE_URL:-}"

log "utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
log "sha=$(git -C "${ROOT}" rev-parse HEAD)"
log "project=${PROJECT_ID}"
log "service=${SERVING_SERVICE}"
log "region=${REGION}"
log "duration=${DURATION}"
log "followed_redirect=no"
log "note=diagnostic 100 then 500 RPS; not production capacity"

echo "=== resolver bench ===" | tee "${RESOLVER_EV}"
(cd "${ROOT}" && go test ./internal/traffic -bench=BenchmarkT9ResolveRedirect -benchtime=3s -count=1 -timeout 2m) | tee -a "${RESOLVER_EV}"

if [[ -z "${URL}" ]]; then
  URL="$(gcloud run services describe "${SERVING_SERVICE}" \
    --project "${PROJECT_ID}" --region "${REGION}" \
    --format='value(status.url)')"
fi
if [[ -z "${URL}" ]]; then
  log "t9=FAIL"
  log "t9_block=no_service_url"
  exit 1
fi
log "url=${URL}"

REV="$(gcloud run services describe "${SERVING_SERVICE}" \
  --project "${PROJECT_ID}" --region "${REGION}" \
  --format=json | python3 -c 'import json,sys
d=json.load(sys.stdin)
for t in d.get("status",{}).get("traffic") or []:
    if t.get("percent")==100 and t.get("revisionName"):
        print(t["revisionName"]); break')"
log "revision_before=${REV}"
log "image=$(gcloud run revisions describe "${REV}" --project "${PROJECT_ID}" --region "${REGION}" --format='value(spec.containers[0].image)' 2>/dev/null || true)"

SCALE="$(gcloud run services describe "${SERVING_SERVICE}" \
  --project "${PROJECT_ID}" --region "${REGION}" \
  --format='value(spec.template.metadata.annotations.autoscaling\.knative\.dev/minScale,spec.template.metadata.annotations.autoscaling\.knative\.dev/maxScale)' 2>/dev/null || true)"
log "min_max_scale=${SCALE}"

CLEANUP_AUTH=""
trap 'rm -f ${CLEANUP_AUTH}' EXIT
if ! prepare_auth "${URL}"; then
  log "t9=FAIL"
  log "t9_block=identity_token"
  exit 1
fi
HDR="$(mktemp)"
chmod 600 "${HDR}"
CLEANUP_AUTH="${CLEANUP_AUTH} ${HDR}"
# Recreate a header file for t9load from AUTH_ARGS (@file).
if [[ "${AUTH_ARGS[1]:-}" == @* ]]; then
  cp "${AUTH_ARGS[1]#@}" "${HDR}"
else
  log "t9=FAIL"
  log "t9_block=auth_header_file"
  exit 1
fi

TARGET="${URL}/?ta_source=t9-load"
START_UTC="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
log "load_start=${START_UTC}"

run_phase() {
  local rps="$1"
  local out
  out="$(mktemp)"
  echo "=== ${rps} rps ${DURATION} ===" | tee -a "${EVIDENCE}"
  if ! go run "${ROOT}/scripts/t9load/main.go" \
    -url "${TARGET}" -rps "${rps}" -duration "${DURATION}" -header-file "${HDR}" \
    | tee "${out}" | tee -a "${EVIDENCE}"; then
    echo "t9_phase_${rps}=FAIL" | tee -a "${EVIDENCE}"
    return 1
  fi
  local ok other err achieved floor_pct
  ok="$(awk -F= '/^status_302=/{print $2}' "${out}")"
  other="$(awk -F= '/^status_other=/{print $2}' "${out}")"
  err="$(awk -F= '/^errors=/{print $2}' "${out}")"
  achieved="$(awk -F= '/^achieved_rps=/{print $2}' "${out}")"
  ok="${ok:-0}"; other="${other:-0}"; err="${err:-0}"; achieved="${achieved:-0}"
  floor_pct="${T9_ACHIEVED_FLOOR_PCT:-80}"
  if [[ "${other}" -ne 0 || "${err}" -ne 0 ]]; then
    echo "t9_phase_${rps}=FAIL" | tee -a "${EVIDENCE}"
    echo "t9_block=errors_or_status_other" | tee -a "${EVIDENCE}"
    echo "t9_phase_${rps}_status_other=${other}" | tee -a "${EVIDENCE}"
    echo "t9_phase_${rps}_errors=${err}" | tee -a "${EVIDENCE}"
    rm -f "${out}"
    return 1
  fi
  if [[ "${ok}" -lt 1 ]]; then
    echo "t9_phase_${rps}=FAIL" | tee -a "${EVIDENCE}"
    echo "t9_block=no_302" | tee -a "${EVIDENCE}"
    rm -f "${out}"
    return 1
  fi
  if ! awk -v a="${achieved}" -v r="${rps}" -v p="${floor_pct}" 'BEGIN{exit !(a+0 >= r*p/100.0)}'; then
    echo "t9_phase_${rps}=FAIL" | tee -a "${EVIDENCE}"
    echo "t9_block=achieved_rps_below_${floor_pct}pct" | tee -a "${EVIDENCE}"
    echo "t9_phase_${rps}_achieved_rps=${achieved}" | tee -a "${EVIDENCE}"
    rm -f "${out}"
    return 1
  fi
  echo "t9_phase_${rps}=OK" | tee -a "${EVIDENCE}"
  rm -f "${out}"
  return 0
}

PHASE100=FAIL
PHASE500=FAIL
if run_phase 100; then PHASE100=OK; fi
if run_phase 500; then PHASE500=OK; fi
END_UTC="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
log "load_end=${END_UTC}"

REV_AFTER="$(gcloud run services describe "${SERVING_SERVICE}" \
  --project "${PROJECT_ID}" --region "${REGION}" \
  --format=json | python3 -c 'import json,sys
d=json.load(sys.stdin)
for t in d.get("status",{}).get("traffic") or []:
    if t.get("percent")==100 and t.get("revisionName"):
        print(t["revisionName"]); break')"
log "revision_after=${REV_AFTER}"
if [[ "${REV}" != "${REV_AFTER}" ]]; then
  log "revision_shifted=yes"
else
  log "revision_shifted=no"
fi

# Best-effort Cloud Run metrics. Denial is recorded, not hidden.
METRICS_ERR="$(mktemp)"
if gcloud logging read \
  "resource.type=\"cloud_run_revision\" AND resource.labels.service_name=\"${SERVING_SERVICE}\" AND timestamp>=\"${START_UTC}\"" \
  --project "${PROJECT_ID}" --limit=20 --freshness=15m \
  --format='value(jsonPayload.msg)' > /tmp/ta-t9-logs.txt 2>"${METRICS_ERR}"; then
  log "logging_read=ok"
  log "sample_msgs=$(tr '\n' '|' </tmp/ta-t9-logs.txt | head -c 400)"
else
  log "logging_read=denied_or_empty"
  log "logging_error=$(tr '\n' ' ' <"${METRICS_ERR}" | head -c 240)"
fi

if gcloud monitoring time-series list \
  --project "${PROJECT_ID}" \
  --filter="metric.type=\"run.googleapis.com/container/instance_count\" AND resource.labels.service_name=\"${SERVING_SERVICE}\"" \
  --interval-start-time="${START_UTC}" \
  --interval-end-time="${END_UTC}" \
  --format='value(points.0.value.int64Value)' > /tmp/ta-t9-inst.txt 2>"${METRICS_ERR}"; then
  log "instance_count_samples=$(tr '\n' ',' </tmp/ta-t9-inst.txt | head -c 200)"
else
  log "instance_count=unavailable"
  log "monitoring_error=$(tr '\n' ' ' <"${METRICS_ERR}" | head -c 240)"
fi

if gcloud monitoring time-series list \
  --project "${PROJECT_ID}" \
  --filter="metric.type=\"run.googleapis.com/container/cpu/utilizations\" AND resource.labels.service_name=\"${SERVING_SERVICE}\"" \
  --interval-start-time="${START_UTC}" \
  --interval-end-time="${END_UTC}" \
  --format='value(points.0.value.doubleValue)' > /tmp/ta-t9-cpu.txt 2>"${METRICS_ERR}"; then
  log "cpu_util_samples=$(tr '\n' ',' </tmp/ta-t9-cpu.txt | head -c 200)"
else
  log "cpu_util=unavailable"
fi

if gcloud monitoring time-series list \
  --project "${PROJECT_ID}" \
  --filter="metric.type=\"run.googleapis.com/container/memory/utilizations\" AND resource.labels.service_name=\"${SERVING_SERVICE}\"" \
  --interval-start-time="${START_UTC}" \
  --interval-end-time="${END_UTC}" \
  --format='value(points.0.value.doubleValue)' > /tmp/ta-t9-mem.txt 2>"${METRICS_ERR}"; then
  log "memory_util_samples=$(tr '\n' ',' </tmp/ta-t9-mem.txt | head -c 200)"
else
  log "memory_util=unavailable"
fi
rm -f "${METRICS_ERR}"

log "load_generator=go run ./scripts/t9load (GHA ubuntu-latest / this script host)"
log "achieved_floor_pct=${T9_ACHIEVED_FLOOR_PCT:-80}"
log "t9_phase_100=${PHASE100}"
log "t9_phase_500=${PHASE500}"
if [[ "${PHASE100}" == "OK" && "${PHASE500}" == "OK" ]]; then
  log "t9=PASS"
  log "t9_note=diagnostic; zero errors/other; achieved >= ${T9_ACHIEVED_FLOOR_PCT:-80}% offered; LIVE_ACCEPTED=false"
  exit 0
fi
if [[ "${PHASE100}" != "OK" ]]; then
  log "t9=FAIL"
  log "t9_block=phase_100"
  exit 1
fi
log "t9=FAIL"
log "t9_block=phase_500"
exit 1
