#!/usr/bin/env bash
# Fetch owner-drop handoff bytes from GCS. Compare the manifest digest to the
# compiled owner pin. Do not treat the downloaded hash as a new trust anchor.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=traffic-runtime-env.sh
source "${ROOT}/scripts/traffic-runtime-env.sh"
# shellcheck source=traffic-runtime-pins.env
source "${ROOT}/scripts/traffic-runtime-pins.env"
export PATH="${HOME}/google-cloud-sdk/bin:${PATH}"

OUT="${HANDOFF_OUT:-${ROOT}/docs/traffic-runtime/evidence/owner-drop}"
mkdir -p "${OUT}"
EVIDENCE="${ROOT}/docs/traffic-runtime/evidence/T2_HANDOFF.txt"
: >"${EVIDENCE}"
log() { echo "$1" | tee -a "${EVIDENCE}"; }

log "utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
log "bucket=${BUCKET}"
log "owner_drop=gs://${BUCKET}/${OWNER_DROP_PREFIX}/"
log "compiled_manifest_sha256=${COMPILED_MANIFEST_SHA256}"
log "compiled_manifest_bytes=${COMPILED_MANIFEST_BYTES}"
log "self_oracle=no"

LIST="$(gcloud storage ls "gs://${BUCKET}/${OWNER_DROP_PREFIX}/" 2>/tmp/ta-handoff-ls.err || true)"
if [[ -z "${LIST}" ]]; then
  log "owner_drop=missing"
  log "list_error=$(tr '\n' ' ' </tmp/ta-handoff-ls.err | head -c 240)"
  log "t2=PARTIAL"
  log "t2_block=owner_drop_unreadable"
  echo "owner-drop not readable; T2 stays PARTIAL" >&2
  exit 0
fi
log "owner_drop_objects=$(echo "${LIST}" | wc -l | tr -d ' ')"

copy_one() {
  local name="$1"
  local dest="${OUT}/${name}"
  if gcloud storage cp "gs://${BUCKET}/${OWNER_DROP_PREFIX}/${name}" "${dest}" --quiet >/dev/null 2>/tmp/ta-handoff-cp.err; then
    local size sha
    size="$(wc -c < "${dest}" | tr -d ' ')"
    sha="$(sha256sum "${dest}" | awk '{print $1}')"
    log "${name}_sha256=${sha}"
    log "${name}_bytes=${size}"
    return 0
  fi
  log "${name}=missing"
  return 1
}

got_manifest=0
got_c1=0
copy_one manifest.json && got_manifest=1 || true
if copy_one program.json && copy_one weights.json && copy_one release.json; then
  got_c1=1
fi

if [[ "${got_c1}" -eq 1 ]]; then
  fail=0
  actual="$(sha256sum "${OUT}/program.json" | awk '{print $1}')"
  if [[ "${actual}" != "${PROGRAM_SHA256}" ]]; then
    log "program_match=FAIL"
    fail=1
  else
    log "program_match=ok"
  fi
  actual="$(sha256sum "${OUT}/weights.json" | awk '{print $1}')"
  if [[ "${actual}" != "${WEIGHTS_SHA256}" ]]; then
    log "weights_match=FAIL"
    fail=1
  else
    log "weights_match=ok"
  fi
  actual="$(sha256sum "${OUT}/release.json" | awk '{print $1}')"
  if [[ "${actual}" != "${RELEASE_SHA256}" ]]; then
    log "release_match=FAIL"
    fail=1
  else
    log "release_match=ok"
  fi
  if [[ "${fail}" -ne 0 ]]; then
    log "t2=FAIL"
    log "t2_block=owner_drop_c1_mismatch"
    echo "owner-drop C1 bytes do not match pinned generations" >&2
    exit 1
  fi
fi

if [[ "${got_manifest}" -eq 1 ]]; then
  got_sha="$(awk -F= '/^manifest.json_sha256=/{print $2; exit}' "${EVIDENCE}")"
  got_bytes="$(awk -F= '/^manifest.json_bytes=/{print $2; exit}' "${EVIDENCE}")"
  if [[ "${got_sha}" == "${COMPILED_MANIFEST_SHA256}" && "${got_bytes}" == "${COMPILED_MANIFEST_BYTES}" ]]; then
    log "manifest_pin=match"
  else
    log "manifest_pin=mismatch"
    log "manifest_pin_note=downloaded digest is not a trust anchor; compiled ExpectedManifestSHA256 unchanged"
  fi
fi

# Full T2 oracle needs hash-vectors + decision-vectors. Owner-drop lists only
# manifest+C1 artifacts; do not invent corpus bytes.
corpus=0
for name in hash-vectors.json decision-vectors.json; do
  if echo "${LIST}" | grep -q "/${name}$"; then
    copy_one "${name}" || true
    corpus=1
  fi
done
if [[ "${corpus}" -eq 0 ]]; then
  log "corpus_vectors=absent"
  log "t2=PARTIAL"
  log "t2_block=corpus_not_in_owner_drop"
  echo "owner-drop has no corpus vectors; T2 stays PARTIAL (no self-oracle)" >&2
  exit 0
fi

log "t2=PARTIAL"
log "t2_block=need_independent_manifest_pin_and_open"
echo "owner-drop fetched; T2 not raised without independent manifest pin + vectors"
