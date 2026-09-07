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
  # On macOS, default to the native CPU. Anywhere else this script is only
  # cross-compiling *for* a Mac, so uname -m (the host CPU) is meaningless:
  # default to Apple Silicon, which is wrong nowhere near as often as Intel.
  if [ "$(uname -s)" != "Darwin" ]; then
    echo "note: cross-compiling from $(uname -s); defaulting to arm64 (Apple Silicon, M1/M2/M3/M4). Use -a amd64 for Intel Macs." >&2
    echo "arm64"
    return
  fi
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
VERSION=""

usage() {
  cat <<'EOF'
Usage: ./build-mac.sh [-a arm64|amd64] [-o output-path]

Options:
  -a ARCH    Target architecture (default: native)
  -o PATH    Output binary path (default: ollama-manager)
  -v VERSION Version string injected as main.appVersion (default: git describe)
  -h         Show this help
EOF
}

while getopts ":a:o:v:h" opt; do
  case "$opt" in
    a) ARCH="$OPTARG" ;;
    o) OUTPUT="$OPTARG" ;;
    v) VERSION="$OPTARG" ;;
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
if [ -z "$VERSION" ] && command -v git >/dev/null 2>&1; then
  VERSION="$(git describe --tags --abbrev=0 2>/dev/null || true)"
fi
LDFLAGS="-s -w -X 'main.buildTime=${BUILD_TIME}'"
if [ -n "$VERSION" ]; then
  LDFLAGS="${LDFLAGS} -X 'main.appVersion=${VERSION}'"
fi

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

# Verify the produced binary really targets the requested architecture.
# NOTE: when run from Git Bash/MSYS on Windows, "$(uname -m)" reports the
# *Windows* CPU (x86_64), so the default becomes Intel even though you are
# building for a Mac. On an Apple Silicon Mac without Rosetta that binary
# fails with "bad CPU type in executable" — pass -a arm64 explicitly.
if ! go version -m "${OUTPUT}" 2>/dev/null | grep -q "GOARCH=${ARCH}\b"; then
  found="$(go version -m "${OUTPUT}" 2>/dev/null | grep -o 'GOARCH=[^[:space:]]*' || echo GOARCH=unknown)"
  echo "Error: '${OUTPUT}' reports ${found}, but arch '${ARCH}' was requested. Aborting to avoid shipping the wrong CPU type." >&2
  exit 1
fi

if [ "$ARCH" = "arm64" ]; then
  echo "Target: Apple Silicon Macs (M1/M2/M3/M4). Will NOT run on Intel Macs."
else
  echo "WARNING: Intel build. Apple Silicon Macs need Rosetta 2 installed or they fail with 'bad CPU type in executable'." >&2
fi
