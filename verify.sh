#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
cd "${ROOT_DIR}"

BINARY="${ROOT_DIR}/docksmith"
LEAK_PATH="/work/docksmith-faculty-leak.txt"
CACHE_INDEX="${HOME}/.docksmith/cache/index.json"
MYAPP_MANIFEST="${HOME}/.docksmith/images/myapp/latest.json"
ENV_MANIFEST="${HOME}/.docksmith/images/envdemo/latest.json"
WRITECHECK_MANIFEST="${HOME}/.docksmith/images/writecheck/latest.json"

echo "=== Docksmith Linux Verification ==="
echo "[1] Go toolchain"
go version

echo
echo "[2] Tests"
go test ./...

echo
echo "[3] Build binary"
go build -o "${BINARY}" ./

echo
echo "[4] Import bundled offline base image"
bash "${ROOT_DIR}/scripts/import-base.sh"

echo
echo "[5] Cold and warm build cache demo"
rm -f "${CACHE_INDEX}" "${MYAPP_MANIFEST}" "${ENV_MANIFEST}" "${WRITECHECK_MANIFEST}"
"${BINARY}" build -t myapp:latest ./examples/hello-basic
"${BINARY}" build -t myapp:latest ./examples/hello-basic

echo
echo "[6] Runtime demo"
"${BINARY}" run myapp:latest

echo
echo "[7] Runtime env override demo"
"${BINARY}" build -t envdemo:latest ./examples/env-override
"${BINARY}" run -e TARGET=faculty envdemo:latest

echo
echo "[8] Isolation write-check demo"
rm -f "${LEAK_PATH}"
"${BINARY}" build -t writecheck:latest ./examples/write-check
"${BINARY}" run -e OUTPUT_FILE="${LEAK_PATH}" writecheck:latest
if [[ -e "${LEAK_PATH}" ]]; then
  echo "FAIL: ${LEAK_PATH} leaked onto the host" >&2
  exit 1
fi
echo "PASS: ${LEAK_PATH} was not created on the host"

echo
echo "[9] Images listing"
"${BINARY}" images

echo
echo "[10] Image removal"
"${BINARY}" rmi myapp:latest

echo
echo "=== VERIFICATION COMPLETE ==="
