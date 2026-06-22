#!/usr/bin/env bash
# Build ollama-manager on macOS (native arch by default).
#
# Usage:
#   ./build-mac.sh
#   ./build-mac.sh -a amd64
#   ./build-mac.sh -o ./bin/ollama-manager
#   ./build-mac.sh -a arm64 -o dist/ollama-manager-macos

set -euo pipefail

default_arch() {
  case "$(uname -m)" in
    arm64) echo "arm64" ;;
    x86_64) echo "amd64" ;;
    *)
      echo "Error: unsupported macOS architecture: $(uname -m)" >&2
      exit 1
      ;;
  esac
}

ARCH="$(default_arch)"
OUTPUT="ollama-manager"

usage() {
  cat <<'EOF'
Usage: ./build-mac.sh [-a arm64|amd64] [-o output-path]

Options:
  -a ARCH    Target architecture (default: native)
  -o PATH    Output binary path (default: ollama-manager)
  -h         Show this help
EOF
}

while getopts ":a:o:h" opt; do
  case "$opt" in
    a) ARCH="$OPTARG" ;;
    o) OUTPUT="$OPTARG" ;;
    h)
      usage
      exit 0
      ;;
    *)
      usage >&2
      exit 1
      ;;
  esac
done

case "$ARCH" in
  arm64|amd64) ;;
  *)
    echo "Error: unsupported architecture '$ARCH' (use arm64 or amd64)." >&2
    exit 1
    ;;
esac

if ! command -v go >/dev/null 2>&1; then
  echo "Error: Go is not installed or not in PATH." >&2
  exit 1
fi

BUILD_TIME="$(date '+%Y-%m-%d %H:%M:%S')"
LDFLAGS="-s -w -X 'main.buildTime=${BUILD_TIME}'"

export CGO_ENABLED=0
export GOOS=darwin
export GOARCH="$ARCH"

echo "Building ollama-manager for macOS (${ARCH})..."
echo "  GOOS    = ${GOOS}"
echo "  GOARCH  = ${GOARCH}"
echo "  Output  = ${OUTPUT}"
echo "  LDFLAGS = ${LDFLAGS}"
echo

go build -trimpath -ldflags "${LDFLAGS}" -o "${OUTPUT}" .

echo "Build succeeded: ${OUTPUT}"
