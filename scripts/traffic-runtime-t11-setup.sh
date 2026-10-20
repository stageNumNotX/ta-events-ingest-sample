#!/usr/bin/env bash
# Isolated T11 Pub/Sub → native BigQuery subscription templates.
# Owner-run only. This agent must not execute against live/production.
#
# Required env:
#   PROJECT_ID T11_TOPIC T11_SUBSCRIPTION T11_DATASET T11_TABLE
# Optional:
#   T11_DLQ_TOPIC T11_DLQ_SUBSCRIPTION T11_SCHEMA T11_LOCATION T11_TTL_DAYS
#   T11_MAX_DELIVERY_ATTEMPTS APPLY
#
# APPLY=1 actually mutates. Anything else is preflight/dry-run.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

# Do not source traffic-runtime-env.sh: T11 must not inherit serving defaults.
if [[ -z "${PROJECT_ID:-}" ]]; then
  echo "PROJECT_ID is required (no default project; T11 does not source traffic-runtime-env.sh)" >&2
  exit 1
fi

reject_production_targets() {
  local topic="${T11_TOPIC:-}"
  local sub="${T11_SUBSCRIPTION:-}"
  local dataset="${T11_DATASET:-}"
  local table="${T11_TABLE:-}"
  local dlq="${T11_DLQ_TOPIC:-}"
  if [[ -z "${topic}" || -z "${sub}" || -z "${dataset}" || -z "${table}" ]]; then
    echo "T11 requires PROJECT_ID T11_TOPIC T11_SUBSCRIPTION T11_DATASET T11_TABLE" >&2
    exit 1
  fi
  if [[ "${topic}" == "ta-events" || "${sub}" == "ta-events-to-bq" || "${dlq}" == "ta-events-dlq" ]]; then
    echo "refusing production Pub/Sub names (${topic}/${sub})" >&2
    exit 1
  fi
  if [[ "${dataset}" == "events_raw" || "${dataset}.${table}" == "events_raw.events" || "${table}" == "events" && "${dataset}" == "events_raw" ]]; then
    echo "refusing production BigQuery target ${dataset}.${table}" >&2
    exit 1
  fi
  if [[ "${SERVICE:-}" == "ta-events-ingest" ]]; then
    echo "refusing production ingest service" >&2
    exit 1
  fi
}

reject_production_targets

T11_DLQ_TOPIC="${T11_DLQ_TOPIC:-${T11_TOPIC}-dlq}"
T11_DLQ_SUBSCRIPTION="${T11_DLQ_SUBSCRIPTION:-${T11_SUBSCRIPTION}-dlq}"
T11_SCHEMA="${T11_SCHEMA:-${T11_TOPIC}-schema}"
T11_LOCATION="${T11_LOCATION:-EU}"
T11_TTL_DAYS="${T11_TTL_DAYS:-1}"
T11_MAX_DELIVERY_ATTEMPTS="${T11_MAX_DELIVERY_ATTEMPTS:-5}"
SCHEMA_FILE="${ROOT}/deploy/event.avsc"
SQL_FILE="${ROOT}/deploy/traffic-runtime-t11.bigquery.sql"
APPLY="${APPLY:-0}"

if ! [[ "${T11_TTL_DAYS}" =~ ^[0-9]+$ ]] || [[ "${T11_TTL_DAYS}" -lt 1 || "${T11_TTL_DAYS}" -gt 7 ]]; then
  echo "T11_TTL_DAYS must be 1..7 (proposed default 1)" >&2
  exit 1
fi

echo "t11_mode=$([[ "${APPLY}" == "1" ]] && echo apply || echo preflight)"
echo "project=${PROJECT_ID}"
echo "topic=${T11_TOPIC}"
echo "subscription=${T11_SUBSCRIPTION}"
echo "dataset=${T11_DATASET}"
echo "table=${T11_TABLE}"
echo "ttl_days=${T11_TTL_DAYS}"
echo "location=${T11_LOCATION}"
echo "note=create table before BigQuery subscription; grant pubsub SA table/dataset roles only (not project-wide dataEditor)"

if [[ "${APPLY}" != "1" ]]; then
  echo "preflight_ok=yes"
  echo "drift_check=run gcloud pubsub subscriptions describe / bq show and compare topic, table, schema, write-metadata, TTL, partition filter"
  echo "iam=roles/bigquery.dataEditor on dataset ${T11_DATASET} (or table) for service-<PROJECT_NUMBER>@gcp-sa-pubsub.iam.gserviceaccount.com"
  echo "iam_dlq=roles/pubsub.publisher on ${T11_DLQ_TOPIC} + roles/pubsub.subscriber on ${T11_SUBSCRIPTION} for the same service agent"
  exit 0
