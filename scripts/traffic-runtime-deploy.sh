#!/usr/bin/env bash
# Build digest image and deploy isolated ta-traffic-runtime-proto.
# Serving only: one revision, pin 100% to that revision. No --no-traffic / --tag.
# Never deploys ta-events-ingest or the T3/T5/T7 probe service.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=traffic-runtime-env.sh
source "${ROOT}/scripts/traffic-runtime-env.sh"
if [[ "${SERVICE}" != "${SERVING_SERVICE}" ]]; then
  echo "deploy.sh deploys only ${SERVING_SERVICE}, got ${SERVICE}" >&2
  exit 1
fi
cd "${ROOT}"

export PATH="${HOME}/google-cloud-sdk/bin:${PATH}"

SHA="$(git rev-parse HEAD)"
if [[ -n "${GITHUB_SHA:-}" ]]; then
  if [[ "${SHA}" != "${GITHUB_SHA}" ]]; then
    echo "HEAD ${SHA} != GITHUB_SHA ${GITHUB_SHA}" >&2
    exit 1
  fi
else
  if [[ -n "$(git status --porcelain)" ]]; then
    echo "refusing deploy from dirty working tree" >&2
    git status --porcelain >&2
    exit 1
  fi
fi
if ls "${ROOT}"/gha-creds-*.json >/dev/null 2>&1; then
  if ! grep -qx 'gha-creds-\*\.json' "${ROOT}/.gcloudignore"; then
    echo "refusing Cloud Build upload: WIF creds in workspace and not gcloudignored" >&2
    exit 1
  fi
fi
IMAGE_NAME="${REGION}-docker.pkg.dev/${PROJECT_ID}/${AR_REPO}/runtime"

echo "project=${PROJECT_ID} region=${REGION} service=${SERVICE} sha=${SHA}"

# Owner created AR `ta-traffic-runtime` and runtime SA
# `ta-traffic-runtime-proto@…` (2026-09-10). Prefer describe-if-exists.
# github-actions-sa cannot serviceusage.services.enable; do not treat
# that as a deploy blocker. Fallback to gcr.io / default compute SA only
# if describe/create is still denied.
if gcloud artifacts repositories describe "${AR_REPO}" \
  --project "${PROJECT_ID}" --location="${REGION}" >/dev/null 2>/tmp/ta-ar.err; then
  echo "artifact registry ${AR_REPO} exists"
  IMAGE_NAME="${REGION}-docker.pkg.dev/${PROJECT_ID}/${AR_REPO}/runtime"
else
  if grep -qi 'NOT_FOUND\|NOT FOUND' /tmp/ta-ar.err; then
    if gcloud artifacts repositories create "${AR_REPO}" \
      --project "${PROJECT_ID}" --location="${REGION}" \
      --repository-format=docker \
      --description='Traffic runtime prototype images' \
      --quiet; then
      echo "created artifact registry ${AR_REPO}"
      IMAGE_NAME="${REGION}-docker.pkg.dev/${PROJECT_ID}/${AR_REPO}/runtime"
    else
      echo "AR create failed; falling back to gcr.io (prototype only)" >&2
      IMAGE_NAME="gcr.io/${PROJECT_ID}/ta-traffic-runtime"
    fi
  elif grep -qi 'PERMISSION_DENIED\|Permission denied' /tmp/ta-ar.err; then
    echo "AR describe denied; falling back to gcr.io (prototype only)" >&2
    cat /tmp/ta-ar.err >&2
    IMAGE_NAME="gcr.io/${PROJECT_ID}/ta-traffic-runtime"
  else
    echo "artifact registry describe unexpected; falling back to gcr.io (prototype only)" >&2
    cat /tmp/ta-ar.err >&2
    IMAGE_NAME="gcr.io/${PROJECT_ID}/ta-traffic-runtime"
  fi
fi
IMAGE="${IMAGE_NAME}:${SHA}"
echo "image=${IMAGE}"

# C1 overlay from WIF fetch-pinned (PINNED_OVERLAY). Not the bootstrap testdata pin.
# shellcheck source=traffic-runtime-pins.env
source "${ROOT}/scripts/traffic-runtime-pins.env"
if [[ -z "${PINNED_OVERLAY:-}" ]]; then
  echo "PINNED_OVERLAY is required so the image is backed by C1 payload.url bytes" >&2
  exit 1
