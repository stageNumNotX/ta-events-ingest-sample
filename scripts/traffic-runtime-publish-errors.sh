#!/usr/bin/env bash
# Print sanitized Cloud Run publisher errors. Never dump message bodies or URLs.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=traffic-runtime-env.sh
source "${ROOT}/scripts/traffic-runtime-env.sh"
export PATH="${HOME}/google-cloud-sdk/bin:${PATH}"

if [[ "${SERVICE}" == "ta-events-ingest" ]]; then
  echo "refusing to read production ingest logs" >&2
  exit 1
fi

FILTER='resource.type="cloud_run_revision" AND resource.labels.service_name="ta-traffic-runtime-proto" AND (jsonPayload.msg="runtime event publish failed" OR jsonPayload.msg="runtime event enqueue dropped")'

RAW="$(mktemp)"
trap 'rm -f "${RAW}"' EXIT
if ! gcloud logging read "${FILTER}" \
  --project "${PROJECT_ID}" \
  --freshness=20m \
  --limit=30 \
  --format='json(timestamp,jsonPayload.msg,jsonPayload.event_name,jsonPayload.error,jsonPayload.attempts)' \
  > "${RAW}" 2>/tmp/ta-publish-errors.err; then
  echo "publish_error_logs=read_failed"
  cat /tmp/ta-publish-errors.err >&2 || true
  exit 0
fi

python3 - "${RAW}" <<'PY'
import json, re, sys
raw = json.loads(open(sys.argv[1], encoding="utf-8").read().strip() or "[]")
if not isinstance(raw, list) or not raw:
    print("publish_error_logs=empty")
    sys.exit(0)
print("publish_error_logs n=%d" % len(raw))

def scrub(s: str) -> str:
    s = re.sub(r"https?://\S+", "https://…", s)
    s = re.sub(r"click_id=[^&\s\"']+", "click_id=…", s)
    return s[:240]

for item in raw[:20]:
    payload = (item or {}).get("jsonPayload") or {}
    ts = str((item or {}).get("timestamp") or "")
    msg = str(payload.get("msg") or "")
    name = str(payload.get("event_name") or "")
    attempts = payload.get("attempts")
    err = scrub(str(payload.get("error") or ""))
    print("ts=%s msg=%s event_name=%s attempts=%s error=%s" % (ts, msg, name, attempts, err))
PY
