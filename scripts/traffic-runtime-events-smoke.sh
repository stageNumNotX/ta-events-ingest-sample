#!/usr/bin/env bash
# Pull prototype events after HTTP 302. Does not touch ta-events.
# Does not print message bodies (may include URLs). Enqueue is not this proof.
# gcloud pull without --auto-ack does not ACK. This script always passes
# --auto-ack on the dedicated verify-sub and records that fact in evidence.
# Publisher is best-effort and two workers can deliver the triple across
# more than one pull; accumulate auto-acked batches instead of requiring
# all three names in a single gcloud response.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=traffic-runtime-env.sh
source "${ROOT}/scripts/traffic-runtime-env.sh"
export PATH="${HOME}/google-cloud-sdk/bin:${PATH}"

TOPIC="${PUBSUB_TOPIC:-ta-traffic-runtime-proto-events}"
SUB="${PUBSUB_VERIFY_SUB:-ta-traffic-runtime-proto-events-verify}"
EVIDENCE="${ROOT}/docs/traffic-runtime/evidence/P4_EVENTS.txt"
MAX_ROUNDS="${EVENTS_PULL_ROUNDS:-10}"
SLEEP_SECS="${EVENTS_PULL_SLEEP_SECS:-3}"

if [[ "${TOPIC}" == "ta-events" || "${SUB}" == "ta-events-to-bq" ]]; then
  echo "refusing to pull production ingest pubsub" >&2
  exit 1
fi

PULL_JSON="$(mktemp)"
ACCUM_JSON="$(mktemp)"
trap 'rm -f "${PULL_JSON}" "${ACCUM_JSON}"' EXIT
echo '[]' > "${ACCUM_JSON}"

PULL_ARGS=(
  pubsub subscriptions pull "${SUB}"
  --project "${PROJECT_ID}"
  --limit=30
  --auto-ack
  --format=json
)
ACKED=no
for a in "${PULL_ARGS[@]}"; do
  if [[ "${a}" == "--auto-ack" ]]; then
    ACKED=yes
  fi
done
if [[ "${ACKED}" != "yes" ]]; then
  echo "refusing events pull without --auto-ack" >&2
  exit 1
fi

eval_accum() {
  local final="$1"
  python3 "${ROOT}/scripts/traffic-runtime-events-eval.py" \
    "${PULL_JSON}" "${ACCUM_JSON}" "${EVIDENCE}" "${ACKED}" "${final}"
}

write_pull_fail() {
  {
    echo "utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
    echo "result=FAIL"
    echo "reason=pull_api"
    echo "acked_by_gcloud_pull=no"
  } > "${EVIDENCE}"
}

round=0
while (( round < MAX_ROUNDS )); do
  round=$((round + 1))
  sleep "${SLEEP_SECS}"
  if ! gcloud "${PULL_ARGS[@]}" > "${PULL_JSON}" 2>/tmp/ta-events-pull.err; then
    echo "pubsub pull failed" >&2
    cat /tmp/ta-events-pull.err >&2 || true
    write_pull_fail
    exit 1
  fi
  final=0
  if (( round == MAX_ROUNDS )); then
    final=1
  fi
  rc=0
  eval_accum "${final}" || rc=$?
  if [[ "${rc}" -eq 0 ]]; then
    exit 0
  fi
  if [[ "${rc}" -eq 1 ]]; then
    exit 1
  fi
done
exit 1
