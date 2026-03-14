#!/bin/bash
set -e

echo "=== Docksmith Linux Verification ==="
echo "[1] Go version"
go version

echo -e "\n[2] Module tidy"
go mod tidy

echo -e "\n[3] Build"
go build ./...
go build -o docksmith-test main.go

echo -e "\n[4] Static analysis"
go vet ./...

echo -e "\n[5] Unit tests"
go test ./layers/... -v

echo -e "\n[6] Integration test"
./docksmith-test

echo -e "\n[7] Store contents"
ls -lh ~/.docksmith/layers/ | head -10

echo -e "\n[8] Tar verification"
LAYER=$(ls -1 ~/.docksmith/layers/*.tar 2>/dev/null | head -1)
if [ -n "$LAYER" ]; then
  echo "Sample layer: $LAYER"
  tar -tzf "$LAYER" | head -5
fi

echo -e "\n=== VERIFICATION COMPLETE ==="
