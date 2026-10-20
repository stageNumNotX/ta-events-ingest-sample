#!/usr/bin/env bash
# Download the generation-pinned producer pointer. Verify size and sha256
# when that generation still exists. A retired generation is observed via
# latest only — never rewrite POINTER_* / DefaultPointerPin (no self-oracle).
# Dump keys and pin generations only — never the object body.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
# shellcheck source=traffic-runtime-env.sh
source "${ROOT}/scripts/traffic-runtime-env.sh"
# shellcheck source=traffic-runtime-pins.env
source "${ROOT}/scripts/traffic-runtime-pins.env"
export PATH="${HOME}/google-cloud-sdk/bin:${PATH}"

OUT="${POINTER_OUT:-${ROOT}/docs/traffic-runtime/evidence/pointer}"
mkdir -p "${OUT}"
DEST="${OUT}/prototype.json"
EVIDENCE="${ROOT}/docs/traffic-runtime/evidence/T4_POINTER.txt"
URI="gs://${BUCKET}/${POINTER_OBJECT}#${POINTER_GENERATION}"

{
  echo "utc=$(date -u +%Y-%m-%dT%H:%M:%SZ)"
  echo "bucket=${BUCKET}"
  echo "object=${POINTER_OBJECT}"
  echo "compiled_generation=${POINTER_GENERATION}"
  echo "compiled_size=${POINTER_SIZE}"
  echo "compiled_sha256=${POINTER_SHA256}"
  echo "self_oracle=no"
  echo "uri=${URI}"
} > "${EVIDENCE}"

err="$(mktemp)"
COMPILED_OK=no
if gcloud storage cp "${URI}" "${DEST}" --quiet >/dev/null 2>"${err}"; then
  COMPILED_OK=yes
  echo "compiled_generation_readable=yes" >> "${EVIDENCE}"
else
  echo "compiled_generation_readable=no" >> "${EVIDENCE}"
  echo "compiled_fetch_error=$(tr '\n' ' ' <"${err}" | head -c 240)" >> "${EVIDENCE}"
  # Old generation can disappear after pointer overwrite. Observe latest
  # only — do not rewrite POINTER_* pins (no self-oracle).
  LATEST_URI="gs://${BUCKET}/${POINTER_OBJECT}"
  if ! gcloud storage cp "${LATEST_URI}" "${DEST}" --quiet >/dev/null 2>"${err}"; then
    echo "FAILED pointer download compiled ${URI} and latest ${LATEST_URI}" >&2
    cat "${err}" >&2 || true
    rm -f "${err}"
    echo "fetch=FAIL" >> "${EVIDENCE}"
    echo "t4=BLOCKED" >> "${EVIDENCE}"
    echo "t4_block=pointer_unreadable" >> "${EVIDENCE}"
    exit 1
  fi
  echo "latest_observed=yes" >> "${EVIDENCE}"
  echo "self_oracle=no" >> "${EVIDENCE}"
  echo "compiled_pin_unchanged=yes" >> "${EVIDENCE}"
  if obs_gen="$(gcloud storage objects describe "${LATEST_URI}" --format='value(generation)' --quiet 2>/dev/null)"; then
    echo "observed_generation=${obs_gen}" >> "${EVIDENCE}"
  fi
fi
rm -f "${err}"

actual_size="$(wc -c < "${DEST}" | tr -d ' ')"
actual_sha="$(sha256sum "${DEST}" | awk '{print $1}')"
if [[ "${COMPILED_OK}" == "yes" ]]; then
  if [[ "${actual_size}" != "${POINTER_SIZE}" ]]; then
    echo "FAILED pointer size got=${actual_size} want=${POINTER_SIZE}" >&2
    echo "fetch=FAIL" >> "${EVIDENCE}"
    echo "got_size=${actual_size}" >> "${EVIDENCE}"
    echo "t4=FAIL" >> "${EVIDENCE}"
    echo "t4_block=pointer_size_mismatch" >> "${EVIDENCE}"
    exit 1
  fi
  if [[ "${actual_sha}" != "${POINTER_SHA256}" ]]; then
    echo "FAILED pointer sha256 got=${actual_sha} want=${POINTER_SHA256}" >&2
    echo "fetch=FAIL" >> "${EVIDENCE}"
    echo "got_sha256=${actual_sha}" >> "${EVIDENCE}"
    echo "t4=FAIL" >> "${EVIDENCE}"
    echo "t4_block=pointer_sha_mismatch_not_a_new_anchor" >> "${EVIDENCE}"
    exit 1
  fi
