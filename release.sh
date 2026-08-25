#!/usr/bin/env bash
# Compiles binaries locally, tags the code, pushes commits & tags, and creates a GitHub Release uploading the assets.
#
# Usage:
#   ./release.sh v0.1.0

set -euo pipefail

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"

VERSION="${1:-}"
FORCE=false
if [[ "${2:-}" == "-f" ]] || [[ "${2:-}" == "--force" ]] || [[ "${1:-}" == "-f" ]] || [[ "${1:-}" == "--force" ]]; then
  FORCE=true
  if [[ "$VERSION" == "-f" ]] || [[ "$VERSION" == "--force" ]]; then
    VERSION="${2:-}"
  fi
fi

# 1. Validate version format (e.g., v1.0.0 or v1.0.0-beta.1)
VERSION_REGEX='^v[0-9]+\.[0-9]+\.[0-9]+(-[a-zA-Z0-9.]+)?$'
if [[ -z "$VERSION" ]] || ! [[ "$VERSION" =~ $VERSION_REGEX ]]; then
  echo -e "\033[0;31mError: La versión debe tener el formato vX.Y.Z (ej. v1.0.0)\033[0m" >&2
  echo "Uso: $0 vX.Y.Z [-f|--force]" >&2
  exit 1
fi

# 2. Check for Git repository
if [ ! -d "${SCRIPT_DIR}/.git" ]; then
  echo -e "\033[0;31mError: Este script debe ser ejecutado en la raíz del repositorio de Git.\033[0m" >&2
  exit 1
fi

# 3. Check for uncommitted changes
if [ -n "$(git status --porcelain)" ]; then
  echo -e "\033[0;31mError: Hay cambios locales sin commitear en el repositorio:\033[0m" >&2
  git status --porcelain
  echo -e "\033[0;31mPor favor, haz commit o stash antes de continuar.\033[0m" >&2
  exit 1
fi

# 4. Get current branch
BRANCH="$(git branch --show-current | tr -d '[:space:]')"
if [ -z "$BRANCH" ]; then
  echo -e "\033[0;31mError: No se pudo determinar la rama actual (¿estás en estado HEAD separado?).\033[0m" >&2
  exit 1
fi

# 5. Check if tag already exists locally
OVERRIDE_TAG=false
if git tag -l "$VERSION" | grep -q "^${VERSION}$"; then
  if [ "$FORCE" = true ]; then
    OVERRIDE_TAG=true
    echo -e "\033[0;33mAviso: El tag '$VERSION' ya existe. Se sobrescribirá (--force activo).\033[0m"
  else
    echo -e "\033[0;33mAviso: El tag '$VERSION' ya existe localmente.\033[0m"
    read -r -p "¿Deseas sobrescribir / pisar el tag existente y reemplazar el Release en GitHub? (y/n): " OVERRIDE_CONFIRM
    case "$OVERRIDE_CONFIRM" in
      y|Y|si|Si|SI|yes|Yes|YES)
        OVERRIDE_TAG=true
        ;;
      *)
        echo -e "\033[0;31mOperación cancelada.\033[0m" >&2
        exit 1
        ;;
    esac
  fi
fi

# 6. Parse Owner and Repo from remote origin URL
REMOTE_URL="$(git remote get-url origin | tr -d '[:space:]')"
REMOTE_REGEX='github\.com[:/]([^/]+)/([^/]+)'
if [[ "$REMOTE_URL" =~ $REMOTE_REGEX ]]; then
  OWNER="${BASH_REMATCH[1]}"
  REPO="${BASH_REMATCH[2]%.git}"
else
  echo -e "\033[0;31mError: No se pudo determinar el propietario/repositorio de GitHub desde la URL de remote origin: $REMOTE_URL\033[0m" >&2
  exit 1
fi

