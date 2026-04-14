#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
SOURCE_TAR="${ROOT_DIR}/layers/ubuntu-rootfs.tar"
STORE_ROOT="${HOME}/.docksmith"
LAYERS_DIR="${STORE_ROOT}/layers"
IMAGES_DIR="${STORE_ROOT}/images/ubuntu"
MANIFEST_PATH="${IMAGES_DIR}/latest.json"

if [[ ! -f "${SOURCE_TAR}" ]]; then
  echo "base tar not found: ${SOURCE_TAR}" >&2
  exit 1
fi

mkdir -p "${LAYERS_DIR}" "${IMAGES_DIR}"

LAYER_HEX="$(sha256sum "${SOURCE_TAR}" | awk '{print $1}')"
LAYER_DIGEST="sha256:${LAYER_HEX}"
LAYER_PATH="${LAYERS_DIR}/${LAYER_DIGEST}.tar"
LAYER_SIZE="$(stat -c %s "${SOURCE_TAR}")"
CREATED_AT="$(date -u +%Y-%m-%dT%H:%M:%SZ)"

if [[ -f "${MANIFEST_PATH}" ]]; then
  EXISTING_CREATED="$(python3 - "${MANIFEST_PATH}" <<'PY'
import json
import sys

with open(sys.argv[1], "r", encoding="utf-8") as fh:
    print(json.load(fh).get("created", ""))
PY
)"
  if [[ -n "${EXISTING_CREATED}" ]]; then
    CREATED_AT="${EXISTING_CREATED}"
  fi
fi

cp "${SOURCE_TAR}" "${LAYER_PATH}"

python3 - "${MANIFEST_PATH}" "${LAYER_DIGEST}" "${LAYER_SIZE}" "${CREATED_AT}" <<'PY'
import hashlib
import json
import sys

manifest_path, layer_digest, layer_size, created_at = sys.argv[1:5]

manifest = {
    "name": "ubuntu",
    "tag": "latest",
    "digest": "",
    "created": created_at,
    "config": {
        "Env": [],
        "Cmd": ["/bin/sh"],
        "WorkingDir": "/",
    },
    "layers": [
        {
            "digest": layer_digest,
            "size": int(layer_size),
            "createdBy": "import ubuntu-rootfs.tar",
        }
    ],
}

canonical = json.dumps(manifest, separators=(",", ":")).encode("utf-8")
manifest["digest"] = "sha256:" + hashlib.sha256(canonical).hexdigest()

with open(manifest_path, "w", encoding="utf-8") as fh:
    json.dump(manifest, fh, indent=2)
    fh.write("\n")
PY

echo "Imported ubuntu:latest -> ${LAYER_DIGEST}"