else
  echo "got_size=${actual_size}" >> "${EVIDENCE}"
  echo "got_sha256=${actual_sha}" >> "${EVIDENCE}"
  echo "got_sha256_is_not_a_new_compiled_pin=yes" >> "${EVIDENCE}"
fi

{
  echo "fetch=ok"
  echo "got_size=${actual_size}"
  echo "got_sha256=${actual_sha}"
} >> "${EVIDENCE}"

python3 - "${DEST}" "${EVIDENCE}" <<'PY'
import json, sys
path, evidence = sys.argv[1], sys.argv[2]
raw = open(path, "rb").read()
data = json.loads(raw.decode("utf-8"))
lines = []
if isinstance(data, dict):
    lines.append("keys=" + ",".join(sorted(data.keys())))
else:
    lines.append("keys=not_object")
    open(evidence, "a", encoding="utf-8").write("\n".join(lines) + "\n")
    sys.exit(0)

def as_obj(v):
    return v if isinstance(v, dict) else {}

def first(m, *keys):
    for k in keys:
        if k in m and m[k] not in (None, ""):
            return m[k]
    lower = {str(k).lower(): v for k, v in m.items()}
    for k in keys:
        if k.lower() in lower and lower[k.lower()] not in (None, ""):
            return lower[k.lower()]
    return None

meta = dict(data)
for nest in ("live", "pointer", "current", "target"):
    child = as_obj(data.get(nest))
    if child:
        meta.update({k: v for k, v in child.items() if k not in meta or meta[k] in (None, "")})

ver = first(meta, "version", "activationVersion", "targetVersion")
aset = first(meta, "artifactSetId", "artifact_set_id")
tid = first(meta, "targetId", "target_id")
jid = first(meta, "jobId", "job_id")
if ver is not None:
    lines.append(f"version={ver}")
if aset:
    lines.append(f"artifactSetId={aset}")
if tid:
    lines.append(f"targetId={tid}")
if jid:
    lines.append(f"jobId={jid}")

def pin_kind(s):
    s = (s or "").lower()
    if "program" in s:
        return "program"
    if "weight" in s:
        return "weights"
    if "release" in s:
        return "release"
    return ""

found = {}

def take(kind, obj):
    if not kind or not isinstance(obj, dict):
        return
    gen = first(obj, "generation", "objectGeneration", "gcsGeneration")
    sha = first(obj, "sha256", "digest", "hash")
    size = first(obj, "sizeBytes", "size", "bytes")
    if gen is not None:
        found.setdefault(kind, {})["generation"] = gen
    if sha:
        found.setdefault(kind, {})["sha256"] = str(sha).removeprefix("sha256:")
    if size is not None:
        found.setdefault(kind, {})["size"] = size

arts = data.get("artifacts")
if arts is None:
    for nest in ("live", "pointer", "current", "target"):
        arts = as_obj(data.get(nest)).get("artifacts")
        if arts is not None:
            break
if isinstance(arts, list):
    for item in arts:
        if isinstance(item, dict):
            take(pin_kind(str(first(item, "kind", "artifactType", "type") or "")), item)
for kind in ("program", "weights", "release"):
    take(kind, as_obj(data.get(kind)))
    for nest in ("live", "pointer", "pins"):
        take(kind, as_obj(as_obj(data.get(nest)).get(kind)))

for kind in ("program", "weights", "release"):
    info = found.get(kind) or {}
    if "generation" in info:
        lines.append(f"{kind}_generation={info['generation']}")
    if "sha256" in info:
        lines.append(f"{kind}_sha256={info['sha256']}")
    if "size" in info:
        lines.append(f"{kind}_size={info['size']}")

open(evidence, "a", encoding="utf-8").write("\n".join(lines) + "\n")
print("shape pointer type=object keys=" + (lines[0].split("=", 1)[1] if lines else ""))
PY

echo "fetch_note=body_not_dumped" >> "${EVIDENCE}"
if [[ "${COMPILED_OK}" == "yes" ]]; then
  echo "t4=FETCH_OK" >> "${EVIDENCE}"
else
  echo "t4=FETCH_COMPILED_MISSING_LATEST_OK" >> "${EVIDENCE}"
fi
echo "t4_formal_see=docs/traffic-runtime/evidence/T4_STATUS.txt" >> "${EVIDENCE}"
echo "OK pointer compiled_ok=${COMPILED_OK} size=${actual_size} sha256=${actual_sha}"
