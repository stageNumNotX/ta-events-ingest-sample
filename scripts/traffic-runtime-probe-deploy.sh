#!/usr/bin/env bash
# Sourced helper: deploy one 100% revision on ta-traffic-runtime-proto-probe.
# Never deploys, tags, or retargets the serving service.
# shellcheck disable=SC2034

require_probe_service() {
  if [[ "${SERVICE}" != "${PROBE_SERVICE}" ]]; then
    echo "refusing: T3/T5/T7 must use SERVICE=${PROBE_SERVICE}, got ${SERVICE:-empty}" >&2
    exit 1
  fi
}

describe_service_json() {
  local name="$1"
  gcloud run services describe "${name}" \
    --project "${PROJECT_ID}" --region "${REGION}" --format=json
}

percent_revision_from_json() {
  python3 -c 'import json,sys
d=json.load(sys.stdin)
for t in d.get("status",{}).get("traffic") or []:
    if t.get("percent")==100 and t.get("revisionName"):
        print(t["revisionName"]); break'
}

service_url_from_json() {
  python3 -c 'import json,sys; print((json.load(sys.stdin).get("status") or {}).get("url") or "")'
}

serving_percent_revision() {
  percent_revision_from_json <<<"$(describe_service_json "${SERVING_SERVICE}")"
}

latest_created_revision() {
  local name="$1"
  gcloud run services describe "${name}" \
    --project "${PROJECT_ID}" --region "${REGION}" \
    --format='value(status.latestCreatedRevisionName)'
}

revision_ready() {
  local rev="$1"
  local json status
  json="$(gcloud run revisions describe "${rev}" \
    --project "${PROJECT_ID}" --region "${REGION}" --format=json)"
  status="$(python3 -c 'import json,sys
d=json.load(sys.stdin)
for c in d.get("status",{}).get("conditions") or []:
    if c.get("type")=="Ready":
        print(c.get("status") or "")
        break' <<<"${json}")"
  [[ "${status}" == "True" ]]
}

bind_probe_invoker() {
  local account="$1"
  local member
  if [[ -z "${account}" ]]; then
    return 0
  fi
  if [[ "${account}" == *.gserviceaccount.com ]]; then
    member="serviceAccount:${account}"
  else
    member="user:${account}"
  fi
  gcloud run services add-iam-policy-binding "${PROBE_SERVICE}" \
    --project "${PROJECT_ID}" --region "${REGION}" \
    --member="${member}" --role='roles/run.invoker' --quiet >/dev/null
}

bind_probe_invokers() {
  local active
  active="$(gcloud auth list --filter=status:ACTIVE --format='value(account)' | head -n1 || true)"
  bind_probe_invoker "${active}" || true
  bind_probe_invoker "github-actions-sa@${PROJECT_ID}.iam.gserviceaccount.com" || true
}

# deploy_probe_revision <startup_path> <envvars>
# Pins 100% to the created revision only after it is Ready.
# Sets PROBE_REV, PROBE_URL, SERVICE_URL (identity-token audience).
deploy_probe_revision() {
  local probe_path="$1"
  local envvars="$2"
  PROBE_REV=""
  PROBE_URL=""

  if [[ "${IMAGE_BY_DIGEST}" != *@sha256:* ]]; then
    echo "IMAGE_BY_DIGEST digest URI required" >&2
    return 1
  fi

  if ! gcloud run deploy "${PROBE_SERVICE}" \
    --project "${PROJECT_ID}" --region "${REGION}" \
    --image "${IMAGE_BY_DIGEST}" \
    --service-account "${RUNTIME_SA}" \
    --cpu=1 --memory=512Mi --concurrency=40 --timeout=10s \
    --min-instances=1 --max-instances=2 --no-cpu-throttling \
    --no-allow-unauthenticated --port=8080 \
    --startup-probe="httpGet.path=${probe_path},httpGet.port=8080,periodSeconds=5,timeoutSeconds=2,failureThreshold=24" \
    --liveness-probe='httpGet.path=/health,httpGet.port=8080,periodSeconds=30,timeoutSeconds=2,failureThreshold=3' \
    --set-env-vars="${envvars}" \
    --labels='purpose=traffic-runtime-prototype,role=probe' \
    --quiet; then
    return 1
  fi

  local created
  created="$(latest_created_revision "${PROBE_SERVICE}")"
  if [[ -z "${created}" ]]; then
    echo "probe latestCreatedRevisionName is empty" >&2
    return 1
  fi
  if ! revision_ready "${created}"; then
    echo "probe revision ${created} is not Ready; not shifting probe traffic" >&2
    return 1
  fi
  gcloud run services update-traffic "${PROBE_SERVICE}" \
    --project "${PROJECT_ID}" --region "${REGION}" \
    --to-revisions="${created}=100" --quiet

  bind_probe_invokers

  local desc
  desc="$(describe_service_json "${PROBE_SERVICE}")"
  PROBE_REV="$(percent_revision_from_json <<<"${desc}")"
  PROBE_URL="$(service_url_from_json <<<"${desc}")"
  if [[ -z "${PROBE_REV}" || "${PROBE_REV}" != "${created}" ]]; then
    echo "probe 100% revision ${PROBE_REV:-none} != created ${created}" >&2
    return 1
  fi
  if [[ -z "${PROBE_URL}" ]]; then
    echo "probe service URL is empty" >&2
    return 1
  fi
  SERVICE_URL="${PROBE_URL}"
  export SERVICE_URL PROBE_URL PROBE_REV
  return 0
}

record_serving_baseline() {
  SERVING_REV_BASELINE="$(serving_percent_revision)"
  if [[ -z "${SERVING_REV_BASELINE}" ]]; then
    echo "serving 100% revision on ${SERVING_SERVICE} is empty" >&2
    exit 1
  fi
  export SERVING_REV_BASELINE
}

assert_serving_unmoved() {
  local after
  after="$(serving_percent_revision)"
  if [[ "${after}" != "${SERVING_REV_BASELINE}" ]]; then
    echo "serving revision shifted ${SERVING_REV_BASELINE} -> ${after}" >&2
    return 1
  fi
  return 0
}

scale_probe_idle() {
  if ! gcloud run services describe "${PROBE_SERVICE}" \
    --project "${PROJECT_ID}" --region "${REGION}" \
    --format='value(metadata.name)' >/dev/null 2>/tmp/ta-probe-idle.err; then
    echo "probe service ${PROBE_SERVICE} absent; nothing to scale"
    return 0
  fi
  gcloud run services update "${PROBE_SERVICE}" \
    --project "${PROJECT_ID}" --region "${REGION}" \
    --min-instances=0 --max-instances=1 --quiet
  echo "probe ${PROBE_SERVICE} min-instances=0"
}
