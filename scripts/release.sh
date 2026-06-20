#!/bin/bash
# Release build: cross-compile for all platforms, generate checksums.
set -euo pipefail
cd "$(dirname "$0")/.."
make cross
cd dist && sha256sum * > SHA256SUMS && cd ..
echo "Release artifacts in dist/"
ls -lh dist/