# 7. Get GitHub Personal Access Token (PAT)
TOKEN="${GITHUB_TOKEN:-}"
if [ -z "$TOKEN" ]; then
  echo -e "\033[0;33mGitHub Personal Access Token (GITHUB_TOKEN) no detectado en el entorno.\033[0m"
  read -s -r -p "Por favor, introduce tu Token de GitHub (con permisos de lectura/escritura de releases): " TOKEN
  echo
  if [ -z "$TOKEN" ]; then
    echo -e "\033[0;31mError: Se requiere un token de GitHub para subir el release.\033[0m" >&2
    exit 1
  fi
fi

echo -e "\033[0;36m=============================================\033[0m"
echo -e "\033[0;36m   PREPARANDO LANZAMIENTO LOCAL DE VERSION   \033[0m"
echo -e "\033[0;36m=============================================\033[0m"
echo "Versión:        $VERSION"
if [ "$OVERRIDE_TAG" = true ]; then
  echo "Modo:           PISAR / REEMPLAZAR RELEASE EXISTENTE"
fi
echo "Repositorio:    $OWNER/$REPO"
echo "Rama origen:    $BRANCH"
echo -e "\033[0;36m=============================================\033[0m"
echo

read -r -p "¿Quieres continuar con la compilación local y subida del Release a GitHub? (y/n): " CONFIRMATION
case "$CONFIRMATION" in
  y|Y|si|Si|SI|yes|Yes|YES) ;;
  *)
    echo -e "\033[0;33mOperación cancelada.\033[0m"
    exit 0
    ;;
esac

# 8. Run local compilation and packaging
echo
echo -e "\033[0;36m[1/4] Compilando binarios locales multiplataforma...\033[0m"
BUILD_SCRIPT="${SCRIPT_DIR}/build-all.sh"
if [ ! -f "$BUILD_SCRIPT" ]; then
  echo -e "\033[0;31mError: No se encontró el script de compilación local '$BUILD_SCRIPT'.\033[0m" >&2
  exit 1
fi

chmod +x "$BUILD_SCRIPT"
"$BUILD_SCRIPT" "$VERSION"

# 9. Create Git tag and push commits/tags
echo
echo -e "\033[0;36m[2/4] Creando tag local y empujando a GitHub...\033[0m"
if [ "$OVERRIDE_TAG" = true ]; then
  git tag -f "$VERSION"
else
  git tag "$VERSION"
fi

cleanup_tag() {
  if [ "$OVERRIDE_TAG" = false ]; then
    echo -e "\033[0;31mError durante operaciones de Git. Borrando tag local $VERSION...\033[0m" >&2
    git tag -d "$VERSION" >/dev/null 2>&1 || true
  fi
}
trap cleanup_tag ERR

echo "Subiendo commits de la rama '$BRANCH' a origin..."
git push origin "$BRANCH"

echo "Subiendo tag '$VERSION' a origin..."
if [ "$OVERRIDE_TAG" = true ]; then
  git push origin --force "$VERSION"
else
  git push origin "$VERSION"
fi

trap - ERR

# 10. Create Release in GitHub via API
echo
echo -e "\033[0;36m[3/4] Gestionando Release en la API de GitHub...\033[0m"

# If overriding, delete existing release first
if [ "$OVERRIDE_TAG" = true ]; then
  EXISTING_REL_ID=$(curl -sSL \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    "https://api.github.com/repos/${OWNER}/${REPO}/releases/tags/${VERSION}" | grep -o '"id": *[0-9]*' | head -n 1 | awk '{print $2}' || true)
  if [ -n "$EXISTING_REL_ID" ]; then
    echo -e "\033[0;33mEliminando release anterior en GitHub (ID: ${EXISTING_REL_ID})...\033[0m"
    curl -sSL -X DELETE \
      -H "Authorization: Bearer ${TOKEN}" \
      -H "Accept: application/vnd.github+json" \
      -H "X-GitHub-Api-Version: 2022-11-28" \
      "https://api.github.com/repos/${OWNER}/${REPO}/releases/${EXISTING_REL_ID}" >/dev/null 2>&1 || true
    sleep 1
  fi
