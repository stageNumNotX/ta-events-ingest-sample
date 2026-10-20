#!/usr/bin/env bash
# Ack leftover messages on the prototype verify subscription so the
# following smoke+pull sees this revision's events. Never touches ta-events.
# gcloud pull without --auto-ack does not ACK; messages reappear after the
# ack deadline. This script always passes --auto-ack on the dedicated verify-sub.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=traffic-runtime-env.sh
source "${ROOT}/scripts/traffic-runtime-env.sh"
export PATH="${HOME}/google-cloud-sdk/bin:${PATH}"

TOPIC="${PUBSUB_TOPIC:-ta-traffic-runtime-proto-events}"
SUB="${PUBSUB_VERIFY_SUB:-ta-traffic-runtime-proto-events-verify}"
LIMIT="${DRAIN_PULL_LIMIT:-50}"
MAX_ROUNDS="${DRAIN_MAX_ROUNDS:-20}"

if [[ "${TOPIC}" == "ta-events" || "${SUB}" == "ta-events-to-bq" ]]; then
  echo "refusing to drain production ingest pubsub" >&2
  exit 1
fi

PULL_JSON="$(mktemp)"
trap 'rm -f "${PULL_JSON}"' EXIT

count_pulled() {
  python3 - "$1" <<'PY'
import json, sys
raw = json.loads(open(sys.argv[1], encoding="utf-8").read().strip() or "[]")
print(len(raw) if isinstance(raw, list) else 0)
PY
}

total=0
rounds=0
empty=0
auto_ack=no
while (( rounds < MAX_ROUNDS )); do
  rounds=$((rounds + 1))
  PULL_ARGS=(
    pubsub subscriptions pull "${SUB}"
    --project "${PROJECT_ID}"
    --limit="${LIMIT}"
    --auto-ack
    --format=json
  )
  auto_ack=no
  for a in "${PULL_ARGS[@]}"; do
    if [[ "${a}" == "--auto-ack" ]]; then
      auto_ack=yes
    fi
  done
  if [[ "${auto_ack}" != "yes" ]]; then
    echo "refusing drain without --auto-ack" >&2
    exit 1
  fi
  if ! gcloud "${PULL_ARGS[@]}" > "${PULL_JSON}" 2>/tmp/ta-events-drain.err; then
    echo "pubsub drain pull failed" >&2
    cat /tmp/ta-events-drain.err >&2 || true
    exit 1
  fi
  n="$(count_pulled "${PULL_JSON}")"
  total=$((total + n))
  if [[ "${n}" -eq 0 ]]; then
    empty=1
    break
  fi
done

echo "drained_verify_sub n=${total} rounds=${rounds} empty=${empty} auto_ack=${auto_ack}"
if [[ "${empty}" -ne 1 ]]; then
  echo "verify sub still not empty after ${MAX_ROUNDS} auto-ack rounds" >&2
  exit 1
fi
