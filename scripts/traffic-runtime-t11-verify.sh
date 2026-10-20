#!/usr/bin/env bash
# Bounded T11 verification query against an explicit dataset.table.
# Isolated setup stays in traffic-runtime-t11-setup.sh (still refuses
# production names). This read-only gate may target the live table when
# T11_DATASET/T11_TABLE are set explicitly. Partition window + max bytes
# + job timeout bound the query. Does not print row payloads.
#
# T11_GATE=probe (intermediate): mart optional.
# T11_GATE=final (default, production accept): mart required, plus
# T11_EXPECT_K_REVISION with captured release SHA.
#
# Required env:
#   PROJECT_ID T11_DATASET T11_TABLE T11_CLICK_IDS
#   T11_EXPECT_TA_SOURCE T11_EXPECT_TA_SUB T11_EXPECT_RELEASE_SHA256
#   T11_EXPECT_CAMPAIGN_ID T11_EXPECT_ZONE_ID T11_EXPECT_COUNTRY
# T11_EXPECT_* string fields must be set; empty string means expect absent on probe.
# Optional probe: T11_MART_DATASET T11_MART_TABLE
# Final also requires those mart ids and nonempty T11_EXPECT_K_REVISION and
# T11_EXPECT_COUNTRY (empty expected country/revision cannot PASS final).
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"

if [[ -z "${PROJECT_ID:-}" ]]; then
  echo "PROJECT_ID is required (no default project; T11 does not source traffic-runtime-env.sh)" >&2
  exit 1
fi
: "${T11_DATASET:?T11_DATASET required}"
: "${T11_TABLE:?T11_TABLE required}"
: "${T11_CLICK_IDS:?T11_CLICK_IDS required (comma-separated)}"
if [[ -z "${T11_EXPECT_TA_SOURCE+x}" ]]; then
  echo "T11_EXPECT_TA_SOURCE must be set (empty string means expect absent)" >&2
  exit 1
fi
if [[ -z "${T11_EXPECT_TA_SUB+x}" ]]; then
  echo "T11_EXPECT_TA_SUB must be set (empty string means expect absent)" >&2
  exit 1
fi
: "${T11_EXPECT_RELEASE_SHA256:?T11_EXPECT_RELEASE_SHA256 required}"
if [[ -z "${T11_EXPECT_CAMPAIGN_ID+x}" ]]; then
  echo "T11_EXPECT_CAMPAIGN_ID must be set (empty string means expect absent)" >&2
  exit 1
fi
if [[ -z "${T11_EXPECT_ZONE_ID+x}" ]]; then
  echo "T11_EXPECT_ZONE_ID must be set (empty string means expect absent)" >&2
  exit 1
fi
if [[ -z "${T11_EXPECT_COUNTRY+x}" ]]; then
  echo "T11_EXPECT_COUNTRY must be set (empty string means expect absent)" >&2
  exit 1
fi

T11_GATE="${T11_GATE:-final}"
if [[ "${T11_GATE}" != "probe" && "${T11_GATE}" != "final" ]]; then
  echo "T11_GATE must be probe or final" >&2
  exit 1
fi
if [[ "${T11_GATE}" == "final" ]]; then
  : "${T11_MART_DATASET:?final gate requires T11_MART_DATASET}"
  : "${T11_MART_TABLE:?final gate requires T11_MART_TABLE}"
  if [[ -z "${T11_EXPECT_K_REVISION+x}" ]]; then
    echo "T11_EXPECT_K_REVISION must be set for final gate" >&2
    exit 1
  fi
  if [[ -z "${T11_EXPECT_K_REVISION}" ]]; then
    echo "final gate requires nonempty T11_EXPECT_K_REVISION" >&2
    exit 1
  fi
  if [[ -z "${T11_EXPECT_COUNTRY}" ]]; then
    echo "final gate requires nonempty T11_EXPECT_COUNTRY" >&2
    exit 1
  fi
fi

T11_LOCATION="${T11_LOCATION:-EU}"
if [[ -z "${T11_WINDOW_START:-}" ]]; then
  T11_WINDOW_START="$(python3 -c 'from datetime import datetime, timedelta, timezone; print((datetime.now(timezone.utc)-timedelta(days=1)).strftime("%Y-%m-%d"))')"
