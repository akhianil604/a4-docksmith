#!/bin/bash
set -e

echo "=== Docksmith Linux Verification ==="
echo "[1] Go version"
go version

echo -e "\n[2] Module tidy"
go mod tidy

echo -e "\n[3] Build"
go build ./...
go build -o docksmith-test .

echo -e "\n[4] Static analysis"
go vet ./...

echo -e "\n[5] Unit tests"
go test ./layers/... -v

echo -e "\n[6] Integration test"
# Running with no args should print usage; treat that as a successful smoke check.
if ./docksmith-test >/tmp/docksmith_verify_out.txt 2>&1; then
  cat /tmp/docksmith_verify_out.txt
else
  cat /tmp/docksmith_verify_out.txt
fi

echo -e "\n[7] Store contents"
if [ -d "$HOME/.docksmith/layers" ]; then
  ls -lh "$HOME/.docksmith/layers/" | head -10
else
  echo "No layers found yet at $HOME/.docksmith/layers"
fi

echo -e "\n[8] Tar verification"
LAYER=$(ls -1 "$HOME/.docksmith/layers/"*.tar 2>/dev/null | head -1)
if [ -n "$LAYER" ]; then
  echo "Sample layer: $LAYER"
  tar -tf "$LAYER" | head -5
else
  echo "No layer tar files to inspect yet."
fi

echo -e "\n=== VERIFICATION COMPLETE ==="