fi

export PATH="${HOME}/google-cloud-sdk/bin:${PATH}"

PROJECT_NUMBER="$(gcloud projects describe "${PROJECT_ID}" --format='value(projectNumber)')"
PUBSUB_SA="service-${PROJECT_NUMBER}@gcp-sa-pubsub.iam.gserviceaccount.com"
gcloud beta services identity create --service=pubsub.googleapis.com --project="${PROJECT_ID}" >/dev/null 2>&1 || true

SQL="$(sed \
  -e "s/{{PROJECT_ID}}/${PROJECT_ID}/g" \
  -e "s/{{DATASET}}/${T11_DATASET}/g" \
  -e "s/{{TABLE}}/${T11_TABLE}/g" \
  -e "s/{{LOCATION}}/${T11_LOCATION}/g" \
  -e "s/{{TTL_DAYS}}/${T11_TTL_DAYS}/g" \
  "${SQL_FILE}")"
bq query --use_legacy_sql=false --location="${T11_LOCATION}" --project_id="${PROJECT_ID}" "${SQL}"

if gcloud pubsub schemas describe "${T11_SCHEMA}" --project "${PROJECT_ID}" >/dev/null 2>&1; then
  echo "schema=exists"
else
  gcloud pubsub schemas create "${T11_SCHEMA}" --project "${PROJECT_ID}" --type=AVRO --definition-file="${SCHEMA_FILE}"
  echo "schema=created"
fi

if gcloud pubsub topics describe "${T11_TOPIC}" --project "${PROJECT_ID}" >/dev/null 2>&1; then
  echo "topic=exists"
else
  gcloud pubsub topics create "${T11_TOPIC}" --project "${PROJECT_ID}" --schema="${T11_SCHEMA}" --message-encoding=JSON
  echo "topic=created"
fi

gcloud pubsub topics describe "${T11_DLQ_TOPIC}" --project "${PROJECT_ID}" >/dev/null 2>&1 \
  || gcloud pubsub topics create "${T11_DLQ_TOPIC}" --project "${PROJECT_ID}"
gcloud pubsub subscriptions describe "${T11_DLQ_SUBSCRIPTION}" --project "${PROJECT_ID}" >/dev/null 2>&1 \
  || gcloud pubsub subscriptions create "${T11_DLQ_SUBSCRIPTION}" --project "${PROJECT_ID}" --topic="${T11_DLQ_TOPIC}"

# Least privilege: dataset, not project-wide BQ.
bq query --use_legacy_sql=false --project_id="${PROJECT_ID}" --location="${T11_LOCATION}" \
  "GRANT \`roles/bigquery.dataEditor\` ON SCHEMA \`${PROJECT_ID}.${T11_DATASET}\` TO 'serviceAccount:${PUBSUB_SA}'" >/dev/null \
  || echo "grant_dataset_editor=manual"

gcloud pubsub topics add-iam-policy-binding "${T11_DLQ_TOPIC}" --project "${PROJECT_ID}" \
  --member="serviceAccount:${PUBSUB_SA}" --role="roles/pubsub.publisher" >/dev/null

if gcloud pubsub subscriptions describe "${T11_SUBSCRIPTION}" --project "${PROJECT_ID}" >/dev/null 2>&1; then
  echo "subscription=exists — inspect for drift; not treated as correct"
  gcloud pubsub subscriptions describe "${T11_SUBSCRIPTION}" --project "${PROJECT_ID}" \
    --format='yaml(topic,bigqueryConfig,deadLetterPolicy,ackDeadlineSeconds)'
else
  gcloud pubsub subscriptions create "${T11_SUBSCRIPTION}" --project "${PROJECT_ID}" \
    --topic="${T11_TOPIC}" \
    --bigquery-table="${PROJECT_ID}:${T11_DATASET}.${T11_TABLE}" \
    --use-topic-schema \
    --write-metadata \
    --dead-letter-topic="${T11_DLQ_TOPIC}" \
    --max-delivery-attempts="${T11_MAX_DELIVERY_ATTEMPTS}"
  echo "subscription=created"
fi

gcloud pubsub subscriptions add-iam-policy-binding "${T11_SUBSCRIPTION}" --project "${PROJECT_ID}" \
  --member="serviceAccount:${PUBSUB_SA}" --role="roles/pubsub.subscriber" >/dev/null

echo "t11_apply=done"
echo "verify=scripts/traffic-runtime-t11-verify.sh"