fi
mkdir -p "${ROOT}/docker-pinned"
verify_overlay() {
  local kind="$1" sha="$2" size="$3"
  local src="${PINNED_OVERLAY}/${kind}.json"
  if [[ ! -f "${src}" ]]; then
    echo "missing ${src}" >&2
    exit 1
  fi
  local actual_size actual_sha
  actual_size="$(wc -c < "${src}" | tr -d ' ')"
  actual_sha="$(sha256sum "${src}" | awk '{print $1}')"
  if [[ "${actual_size}" != "${size}" ]]; then
    echo "overlay ${kind} size got=${actual_size} want=${size}" >&2
    exit 1
  fi
  if [[ "${actual_sha}" != "${sha}" ]]; then
    echo "overlay ${kind} sha256 got=${actual_sha} want=${sha}" >&2
    exit 1
  fi
  cp "${src}" "${ROOT}/docker-pinned/${kind}.json"
  echo "overlay ${kind} sha256=${actual_sha} size=${actual_size}"
}
verify_overlay program "${PROGRAM_SHA256}" "${PROGRAM_SIZE}"
verify_overlay weights "${WEIGHTS_SHA256}" "${WEIGHTS_SIZE}"
verify_overlay release "${RELEASE_SHA256}" "${RELEASE_SIZE}"

DEPLOY_SA_FLAG=()
if gcloud iam service-accounts describe "${RUNTIME_SA}" --project "${PROJECT_ID}" >/dev/null 2>/tmp/ta-sa.err; then
  echo "runtime sa exists"
  DEPLOY_SA_FLAG=(--service-account "${RUNTIME_SA}")
else
  if grep -qi 'NOT_FOUND\|NOT FOUND' /tmp/ta-sa.err; then
    if gcloud iam service-accounts create "${RUNTIME_SA_NAME}" \
      --project "${PROJECT_ID}" \
      --display-name='Traffic runtime prototype' \
      --quiet; then
      DEPLOY_SA_FLAG=(--service-account "${RUNTIME_SA}")
    else
      echo "runtime sa create failed; Cloud Run default compute SA will be used" >&2
    fi
  elif grep -qi 'PERMISSION_DENIED\|Permission denied' /tmp/ta-sa.err; then
    echo "runtime sa describe denied; Cloud Run default compute SA will be used" >&2
    cat /tmp/ta-sa.err >&2
  else
    echo "runtime sa describe failed:" >&2
    cat /tmp/ta-sa.err >&2
    exit 1
  fi
fi

BUILD_ID=""
if BUILD_ID="$(gcloud builds submit "${ROOT}" \
  --project "${PROJECT_ID}" --region "${REGION}" \
  --config "${ROOT}/deploy/traffic-runtime.cloudbuild.yaml" \
  --substitutions="_IMAGE=${IMAGE},_REVISION=${SHA}" \
  --format='value(id)')"; then
  echo "build_id=${BUILD_ID} location=${REGION}"
else
  echo "regional Cloud Build failed; retrying global"
  BUILD_ID="$(gcloud builds submit "${ROOT}" \
    --project "${PROJECT_ID}" \
    --config "${ROOT}/deploy/traffic-runtime.cloudbuild.yaml" \
    --substitutions="_IMAGE=${IMAGE},_REVISION=${SHA}" \
    --format='value(id)')"
  echo "build_id=${BUILD_ID} location=global"
fi

DIGEST=""
if DIGEST="$(gcloud artifacts docker images describe "${IMAGE}" \
  --project "${PROJECT_ID}" \
  --format='value(image_summary.digest)' 2>/tmp/ta-digest.err)"; then
  :
elif DIGEST="$(gcloud container images describe "${IMAGE}" \
  --format='value(image_summary.digest)' 2>/tmp/ta-digest2.err)"; then
  :
else
  echo "image digest lookup failed" >&2
  cat /tmp/ta-digest.err >&2 || true
  cat /tmp/ta-digest2.err >&2 || true
  exit 1
fi
case "${DIGEST}" in
  sha256:*) ;;
  *) echo "unexpected digest ${DIGEST}" >&2; exit 1 ;;
esac
IMAGE_BY_DIGEST="${IMAGE_NAME}@${DIGEST}"
echo "image_by_digest=${IMAGE_BY_DIGEST}"