fi

RELEASE_PAYLOAD=$(cat <<EOF
{
  "tag_name": "${VERSION}",
  "target_commitish": "${BRANCH}",
  "name": "Release ${VERSION}",
  "body": "Release ${VERSION} generado y subido automáticamente por release.sh",
  "draft": false,
  "prerelease": false,
  "generate_release_notes": true
}
EOF
)

RELEASE_RESP=$(curl -sSL -X POST \
  -H "Authorization: Bearer ${TOKEN}" \
  -H "Accept: application/vnd.github+json" \
  -H "X-GitHub-Api-Version: 2022-11-28" \
  -H "Content-Type: application/json" \
  "https://api.github.com/repos/${OWNER}/${REPO}/releases" \
  -d "${RELEASE_PAYLOAD}")

HTML_URL=$(echo "$RELEASE_RESP" | grep -o '"html_url": *"[^"]*"' | head -n 1 | cut -d'"' -f4 || true)
UPLOAD_URL_TMPL=$(echo "$RELEASE_RESP" | grep -o '"upload_url": *"[^"]*"' | head -n 1 | cut -d'"' -f4 || true)

if [ -z "$HTML_URL" ] || [ -z "$UPLOAD_URL_TMPL" ]; then
  echo -e "\033[0;31mError al crear el release en GitHub:\033[0m" >&2
  echo "$RELEASE_RESP" >&2
  echo -e "\033[0;33mPor favor, borra el tag de git remoto si deseas reintentar: git push origin --delete $VERSION\033[0m"
  exit 1
fi

UPLOAD_URL_BASE="${UPLOAD_URL_TMPL%\{*\}}"

echo -e "\033[0;32mRelease creado exitosamente en GitHub: ${HTML_URL}\033[0m"

# 11. Upload packages as release assets
echo
echo -e "\033[0;36m[4/4] Subiendo binarios empaquetados como assets...\033[0m"
DIST_DIR="${SCRIPT_DIR}/dist"
ASSETS=("${DIST_DIR}"/*)

if [ ${#ASSETS[@]} -eq 0 ] || [ ! -e "${ASSETS[0]}" ]; then
  echo -e "\033[0;31mError: No se encontraron archivos empaquetados en la carpeta dist/.\033[0m" >&2
  exit 1
fi

for asset_path in "${DIST_DIR}"/*; do
  [ -f "$asset_path" ] || continue
  file_name="$(basename "$asset_path")"
  echo -e "\033[0;36mSubiendo asset: ${file_name}...\033[0m"

  UPLOAD_URL="${UPLOAD_URL_BASE}?name=${file_name}"

  CONTENT_TYPE="application/octet-stream"
  if [[ "$file_name" == *.zip ]]; then
    CONTENT_TYPE="application/zip"
  elif [[ "$file_name" == *.tar.gz ]]; then
    CONTENT_TYPE="application/gzip"
  fi

  UPLOAD_RESP=$(curl -sSL -X POST \
    -H "Authorization: Bearer ${TOKEN}" \
    -H "Accept: application/vnd.github+json" \
    -H "X-GitHub-Api-Version: 2022-11-28" \
    -H "Content-Type: ${CONTENT_TYPE}" \
    --data-binary "@${asset_path}" \
    "${UPLOAD_URL}")

  if echo "$UPLOAD_RESP" | grep -q '"state": *"uploaded"'; then
    echo -e "\033[0;32m¡Subida exitosa: ${file_name}!\033[0m"
  else
    echo -e "\033[0;33mResultado de subida de ${file_name}:\033[0m"
    echo "$UPLOAD_RESP"
  fi
done

echo
echo -e "\033[0;32m¡Proceso de lanzamiento completado!\033[0m"
echo -e "\033[0;32mTu release ya está disponible en: ${HTML_URL}\033[0m"
