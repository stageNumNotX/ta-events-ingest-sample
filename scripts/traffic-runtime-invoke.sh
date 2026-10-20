# Sourced helper: mint a private identity token into AUTH_ARGS.
# Does not print the token. Caller may set SERVICE_URL as audience fallback.
# shellcheck disable=SC2034

INVOKER_SA="${INVOKER_SA:-github-actions-sa@${PROJECT_ID}.iam.gserviceaccount.com}"

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

prepare_auth() {
  local url="$1"
  AUTH_ARGS=()
  if [[ -n "${AUTH_HEADER_FILE:-}" ]]; then
    AUTH_ARGS=(--header @"${AUTH_HEADER_FILE}")
    return 0
  fi
  # Cloud Run checks aud against the untagged service URL.
  local audience="${SERVICE_URL:-${url}}"
  local token_file hdr_file
  token_file="$(mktemp)"
  hdr_file="$(mktemp)"
  chmod 600 "${token_file}" "${hdr_file}"
  CLEANUP_AUTH="${CLEANUP_AUTH:-} ${token_file} ${hdr_file}"
  if ! mint_identity_token "${audience}" "${token_file}"; then
    echo "identity token unavailable audience=${audience} url=${url}" >&2
    cat /tmp/ta-idtoken.err >&2 || true
    return 1
  fi
  printf 'Authorization: Bearer %s\n' "$(cat "${token_file}")" >"${hdr_file}"
  AUTH_ARGS=(--header @"${hdr_file}")
  return 0
}

curl_auth() {
  curl --silent --show-error --max-time 15 "${AUTH_ARGS[@]}" "$@"
}