# Isolated prototype topic. Permission denial is recorded, not a deploy blocker.
bash "${ROOT}/scripts/traffic-runtime-pubsub.sh"

gcloud run deploy "${SERVICE}" \
  --project "${PROJECT_ID}" --region "${REGION}" \
  --image "${IMAGE_BY_DIGEST}" \
  "${DEPLOY_SA_FLAG[@]}" \
  --cpu=1 --memory=512Mi --concurrency=40 --timeout=10s \
  --min-instances=1 --max-instances=2 --no-cpu-throttling \
  --no-allow-unauthenticated \
  --port=8080 \
  --startup-probe='httpGet.path=/ready,httpGet.port=8080,periodSeconds=5,timeoutSeconds=2,failureThreshold=24' \
  --liveness-probe='httpGet.path=/health,httpGet.port=8080,periodSeconds=30,timeoutSeconds=2,failureThreshold=3' \
  --set-env-vars="PROJECT_ID=${PROJECT_ID},PUBSUB_TOPIC=ta-traffic-runtime-proto-events,PROTOTYPE_MODE=true,PROTOTYPE_REDIRECT_URL=https://example.com/,PINNED_DIR=/pinned,ARTIFACT_SOURCE=gcs,TRAFFIC_USE_POINTER=1,TRAFFIC_POINTER_POLL=1,TRAFFIC_POINTER_POLL_INTERVAL=5s,TRAFFIC_POINTER_BUCKET=${BUCKET},TRAFFIC_POINTER_OBJECT=${POINTER_OBJECT},TRAFFIC_POINTER_GENERATION=${POINTER_GENERATION},TRAFFIC_POINTER_SIZE=${POINTER_SIZE},TRAFFIC_POINTER_SHA256=${POINTER_SHA256},TRAFFIC_PIN_BUCKET=${BUCKET},TRAFFIC_PIN_PROGRAM_OBJECT=${PROGRAM_OBJECT},TRAFFIC_PIN_PROGRAM_SHA256=${PROGRAM_SHA256},TRAFFIC_PIN_PROGRAM_GENERATION=${PROGRAM_GENERATION},TRAFFIC_PIN_PROGRAM_SIZE=${PROGRAM_SIZE},TRAFFIC_PIN_WEIGHTS_OBJECT=${WEIGHTS_OBJECT},TRAFFIC_PIN_WEIGHTS_SHA256=${WEIGHTS_SHA256},TRAFFIC_PIN_WEIGHTS_GENERATION=${WEIGHTS_GENERATION},TRAFFIC_PIN_WEIGHTS_SIZE=${WEIGHTS_SIZE},TRAFFIC_PIN_RELEASE_OBJECT=${RELEASE_OBJECT},TRAFFIC_PIN_RELEASE_SHA256=${RELEASE_SHA256},TRAFFIC_PIN_RELEASE_GENERATION=${RELEASE_GENERATION},TRAFFIC_PIN_RELEASE_SIZE=${RELEASE_SIZE}" \
  --labels='purpose=traffic-runtime-prototype' \
  --quiet

# Pin 100% to the revision this deploy created. Do not use --to-latest:
# leftover LATEST aliases from old --no-traffic tags pointed at stale revisions.
CREATED="$(gcloud run services describe "${SERVICE}" \
  --project "${PROJECT_ID}" --region "${REGION}" \
  --format='value(status.latestCreatedRevisionName)')"
if [[ -z "${CREATED}" ]]; then
  echo "latestCreatedRevisionName is empty" >&2
  exit 1
fi
gcloud run services update-traffic "${SERVICE}" \
  --project "${PROJECT_ID}" --region "${REGION}" \
  --to-revisions="${CREATED}=100" --quiet
echo "traffic_to=${CREATED}"

# Drop leftover tag URLs from the retired serving-service probe ballet.
LEFTOVER_TAGS="$(gcloud run services describe "${SERVICE}" \
  --project "${PROJECT_ID}" --region "${REGION}" --format=json | python3 -c 'import json,sys
d=json.load(sys.stdin)
tags=[]
for t in d.get("status",{}).get("traffic") or []:
    tag=t.get("tag")
    if tag and tag not in tags:
        tags.append(tag)
