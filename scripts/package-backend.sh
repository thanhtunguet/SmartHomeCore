#!/usr/bin/env bash
# Build and package the Linux backend release archive.
#
#   scripts/package-backend.sh <goarch> [version]
#
#   goarch   amd64 | arm64
#   version  defaults to `git describe --tags --always --dirty`
#
# Env: OUT_DIR (default build/dist), COMMIT, ALLOW_NO_UI=1 (package without the built frontend).
# The frontend must already be built into web/static (`make ui-build`); it is embedded in the binary.
# Output: $OUT_DIR/homecore-<version>-linux-<goarch>.tar.gz and a matching .sha256 file.
set -euo pipefail

cd "$(dirname "$0")/.."

ARCH="${1:?usage: $0 <amd64|arm64> [version]}"
VERSION="${2:-$(git describe --tags --always --dirty 2>/dev/null || echo dev)}"
COMMIT="${COMMIT:-$(git rev-parse --short HEAD 2>/dev/null || echo none)}"
OUT_DIR="${OUT_DIR:-build/dist}"
PKG="github.com/thanhtunguet/homecore"

case "$ARCH" in
  amd64) UNAME_M=x86_64 ;;
  arm64) UNAME_M=aarch64 ;;
  *) echo "unsupported goarch: $ARCH (amd64 | arm64)" >&2; exit 2 ;;
esac

if [ ! -f web/static/index.html ] && [ "${ALLOW_NO_UI:-}" != 1 ]; then
  echo "web/static/index.html missing: build the frontend first (make ui-build), or set ALLOW_NO_UI=1" >&2
  exit 1
fi

# Newest migration version, from the file names (NNNN_name.sql).
SCHEMA_VERSION="$(ls internal/storage/migrations/*.sql | sed 's#.*/##; s#_.*##' | sort | tail -n1 | sed 's/^0*//')"

NAME="homecore-${VERSION}-linux-${ARCH}"
STAGE="$(mktemp -d)"
trap 'rm -rf "$STAGE"' EXIT
ROOT="$STAGE/$NAME"
mkdir -p "$ROOT/configs" "$ROOT/deploy/systemd" "$ROOT/deploy/polkit" "$ROOT/migrations" "$OUT_DIR"

echo "building $NAME (schema $SCHEMA_VERSION)"
CGO_ENABLED=0 GOOS=linux GOARCH="$ARCH" go build -trimpath \
  -ldflags "-s -w -X $PKG/internal/version.Version=$VERSION -X $PKG/internal/version.Commit=$COMMIT" \
  -o "$ROOT/homecore" ./cmd/homecore

cp configs/homecore.example.yaml "$ROOT/configs/"
cp deploy/systemd/*.service "$ROOT/deploy/systemd/"
cp deploy/polkit/* "$ROOT/deploy/polkit/"
cp internal/storage/migrations/*.sql "$ROOT/migrations/"

sed -e "s|@VERSION@|$VERSION|g" \
    -e "s|@COMMIT@|$COMMIT|g" \
    -e "s|@GOARCH@|$ARCH|g" \
    -e "s|@UNAME_M@|$UNAME_M|g" \
    -e "s|@SCHEMA_VERSION@|$SCHEMA_VERSION|g" \
    deploy/package/README.md.tmpl > "$ROOT/README.md"
if grep -n '@[A-Z_]*@' "$ROOT/README.md"; then
  echo "unreplaced placeholder in README template" >&2
  exit 1
fi

cat > "$ROOT/VERSION" <<EOF
version=$VERSION
commit=$COMMIT
os=linux
arch=$ARCH
schema_version=$SCHEMA_VERSION
EOF

if tar --version 2>/dev/null | grep -q GNU; then
  OWNER=(--owner=0 --group=0 --numeric-owner)
else
  OWNER=(--uid 0 --gid 0)
fi
ARCHIVE="$OUT_DIR/$NAME.tar.gz"
tar -C "$STAGE" "${OWNER[@]}" -czf "$ARCHIVE" "$NAME"

if command -v sha256sum >/dev/null; then SUM=(sha256sum); else SUM=(shasum -a 256); fi
(cd "$OUT_DIR" && "${SUM[@]}" "$NAME.tar.gz" > "$NAME.tar.gz.sha256")

echo "wrote $ARCHIVE"
