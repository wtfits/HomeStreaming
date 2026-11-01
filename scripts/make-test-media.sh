#!/usr/bin/env bash
# Generates the test media tree (DESIGN §10) into the given directory.
set -euo pipefail
cd "$(dirname "$0")/.."
exec go run ./internal/testutil/maketestmedia "${1:?usage: make-test-media.sh <dir>}"
