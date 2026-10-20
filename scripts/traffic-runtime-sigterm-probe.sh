#!/usr/bin/env bash
# T7 live SIGTERM → bounded drain on ta-traffic-runtime-proto-probe.
# Deploy revision A at 100%, enqueue, deploy B at 100%, delete A.
# Does not change serving 100% traffic.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=traffic-runtime-env.sh
source "${ROOT}/scripts/traffic-runtime-env.sh"
SERVICE="${PROBE_SERVICE}"
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

EVIDENCE="${ROOT}/docs/traffic-runtime/evidence/T7_SIGTERM.txt"
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

DIR_ENV="PROJECT_ID=${PROJECT_ID},PUBSUB_TOPIC=ta-traffic-runtime-proto-events,PROTOTYPE_MODE=true,PROTOTYPE_REDIRECT_URL=https://example.com/,PINNED_DIR=/pinned,ARTIFACT_SOURCE=dir"

if ! deploy_probe_revision /ready "${DIR_ENV}"; then
  log "t7_deploy=FAIL"
  log "result=FAIL"
  scale_probe_idle || true
  exit 0
fi
T7_REV="${PROBE_REV}"
T7_URL="${PROBE_URL}"
log "t7_revision=${T7_REV}"

if ! prepare_auth "${T7_URL}"; then
  log "t7_auth=FAIL"
  log "result=FAIL"
  scale_probe_idle || true
  exit 0
fi
for _ in 1 2 3; do
  curl_auth -o /dev/null -w '%{http_code}\n' "${T7_URL}/?ta_source=t7-drain" || true
done
log "t7_enqueued=3"

# Successor revision takes 100% so A is neither latest nor serving and can be deleted.
if ! deploy_probe_revision /ready "${DIR_ENV}"; then
  log "t7_successor=FAIL"
  log "result=FAIL"
  scale_probe_idle || true
  exit 0
fi
log "t7_successor=${PROBE_REV}"

DELETED=no
if gcloud run revisions delete "${T7_REV}" \
  --project "${PROJECT_ID}" --region "${REGION}" --quiet; then
  DELETED=yes
fi
log "t7_revision_deleted=${DELETED}"

read_shutdown_logs() {
  local filter
  filter="resource.type=\"cloud_run_revision\" AND resource.labels.revision_name=\"${T7_REV}\" AND (jsonPayload.msg=\"shutdown signal received\" OR jsonPayload.msg=\"publisher drained\" OR jsonPayload.msg=\"publisher drain timed out\" OR jsonPayload.msg=\"stopped\" OR jsonPayload.message=\"shutdown signal received\" OR jsonPayload.message=\"publisher drained\" OR jsonPayload.message=\"stopped\")"
  gcloud logging read "${filter}" \
    --project "${PROJECT_ID}" --freshness=20m --limit=20 \
    --format='value(jsonPayload.msg)' 2>/tmp/ta-t7-log.err || true
}

LOGS=""
for _ in 1 2 3 4 5 6 7 8; do
  sleep 6
  LOGS="$(read_shutdown_logs)"
  if echo "${LOGS}" | grep -q "shutdown signal received" \
    && echo "${LOGS}" | grep -Eq "publisher drained|publisher drain timed out|stopped"; then
    break
  fi
done
if [[ -z "${LOGS}" && -s /tmp/ta-t7-log.err ]]; then
  log "t7_log_read_error=$(tr '\n' ' ' </tmp/ta-t7-log.err | head -c 200)"
fi
log "t7_shutdown_log=$(echo "${LOGS}" | tr '\n' '|' | head -c 400)"

RESULT=FAIL
if [[ "${DELETED}" != yes ]]; then
  RESULT=BLOCKED
  log "t7_block=revision_delete_denied"
elif echo "${LOGS}" | grep -q "shutdown signal received"; then
  if echo "${LOGS}" | grep -Eq "publisher drained|publisher drain timed out|stopped"; then
    RESULT=PASS
  fi
fi
log "result=${RESULT}"

if ! assert_serving_unmoved; then
  log "serving_after=$(serving_percent_revision)"
  log "serving_shift=FAIL"
  echo "serving revision shifted" >&2
  scale_probe_idle || true
  exit 1
fi
log "serving_after=${SERVING_REV_BASELINE}"
log "serving_shift=no"
scale_probe_idle || log "probe_idle=WARN"
echo "T7 SIGTERM probe written to ${EVIDENCE}"