print(",".join(tags))')"
if [[ -n "${LEFTOVER_TAGS}" ]]; then
  gcloud run services update-traffic "${SERVICE}" \
    --project "${PROJECT_ID}" --region "${REGION}" \
    --remove-tags="${LEFTOVER_TAGS}" --quiet
  echo "removed_serving_tags=${LEFTOVER_TAGS}"
  gcloud run services update-traffic "${SERVICE}" \
    --project "${PROJECT_ID}" --region "${REGION}" \
    --to-revisions="${CREATED}=100" --quiet
fi

bind_invoker() {
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
  gcloud run services add-iam-policy-binding "${SERVICE}" \
    --project "${PROJECT_ID}" --region "${REGION}" \
    --member="${member}" --role='roles/run.invoker' --quiet >/dev/null
}

ACTIVE="$(gcloud auth list --filter=status:ACTIVE --format='value(account)' | head -n1 || true)"
bind_invoker "${ACTIVE}" || true
bind_invoker "github-actions-sa@${PROJECT_ID}.iam.gserviceaccount.com" || true

DESC="$(gcloud run services describe "${SERVICE}" \
  --project "${PROJECT_ID}" --region "${REGION}" --format=json)"
URL="$(python3 -c 'import json,sys; print((json.load(sys.stdin).get("status") or {}).get("url") or "")' <<<"${DESC}")"
REVISION="$(python3 -c 'import json,sys
d=json.load(sys.stdin)
for t in d.get("status",{}).get("traffic") or []:
    if t.get("percent")==100 and t.get("revisionName"):
        print(t["revisionName"]); break' <<<"${DESC}")"
LATEST="$(python3 -c 'import json,sys; print((json.load(sys.stdin).get("status") or {}).get("latestReadyRevisionName") or "")' <<<"${DESC}")"
if [[ -z "${REVISION}" || "${REVISION}" != "${CREATED}" ]]; then
  echo "serving 100% revision ${REVISION:-none} != created ${CREATED}" >&2
  exit 1
fi
STILL_TAGS="$(python3 -c 'import json,sys
d=json.load(sys.stdin)
tags=[]
for t in d.get("status",{}).get("traffic") or []:
    tag=t.get("tag")
    if tag and tag not in tags:
        tags.append(tag)
print(",".join(tags))' <<<"${DESC}")"
if [[ -n "${STILL_TAGS}" ]]; then
  echo "serving still has traffic tags ${STILL_TAGS}" >&2
  exit 1
fi
REV_IMAGE="$(gcloud run revisions describe "${REVISION}" \
  --project "${PROJECT_ID}" --region "${REGION}" \
  --format='value(spec.containers[0].image)')"
if [[ "${REV_IMAGE}" != "${IMAGE_BY_DIGEST}" ]]; then
  echo "serving image ${REV_IMAGE} != ${IMAGE_BY_DIGEST}" >&2
  exit 1
fi

EVIDENCE_DIR="${ROOT}/docs/traffic-runtime/evidence"
mkdir -p "${EVIDENCE_DIR}"
{
  echo "utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "sha=${SHA}"
  echo "build_id=${BUILD_ID}"
  echo "image=${IMAGE_BY_DIGEST}"
  echo "revision=${REVISION}"
  echo "url=${URL}"
  echo "service=${SERVICE}"
  echo "project=${PROJECT_ID}"
  echo "region=${REGION}"
  if [[ ${#DEPLOY_SA_FLAG[@]} -eq 0 ]]; then
    echo "runtime_sa=CloudRunDefault"
  else
    echo "runtime_sa=${RUNTIME_SA}"
  fi
  echo "pinned_dir=/pinned"
  echo "artifact_source=gcs"
  echo "activation_expected=gcs-pointer"
  echo "pointer_object=${POINTER_OBJECT}"
  echo "pointer_generation=${POINTER_GENERATION}"
  echo "pointer_sha256=${POINTER_SHA256}"
  echo "http_release=c1-payload-url"
  echo "program_sha256=${PROGRAM_SHA256}"
  echo "program_generation=${PROGRAM_GENERATION}"
  echo "weights_sha256=${WEIGHTS_SHA256}"
  echo "weights_generation=${WEIGHTS_GENERATION}"
  echo "release_sha256=${RELEASE_SHA256}"
  echo "release_generation=${RELEASE_GENERATION}"
  echo "pinned_overlay=wif-fetch"
} > "${EVIDENCE_DIR}/P1_DEPLOY.txt"

echo "deployed ${SERVICE} revision=${REVISION} url=${URL}"
