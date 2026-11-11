#!/usr/bin/env bash
# Fetch pinned swagger-ui-dist assets into web/swagger/ so Swagger UI works fully offline (ADR-008).
# Run once when bumping the version; the downloaded files are committed.
set -euo pipefail

VERSION="${SWAGGER_UI_VERSION:-5.17.14}"
DEST="$(cd "$(dirname "$0")/.." && pwd)/web/swagger"
BASE="https://cdn.jsdelivr.net/npm/swagger-ui-dist@${VERSION}"
FILES=(swagger-ui.css swagger-ui-bundle.js)

mkdir -p "$DEST"
for f in "${FILES[@]}"; do
  echo "fetching ${f} (${VERSION})"
  curl -fsSL --retry 3 -o "${DEST}/${f}.tmp" "${BASE}/${f}"
  mv "${DEST}/${f}.tmp" "${DEST}/${f}"
done

{
  echo "swagger-ui-dist ${VERSION}"
  (cd "$DEST" && shasum -a 256 "${FILES[@]}")
} > "${DEST}/VERSION"
cat "${DEST}/VERSION"