fi
T11_MAX_BYTES_BILLED="${T11_MAX_BYTES_BILLED:-10000000}"
T11_JOB_TIMEOUT_MS="${T11_JOB_TIMEOUT_MS:-60000}"
SQL_FILE="${ROOT}/scripts/traffic-runtime-t11-verify.sql"
EVAL_PY="${ROOT}/scripts/traffic-runtime-t11-eval.py"

IDS_SQL="$(python3 "${EVAL_PY}" --sql-in "${T11_CLICK_IDS}")"

SQL="$(sed \
  -e "s/{{PROJECT_ID}}/${PROJECT_ID}/g" \
  -e "s/{{DATASET}}/${T11_DATASET}/g" \
  -e "s/{{TABLE}}/${T11_TABLE}/g" \
  -e "s/{{WINDOW_START}}/${T11_WINDOW_START}/g" \
  -e "s/{{CLICK_IDS}}/${IDS_SQL}/g" \
  "${SQL_FILE}")"

echo "utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
echo "project=${PROJECT_ID}"
echo "table=${T11_DATASET}.${T11_TABLE}"
echo "gate=${T11_GATE}"
echo "window_start=${T11_WINDOW_START}"
echo "max_bytes_billed=${T11_MAX_BYTES_BILLED}"
echo "job_timeout_ms=${T11_JOB_TIMEOUT_MS}"
echo "click_ids=${T11_CLICK_IDS}"

ROWS="$(mktemp)"
MART_FILE="-"
cleanup() { rm -f "${ROWS}" "${MART_TMP:-}"; }
trap cleanup EXIT
bq query --use_legacy_sql=false --location="${T11_LOCATION}" --project_id="${PROJECT_ID}" \
  --maximum_bytes_billed="${T11_MAX_BYTES_BILLED}" \
  --job_timeout_ms="${T11_JOB_TIMEOUT_MS}" \
  --format=prettyjson \
  --quiet=true \
  "${SQL}" > "${ROWS}"

if [[ -n "${T11_MART_DATASET:-}" && -n "${T11_MART_TABLE:-}" ]]; then
  MART_SQL="$(sed \
    -e "s/{{PROJECT_ID}}/${PROJECT_ID}/g" \
    -e "s/{{MART_DATASET}}/${T11_MART_DATASET}/g" \
    -e "s/{{MART_TABLE}}/${T11_MART_TABLE}/g" \
    -e "s/{{CLICK_IDS}}/${IDS_SQL}/g" \
    "${ROOT}/scripts/traffic-runtime-t11-verify-mart.sql")"
  MART_TMP="$(mktemp)"
  bq query --use_legacy_sql=false --location="${T11_LOCATION}" --project_id="${PROJECT_ID}" \
    --maximum_bytes_billed="${T11_MAX_BYTES_BILLED}" \
    --job_timeout_ms="${T11_JOB_TIMEOUT_MS}" \
    --format=prettyjson \
    --quiet=true \
    "${MART_SQL}" > "${MART_TMP}"
  MART_FILE="${MART_TMP}"
fi

if [[ "${T11_GATE}" == "final" ]]; then
  python3 "${EVAL_PY}" --final "${ROWS}" "${MART_FILE}" "${T11_CLICK_IDS}" \
    "${T11_EXPECT_TA_SOURCE}" "${T11_EXPECT_TA_SUB}" \
    "${T11_EXPECT_CAMPAIGN_ID}" "${T11_EXPECT_ZONE_ID}" "${T11_EXPECT_COUNTRY}" \
    "${T11_EXPECT_RELEASE_SHA256}" "${T11_EXPECT_K_REVISION}"
else
  python3 "${EVAL_PY}" --attr "${ROWS}" "${MART_FILE}" "${T11_CLICK_IDS}" \
    "${T11_EXPECT_TA_SOURCE}" "${T11_EXPECT_TA_SUB}" \
    "${T11_EXPECT_CAMPAIGN_ID}" "${T11_EXPECT_ZONE_ID}" "${T11_EXPECT_COUNTRY}" \
    "${T11_EXPECT_RELEASE_SHA256}"
fi
