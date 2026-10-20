#!/usr/bin/env bash
# Assert live prototype behaviour. Does not follow Location (-L forbidden).
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=traffic-runtime-env.sh
source "${ROOT}/scripts/traffic-runtime-env.sh"
if [[ "${SERVICE}" != "${SERVING_SERVICE}" ]]; then
  echo "smoke only on serving ${SERVING_SERVICE}, got ${SERVICE}" >&2
  exit 1
fi
export PATH="${HOME}/google-cloud-sdk/bin:${PATH}"

URL="${1:-}"
if [[ -z "${URL}" ]]; then
  URL="$(gcloud run services describe "${SERVICE}" \
    --project "${PROJECT_ID}" --region "${REGION}" \
    --format='value(status.url)')"
fi
if [[ -z "${URL}" ]]; then
  echo "service URL is empty" >&2
  exit 1
fi

AUTH_ARGS=()
CLEANUP_AUTH=""
INVOKER_SA="github-actions-sa@${PROJECT_ID}.iam.gserviceaccount.com"

mint_identity_token() {
  local url="$1"
  local dest="$2"
  : > /tmp/ta-idtoken.err
  if gcloud auth print-identity-token --audiences="${url}" >"${dest}" 2>/tmp/ta-idtoken.err; then
    return 0
  fi
  if gcloud auth print-identity-token --audiences="${url}" \
    --impersonate-service-account="${INVOKER_SA}" --include-email \
    >"${dest}" 2>>/tmp/ta-idtoken.err; then
    return 0
  fi
  local access
  access="$(gcloud auth print-access-token)"
  python3 - "${INVOKER_SA}" "${url}" "${dest}" "${access}" <<'PY'
import json, sys, urllib.error, urllib.request
sa, aud, dest, token = sys.argv[1:]
req = urllib.request.Request(
    f"https://iamcredentials.googleapis.com/v1/projects/-/serviceAccounts/{sa}:generateIdToken",
    data=json.dumps({"audience": aud, "includeEmail": True}).encode(),
    headers={"Authorization": f"Bearer {token}", "Content-Type": "application/json"},
    method="POST",
)
try:
    with urllib.request.urlopen(req, timeout=15) as resp:
        body = json.load(resp)
except urllib.error.HTTPError as e:
    sys.stderr.write(f"generateIdToken HTTP {e.code}\n")
    sys.exit(1)
open(dest, "w", encoding="utf-8").write(body["token"])
PY
}

if [[ -n "${AUTH_HEADER_FILE:-}" ]]; then
  AUTH_ARGS=(--header @"${AUTH_HEADER_FILE}")
else
  TOKEN_FILE="$(mktemp)"
  HDR_FILE="$(mktemp)"
  chmod 600 "${TOKEN_FILE}" "${HDR_FILE}"
  CLEANUP_AUTH="${TOKEN_FILE} ${HDR_FILE}"
  trap 'rm -f ${CLEANUP_AUTH}' EXIT
  if mint_identity_token "${URL}" "${TOKEN_FILE}"; then
    printf 'Authorization: Bearer %s\n' "$(cat "${TOKEN_FILE}")" >"${HDR_FILE}"
    AUTH_ARGS=(--header @"${HDR_FILE}")
    AUTH_HEADER_FILE="${HDR_FILE}"
  else
    echo "identity token unavailable; enabling unauthenticated invoke on prototype only" >&2
    cat /tmp/ta-idtoken.err >&2 || true
    gcloud run services add-iam-policy-binding "${SERVICE}" \
      --project "${PROJECT_ID}" --region "${REGION}" \
      --member='allUsers' --role='roles/run.invoker' --quiet >/dev/null
    AUTH_ARGS=()
  fi
fi

tmpdir="$(mktemp -d)"
trap 'rm -rf "${tmpdir}" ${CLEANUP_AUTH}' EXIT

curl_auth() {
  curl --silent --show-error --max-time 10 "${AUTH_ARGS[@]}" "$@"
}

assert_eq() {
  local got="$1" want="$2" msg="$3"
  if [[ "${got}" != "${want}" ]]; then
    echo "FAIL ${msg}: got ${got}, want ${want}" >&2
    exit 1
  fi
}

health_code="$(curl_auth -o "${tmpdir}/health.body" -w '%{http_code}' "${URL}/health")"
assert_eq "${health_code}" "200" "GET /health"
assert_eq "$(cat "${tmpdir}/health.body")" "ok" "GET /health body"

