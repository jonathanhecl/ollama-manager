#!/usr/bin/env bash
# Compiles and packages ollama-manager for all major operating systems and architectures.
#
# Usage:
#   ./build-all.sh
#   ./build-all.sh v1.0.0
#   ./build-all.sh -v v1.0.0

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIST_DIR="${SCRIPT_DIR}/dist"

VERSION=""
if [ $# -gt 0 ]; then
  if [ "$1" = "-v" ] || [ "$1" = "--version" ]; then
    VERSION="${2:-}"
  else
    VERSION="$1"
  fi
fi

if ! command -v go >/dev/null 2>&1; then
  echo "Error: Go no está instalado o no está en el PATH." >&2
  exit 1
fi

rm -rf "${DIST_DIR}"
mkdir -p "${DIST_DIR}"

BUILD_TIME="$(date '+%Y-%m-%d %H:%M:%S')"
LDFLAGS="-s -w -X 'main.buildTime=${BUILD_TIME}'"

# Matrix of targets: OS ARCH FORMAT
TARGETS=(
  "windows amd64 zip"
  "windows arm64 zip"
  "linux amd64 targz"
  "linux arm64 targz"
  "darwin amd64 targz"
  "darwin arm64 targz"
)

echo "Iniciando compilación multiplataforma..."

for target in "${TARGETS[@]}"; do
  read -r os arch format <<< "$target"

  binary_name="ollama-manager"
  if [ "$os" = "windows" ]; then
    binary_name="ollama-manager.exe"
  fi

  pack_base="ollama-manager"
  if [ -n "$VERSION" ]; then
    pack_base="${pack_base}-${VERSION}"
  fi

  pack_os="$os"
  pack_arch="$arch"
  if [ "$os" = "darwin" ]; then
    pack_os="apple"
    if [ "$arch" = "arm64" ]; then
      pack_arch="silicon"
    else
      pack_arch="intel"
    fi
  fi

  pack_name="${pack_base}-${pack_os}-${pack_arch}"
  if [ "$format" = "zip" ]; then
    pack_name="${pack_name}.zip"
  else
    pack_name="${pack_name}.tar.gz"
  fi

  pack_path="${DIST_DIR}/${pack_name}"
  temp_dir="${DIST_DIR}/temp_${os}_${arch}"

  echo "Compilando para ${os} (${arch})..."
  rm -rf "${temp_dir}"
  mkdir -p "${temp_dir}"

  binary_path="${temp_dir}/${binary_name}"

  CGO_ENABLED=0 GOOS="${os}" GOARCH="${arch}" go build -trimpath -ldflags "${LDFLAGS}" -o "${binary_path}" "${SCRIPT_DIR}"

  echo "Empaquetando en ${pack_name} (binario interno: ${binary_name})..."
  if [ "$format" = "zip" ]; then
    (cd "${temp_dir}" && zip -q -9 "${pack_path}" "${binary_name}")
  else
    (cd "${temp_dir}" && tar -czf "${pack_path}" "${binary_name}")
  fi

  rm -rf "${temp_dir}"
done

echo
echo "Compilación y empaquetado completados exitosamente. Archivos en dist/:"
ls -lh "${DIST_DIR}"
