#!/usr/bin/env bash
# P0 read-only inventory. Does not mutate ingest or production DNS/IAM.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=traffic-runtime-env.sh
source "${ROOT}/scripts/traffic-runtime-env.sh"

export PATH="${HOME}/google-cloud-sdk/bin:${PATH}"

STAMP="$(date -u +%Y-%m-%dT%H:%M:%SZ)"
OUT_DIR="${ROOT}/docs/traffic-runtime/evidence"
mkdir -p "${OUT_DIR}"
OUT="${OUT_DIR}/P0_INVENTORY.txt"

{
  echo "utc=${STAMP}"
  echo "actor=$(whoami)"
  echo "host=$(hostname)"
  echo "git_head=$(git -C "${ROOT}" rev-parse HEAD)"
  echo "git_branch=$(git -C "${ROOT}" rev-parse --abbrev-ref HEAD)"
  echo
  echo "== toolchain =="
  command -v gcloud || echo "gcloud: missing"
  command -v go
  go version
  gcloud version || true
  echo
  echo "== identity =="
  gcloud auth list --filter=status:ACTIVE --format='value(account)' 2>&1 || true
  echo "GOOGLE_APPLICATION_CREDENTIALS=${GOOGLE_APPLICATION_CREDENTIALS:-unset}"
  echo "project_config=$(gcloud config get-value project 2>/dev/null || true)"
  echo
  echo "== project =="
  if gcloud projects describe "${PROJECT_ID}" --format='value(projectNumber)' >/tmp/ta-project-number 2>/tmp/ta-project-describe.err; then
    echo "project_id=${PROJECT_ID}"
    echo "project_number=$(cat /tmp/ta-project-number)"
    if [[ -n "${EXPECTED_PROJECT_NUMBER}" && "$(cat /tmp/ta-project-number)" != "${EXPECTED_PROJECT_NUMBER}" ]]; then
      echo "WARNING: project number mismatch expected ${EXPECTED_PROJECT_NUMBER}"
    fi
  else
    echo "projects.describe FAILED"
    cat /tmp/ta-project-describe.err
  fi
  echo
  echo "== ingest service (selected fields only; env not dumped) =="
  gcloud run services describe ta-events-ingest \
    --project "${PROJECT_ID}" --region "${REGION}" \
    --format='yaml(metadata.name,status.url,status.latestReadyRevisionName,status.traffic,spec.template.spec.serviceAccountName,spec.template.spec.containerConcurrency,spec.template.spec.timeoutSeconds,spec.template.spec.containers[0].image,spec.template.spec.containers[0].resources)' \
    2>&1 || true
  echo
  echo "== ingest revision annotations (min/max/cpu billing) =="
  gcloud run services describe ta-events-ingest \
    --project "${PROJECT_ID}" --region "${REGION}" \
    --format='yaml(spec.template.metadata.annotations)' \
    2>&1 || true
  echo
  echo "== ingest health (read-only GET) =="
  if [[ -n "${INGEST_HEALTH_URL:-}" ]]; then
    curl --silent --show-error --max-time 10 \
      -o /tmp/ta-ingest-health.body -w 'http_code=%{http_code} err=%{errormsg}\n' \
      "${INGEST_HEALTH_URL}" || true
  else
    echo "INGEST_HEALTH_URL unset; skip"
  fi
  echo
  echo "== enabled APIs (filtered) =="
  gcloud services list --enabled --project "${PROJECT_ID}" --format='value(config.name)' 2>&1 \
    | grep -E 'run|cloudbuild|artifactregistry|pubsub|storage|secretmanager|sql|iam' || true
  echo
  echo "== artifact registry =="
  gcloud artifacts repositories list --project "${PROJECT_ID}" --location="${REGION}" 2>&1 || true
  echo
  echo "== WIF provider (no secrets) =="
  gcloud iam workload-identity-pools providers describe github-provider-new \
    --workload-identity-pool=github-pool --location=global --project "${PROJECT_ID}" \
    --format='yaml(name,attributeMapping,attributeCondition)' 2>&1 || true
  echo
  echo "== github-actions-sa IAM policy =="
  gcloud iam service-accounts get-iam-policy \
    "github-actions-sa@${PROJECT_ID}.iam.gserviceaccount.com" --project "${PROJECT_ID}" \
    2>&1 || true
  echo
  echo "== prototype service if present =="
  gcloud run services describe "${SERVICE}" \
    --project "${PROJECT_ID}" --region "${REGION}" \
    --format='yaml(metadata.name,status.url,status.latestReadyRevisionName,status.traffic,spec.template.spec.serviceAccountName)' \
    2>&1 || true
} > "${OUT}" 2>&1

echo "wrote ${OUT}"