ready_code="$(curl_auth -o "${tmpdir}/ready.body" -w '%{http_code}' "${URL}/ready")"
assert_eq "${ready_code}" "200" "GET /ready"
assert_eq "$(cat "${tmpdir}/ready.body")" "ok" "GET /ready body"

# Startup keeps dir LKG when the compiled pointer generation is gone.
# Poller reads latest out of band — wait, do not require the first probe.
deadline=$((SECONDS + 45))
contract_code=""
while (( SECONDS < deadline )); do
  contract_code="$(curl_auth -o "${tmpdir}/contract.body" -w '%{http_code}' "${URL}/contract" || true)"
  if [[ "${contract_code}" == "200" ]]; then
    act="$(python3 -c 'import json,sys; print((json.load(open(sys.argv[1])) or {}).get("activation",""))' "${tmpdir}/contract.body" 2>/dev/null || true)"
    if [[ "${act}" == "gcs-pointer" ]]; then
      break
    fi
  fi
  sleep 1
done
assert_eq "${contract_code}" "200" "GET /contract"
python3 - "${tmpdir}/contract.body" <<'PY'
import json, sys
path = sys.argv[1]
with open(path, encoding="utf-8") as f:
    data = json.load(f)
assert data.get("compileTarget") == "traffic-runtime/v1", data
assert data.get("activation") == "gcs-pointer", data
assert data.get("programSha256") == "53bfd83f26c6e7d3bc434236b51d1d618bf10b0e498ceb622eda52e7c05ae8d3", data
assert data.get("weightsSha256") == "8cc0537da05eed40ae702585373714bbaa1f6849a6cee9e8761aacde06fe3a7f", data
assert data.get("httpRelease", "").startswith("c1-payload-url"), data
req = data.get("request") or {}
assert req.get("contextSource") == "prototype-context", data
assert req.get("variantId") == "smartlink-main", data
assert req.get("enabled") is True and req.get("hasURL") is True, data
t6 = data.get("t6") or {}
assert t6.get("variantId") == "disabled_target", data
print(
    "OK contract layoutId=" + str(data.get("layoutId"))
    + " pointerVersion=" + str(data.get("pointerVersion"))
    + " pointerGeneration=" + str(data.get("pointerGeneration"))
    + " artifactSetId=" + str(data.get("artifactSetId"))
    + " request=" + str(req) + " t6=" + str(t6)
)
PY
head_contract="$(curl_auth --head -o /dev/null -w '%{http_code}' "${URL}/contract")"
assert_eq "${head_contract}" "405" "HEAD /contract"

# Do not follow redirect: no -L.
redir_code="$(curl_auth -D "${tmpdir}/redir.hdr" -o "${tmpdir}/redir.body" -w '%{http_code}' \
  "${URL}/?ta_source=proto-smoke")"
assert_eq "${redir_code}" "302" "GET / status"

# Headers may be mixed-case.
location="$(awk 'BEGIN{IGNORECASE=1} /^Location:/{sub(/\r$/,""); sub(/^Location:[[:space:]]*/,""); print; exit}' "${tmpdir}/redir.hdr")"
cc="$(awk 'BEGIN{IGNORECASE=1} /^Cache-Control:/{sub(/\r$/,""); sub(/^Cache-Control:[[:space:]]*/,""); print; exit}' "${tmpdir}/redir.hdr")"

if [[ "${location}" != https://example.com/* ]]; then
  echo "FAIL Location host/path: ${location}" >&2
  exit 1
fi
if [[ "${location}" != *campaign=contract-smoke* ]]; then
  echo "FAIL Location missing C1 payload.url marker: ${location}" >&2
  exit 1
fi
if [[ "${location}" != *click_id=* ]]; then
  echo "FAIL Location missing click_id: ${location}" >&2
  exit 1
fi
case "${location}" in
  *ta_source=proto-smoke*) ;;
  *) echo "FAIL Location missing ta_source: ${location}" >&2; exit 1 ;;
esac
if [[ "${cc}" != *no-store* ]]; then
  echo "FAIL Cache-Control=${cc}" >&2
  exit 1
fi

# --head (not -X HEAD): do not wait for a body.
head_code="$(curl_auth --head -o /dev/null -w '%{http_code}' "${URL}/")"
assert_eq "${head_code}" "405" "HEAD /"

echo "PASS smoke url=${URL} location_host=example.com campaign=contract-smoke cache=no-store"
