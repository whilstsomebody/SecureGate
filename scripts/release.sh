#!/usr/bin/env bash
set -euo pipefail

ROOT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")/.." && pwd)"
cd "$ROOT_DIR"

VERSION="${1:-v0.2.0}"
COMMIT=$(git rev-parse --short HEAD 2>/dev/null || echo "unknown")
DATE=$(date -u +"%Y-%m-%dT%H:%M:%SZ")

OUT_DIR="bin/release"
mkdir -p "$OUT_DIR"

LDFLAGS="-s -w \
  -X github.com/whilstsomebody/securegate/internal/version.Version=${VERSION} \
  -X github.com/whilstsomebody/securegate/internal/version.Commit=${COMMIT} \
  -X github.com/whilstsomebody/securegate/internal/version.BuildDate=${DATE}"

TARGETS=(
  "linux/amd64"
  "linux/arm64"
  "darwin/amd64"
  "darwin/arm64"
)

echo "Building SecureGate ${VERSION} (commit: ${COMMIT}, date: ${DATE})..."

for target in "${TARGETS[@]}"; do
  GOOS="${target%/*}"
  GOARCH="${target#*/}"
  OUTPUT_NAME="securegate-${VERSION}-${GOOS}-${GOARCH}"
  OUTPUT_PATH="${OUT_DIR}/${OUTPUT_NAME}"

  echo "  -> Compiling ${GOOS}/${GOARCH}..."
  CGO_ENABLED=0 GOOS="$GOOS" GOARCH="$GOARCH" go build -trimpath -ldflags "$LDFLAGS" -o "$OUTPUT_PATH" ./cmd/gateway
done

echo "Generating SHA256 checksums..."
(
  cd "$OUT_DIR"
  if command -v shasum >/dev/null 2>&1; then
    shasum -a 256 securegate-* > SHA256SUMS
  else
    sha256sum securegate-* > SHA256SUMS
  fi
)

echo ""
echo "Release build complete in ${OUT_DIR}:"
ls -la "$OUT_DIR"
echo ""
cat "$OUT_DIR/SHA256SUMS"
