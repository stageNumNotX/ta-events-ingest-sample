#!/usr/bin/env bash
# Create isolated prototype Pub/Sub topic + pull subscription.
# Never touches production ta-events / ta-events-to-bq / BQ IAM.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=traffic-runtime-env.sh
source "${ROOT}/scripts/traffic-runtime-env.sh"
export PATH="${HOME}/google-cloud-sdk/bin:${PATH}"

TOPIC="${PUBSUB_TOPIC:-ta-traffic-runtime-proto-events}"
SUB="${PUBSUB_VERIFY_SUB:-ta-traffic-runtime-proto-events-verify}"
SCHEMA_NAME="${PUBSUB_SCHEMA:-ta-traffic-runtime-proto-schema}"
SCHEMA_FILE="${ROOT}/deploy/event.avsc"
EVIDENCE="${ROOT}/docs/traffic-runtime/evidence/P4_PUBSUB.txt"
GHA_SA="github-actions-sa@${PROJECT_ID}.iam.gserviceaccount.com"

mkdir -p "$(dirname "${EVIDENCE}")"
: > "${EVIDENCE}"
{
  echo "utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "project=${PROJECT_ID}"
  echo "topic=${TOPIC}"
  echo "subscription=${SUB}"
  echo "schema=${SCHEMA_NAME}"
} >> "${EVIDENCE}"

log() { echo "$*" | tee -a "${EVIDENCE}"; }

if [[ "${TOPIC}" == "ta-events" || "${SUB}" == "ta-events-to-bq" ]]; then
  echo "refusing to operate on production ingest pubsub" >&2
  exit 1
fi

PROJECT_NUMBER="$(gcloud projects describe "${PROJECT_ID}" --format='value(projectNumber)')"
COMPUTE_SA="${PROJECT_NUMBER}-compute@developer.gserviceaccount.com"
RUNTIME_MEMBER="serviceAccount:${COMPUTE_SA}"
if gcloud iam service-accounts describe "${RUNTIME_SA}" --project "${PROJECT_ID}" >/dev/null 2>/tmp/ta-ps-sa.err; then
  RUNTIME_MEMBER="serviceAccount:${RUNTIME_SA}"
fi
log "runtime_member=${RUNTIME_MEMBER}"

schema_ok=0
if gcloud pubsub schemas describe "${SCHEMA_NAME}" --project "${PROJECT_ID}" >/dev/null 2>/tmp/ta-ps-schema.err; then
  log "schema=exists"
  schema_ok=1
else
  if gcloud pubsub schemas create "${SCHEMA_NAME}" --project "${PROJECT_ID}" \
      --type=AVRO --definition-file="${SCHEMA_FILE}" >/dev/null 2>/tmp/ta-ps-schema-create.err; then
    log "schema=created"
    schema_ok=1
  else
    if grep -qi 'PERMISSION_DENIED\|Permission denied' /tmp/ta-ps-schema-create.err; then
      log "schema=create_denied"
    else
      log "schema=create_failed"
      cat /tmp/ta-ps-schema-create.err >> "${EVIDENCE}" || true
    fi
  fi
fi

topic_ok=0
if gcloud pubsub topics describe "${TOPIC}" --project "${PROJECT_ID}" >/dev/null 2>/tmp/ta-ps-topic.err; then
  log "topic=exists"
  topic_ok=1
else
  create_args=(pubsub topics create "${TOPIC}" --project "${PROJECT_ID}")
  if [[ "${schema_ok}" -eq 1 ]]; then
    create_args+=(--schema="${SCHEMA_NAME}" --message-encoding=JSON)
  fi
  if gcloud "${create_args[@]}" >/dev/null 2>/tmp/ta-ps-topic-create.err; then
    log "topic=created schema_attached=${schema_ok}"
    topic_ok=1
  else
    if grep -qi 'PERMISSION_DENIED\|Permission denied' /tmp/ta-ps-topic-create.err; then
      log "topic=create_denied"
    else
      log "topic=create_failed"
      cat /tmp/ta-ps-topic-create.err >> "${EVIDENCE}" || true
    fi
  fi
fi

if [[ "${topic_ok}" -eq 1 ]]; then
  gcloud pubsub topics add-iam-policy-binding "${TOPIC}" --project "${PROJECT_ID}" \
    --member="${RUNTIME_MEMBER}" --role='roles/pubsub.publisher' --quiet >/dev/null 2>/tmp/ta-ps-pub.err \
    && log "publisher_binding=ok" || log "publisher_binding=denied"
  gcloud pubsub topics add-iam-policy-binding "${TOPIC}" --project "${PROJECT_ID}" \
    --member="serviceAccount:${GHA_SA}" --role='roles/pubsub.publisher' --quiet >/dev/null 2>/tmp/ta-ps-gha-pub.err \
    && log "gha_publisher_binding=ok" || log "gha_publisher_binding=denied"

  if gcloud pubsub subscriptions describe "${SUB}" --project "${PROJECT_ID}" >/dev/null 2>/tmp/ta-ps-sub.err; then
    log "subscription=exists"
  else
    if gcloud pubsub subscriptions create "${SUB}" --project "${PROJECT_ID}" \
        --topic="${TOPIC}" --ack-deadline=30 \
        --message-retention-duration=1d >/dev/null 2>/tmp/ta-ps-sub-create.err; then
      log "subscription=created"
    else
      if grep -qi 'PERMISSION_DENIED\|Permission denied' /tmp/ta-ps-sub-create.err; then
        log "subscription=create_denied"
      else
        log "subscription=create_failed"
        cat /tmp/ta-ps-sub-create.err >> "${EVIDENCE}" || true
      fi
    fi
  fi
  gcloud pubsub subscriptions add-iam-policy-binding "${SUB}" --project "${PROJECT_ID}" \
    --member="serviceAccount:${GHA_SA}" --role='roles/pubsub.subscriber' --quiet >/dev/null 2>/tmp/ta-ps-sub-iam.err \
    && log "gha_subscriber_binding=ok" || log "gha_subscriber_binding=denied"
  FILTER="$(gcloud pubsub subscriptions describe "${SUB}" --project "${PROJECT_ID}" --format='value(filter)' 2>/tmp/ta-ps-filter.err || true)"
  if [[ -z "${FILTER}" ]]; then
    log "subscription_filter=none"
  else
    log "subscription_filter=set"
  fi
else
  log "subscription=skipped_no_topic"
fi

echo "wrote ${EVIDENCE}"
# IAM denial is recorded, not a deploy blocker.
exit 0
