#!/usr/bin/env bash
# Cross-compiles release binaries (web client embedded) into dist/.
set -euo pipefail
cd "$(dirname "$0")/.."
VERSION="${VERSION:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
if [ ! -f web/dist/index.html ]; then
  (cd web && npm ci --no-audit --no-fund && npm run build)
fi
rm -rf dist && mkdir -p dist
for target in windows/amd64 linux/amd64 linux/arm64 darwin/arm64; do
  os="${target%/*}"; arch="${target#*/}"
  name="homestreaming-${os}-${arch}"
  ext=""; [ "$os" = windows ] && ext=".exe"
  echo "building ${name}${ext}"
  CGO_ENABLED=0 GOOS="$os" GOARCH="$arch" go build -trimpath -ldflags "-s -w -X main.version=${VERSION}" -o "dist/${name}${ext}" ./cmd/homestreaming
  pkg="dist/pkg-${name}"
  mkdir -p "$pkg/deploy"
  cp "dist/${name}${ext}" "$pkg/homestreaming${ext}"
  cp README.md "$pkg/"
  cp deploy/*.md "$pkg/deploy/" 2>/dev/null || true
  if [ "$os" = windows ]; then
    (cd "$pkg" && zip -qr "../${name}.zip" .)
  else
    tar -czf "dist/${name}.tar.gz" -C "$pkg" .
  fi
  rm -rf "$pkg"
done
ls -lh dist
