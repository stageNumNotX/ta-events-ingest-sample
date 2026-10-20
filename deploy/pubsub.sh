#!/usr/bin/env bash
# Создаёт Pub/Sub topic со схемой, DLQ и BigQuery subscription для ta-events-ingest
# (разделы 10 спеки). Идемпотентно. Требует прав owner/pubsub.admin.
#
# ВАЖНО: таблица events_raw.events должна существовать ДО запуска (см. deploy/bigquery.sql),
# т.к. BigQuery subscription привязывается к ней.
#
# Usage:
#   PROJECT_ID=your-gcp-project ./deploy/pubsub.sh
set -euo pipefail

: "${PROJECT_ID:?PROJECT_ID is required}"
TOPIC="${PUBSUB_TOPIC:-ta-events}"
DLQ_TOPIC="${DLQ_TOPIC:-ta-events-dlq}"
SUBSCRIPTION="${SUBSCRIPTION:-ta-events-to-bq}"
DLQ_SUBSCRIPTION="${DLQ_SUBSCRIPTION:-ta-events-dlq-sub}"
BQ_DATASET="${BQ_DATASET:-events_raw}"
BQ_TABLE="${BQ_TABLE:-events}"
SCHEMA_NAME="${SCHEMA_NAME:-ta-events-schema}"
MAX_DELIVERY_ATTEMPTS="${MAX_DELIVERY_ATTEMPTS:-5}"
SCHEMA_FILE="$(cd "$(dirname "$0")" && pwd)/event.avsc"

echo ">> project=${PROJECT_ID} topic=${TOPIC} subscription=${SUBSCRIPTION}"
gcloud config set project "${PROJECT_ID}" >/dev/null

# 1) Avro-схема topic'а.
if gcloud pubsub schemas describe "${SCHEMA_NAME}" >/dev/null 2>&1; then
  echo ">> schema ${SCHEMA_NAME} exists"
else
  echo ">> creating schema ${SCHEMA_NAME}"
  gcloud pubsub schemas create "${SCHEMA_NAME}" --type=AVRO --definition-file="${SCHEMA_FILE}"
fi

# 2) Topic со схемой и JSON-кодированием сообщений.
if gcloud pubsub topics describe "${TOPIC}" >/dev/null 2>&1; then
  echo ">> topic ${TOPIC} exists"
else
  echo ">> creating topic ${TOPIC}"
  gcloud pubsub topics create "${TOPIC}" \
    --schema="${SCHEMA_NAME}" \
    --message-encoding=JSON
fi

# 3) DLQ topic + pull-подписка для разбора мёртвых сообщений.
gcloud pubsub topics describe "${DLQ_TOPIC}" >/dev/null 2>&1 \
  || gcloud pubsub topics create "${DLQ_TOPIC}"
gcloud pubsub subscriptions describe "${DLQ_SUBSCRIPTION}" >/dev/null 2>&1 \
  || gcloud pubsub subscriptions create "${DLQ_SUBSCRIPTION}" --topic="${DLQ_TOPIC}"

# 4) Pub/Sub service agent + права: писать в BigQuery и публиковать в DLQ.
PROJECT_NUMBER="$(gcloud projects describe "${PROJECT_ID}" --format='value(projectNumber)')"
gcloud beta services identity create --service=pubsub.googleapis.com --project="${PROJECT_ID}" >/dev/null 2>&1 || true
PUBSUB_SA="service-${PROJECT_NUMBER}@gcp-sa-pubsub.iam.gserviceaccount.com"
echo ">> pubsub service agent: ${PUBSUB_SA}"

gcloud projects add-iam-policy-binding "${PROJECT_ID}" \
  --member="serviceAccount:${PUBSUB_SA}" \
  --role="roles/bigquery.dataEditor" \
  --condition=None >/dev/null
gcloud pubsub topics add-iam-policy-binding "${DLQ_TOPIC}" \
  --member="serviceAccount:${PUBSUB_SA}" \
  --role="roles/pubsub.publisher" >/dev/null

# 5) BigQuery subscription (таблица уже создана через deploy/bigquery.sql).
if gcloud pubsub subscriptions describe "${SUBSCRIPTION}" >/dev/null 2>&1; then
  echo ">> subscription ${SUBSCRIPTION} exists"
else
  echo ">> creating BigQuery subscription ${SUBSCRIPTION}"
  gcloud pubsub subscriptions create "${SUBSCRIPTION}" \
    --topic="${TOPIC}" \
    --bigquery-table="${PROJECT_ID}:${BQ_DATASET}.${BQ_TABLE}" \
    --use-topic-schema \
    --write-metadata \
    --dead-letter-topic="${DLQ_TOPIC}" \
    --max-delivery-attempts="${MAX_DELIVERY_ATTEMPTS}"
fi

# 6) Право DLQ: service agent должен читать исходную подписку, чтобы пересылать в DLQ.
gcloud pubsub subscriptions add-iam-policy-binding "${SUBSCRIPTION}" \
  --member="serviceAccount:${PUBSUB_SA}" \
  --role="roles/pubsub.subscriber" >/dev/null

echo ">> done"
