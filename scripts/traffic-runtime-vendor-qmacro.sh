#!/usr/bin/env bash
# Copy producer Q-MACRO-CONTRACT evidence into testdata/traffic-contract/q-macro-contract.
# Usage: scripts/traffic-runtime-vendor-qmacro.sh /path/to/compiler-service
# Does not invent a field registry. Does not deploy.
set -euo pipefail
set +x

ROOT="$(cd "$(dirname "$0")/.." && pwd)"
SRC_ROOT="${1:?path to compiler-service checkout}"
DIGEST="eddc83fd05225ec71368f4570fce593f30a2d9bef3b8a1abc4981ad6686208f5"
COMMIT_PREFIX="39db6c0"

if [[ ! -d "${SRC_ROOT}" ]]; then
  echo "compiler-service checkout not a directory: ${SRC_ROOT}" >&2
  exit 1
fi

SRC=""
for cand in \
  "docs/control-plane/evidence/q-macro-contract" \
  "docs/control-plane/evidence/hotlink-macros"
 do
  if [[ -d "${SRC_ROOT}/${cand}" ]]; then
    SRC="${SRC_ROOT}/${cand}"
    break
  fi
done
if [[ -z "${SRC}" ]]; then
  echo "producer evidence directory not found under ${SRC_ROOT}" >&2
  echo "looked for docs/control-plane/evidence/{q-macro-contract,hotlink-macros}" >&2
  exit 1
fi

if command -v git >/dev/null && git -C "${SRC_ROOT}" rev-parse --is-inside-work-tree >/dev/null 2>&1; then
  HEAD="$(git -C "${SRC_ROOT}" rev-parse HEAD)"
  case "${HEAD}" in
    "${COMMIT_PREFIX}"*) ;;
    *)
      echo "warning: producer HEAD ${HEAD} does not start with ${COMMIT_PREFIX}" >&2
      ;;
  esac
fi

DEST="${ROOT}/testdata/traffic-contract/q-macro-contract"
mkdir -p "$(dirname "${DEST}")"
rm -rf "${DEST}"
mkdir -p "${DEST}"
cp -a "${SRC}/." "${DEST}/"

if [[ ! -f "${DEST}/DIGEST" && ! -f "${DEST}/DIGEST.txt" ]]; then
  echo "vendored package has no DIGEST file" >&2
  exit 1
fi

echo "vendored ${SRC} -> ${DEST}"
echo "oracleDigest=${DIGEST}"
echo "next: go test ./internal/qmacrocontract ./internal/decision -count=1"
