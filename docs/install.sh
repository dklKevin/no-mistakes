#!/bin/sh
set -e

REPO="kunchenguid/no-mistakes"
INSTALL_DIR="${NO_MISTAKES_INSTALL_DIR:-$HOME/.no-mistakes/bin}"
LINK_DIR="${NO_MISTAKES_LINK_DIR:-}"

if [ -z "$LINK_DIR" ]; then
  case ":$PATH:" in
    *":$HOME/.local/bin:"*) LINK_DIR="$HOME/.local/bin" ;;
    *) LINK_DIR="/usr/local/bin" ;;
  esac
fi

BIN_PATH="$INSTALL_DIR/no-mistakes"
LINK_PATH="$LINK_DIR/no-mistakes"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$OS" in
  darwin|linux) ;;
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Pin the release tag. Scraping GitHub's moving "latest" pointer is unpinned
# and also skips prereleases, which is how 1.54/1.55 disappeared from the
# installer. Override with NO_MISTAKES_VERSION only for a specific tag or tests.
VERSION="${NO_MISTAKES_VERSION:-v1.55.0}"
case "$VERSION" in
  v[0-9]*.[0-9]*.[0-9]*)
    case "$VERSION" in
      */*|*" "*|*".."*)
        echo "Invalid version: $VERSION"
        exit 1
        ;;
    esac
    ;;
  *)
    echo "Invalid version: $VERSION (expected vMAJOR.MINOR.PATCH)"
    exit 1
    ;;
esac

FILENAME="no-mistakes-${VERSION}-${OS}-${ARCH}.tar.gz"
URL="https://github.com/${REPO}/releases/download/${VERSION}/${FILENAME}"
CHECKSUMS_URL="https://github.com/${REPO}/releases/download/${VERSION}/checksums.txt"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

echo "Downloading no-mistakes ${VERSION} for ${OS}/${ARCH}..."
curl -fsSL "$URL" -o "${TMPDIR}/${FILENAME}"

echo "Verifying checksums.txt..."
if ! curl -fsSL "$CHECKSUMS_URL" -o "${TMPDIR}/checksums.txt"; then
  echo "Failed to download checksums.txt for ${VERSION}"
  exit 1
fi
if [ ! -s "${TMPDIR}/checksums.txt" ]; then
  echo "checksums.txt for ${VERSION} is empty"
  exit 1
fi

if ! expected="$(awk -v f="$FILENAME" '
  $2 == f || $2 == ("*" f) { print $1; found=1; exit }
  END { if (!found) exit 1 }
' "${TMPDIR}/checksums.txt")"; then
  echo "checksums.txt has no SHA-256 for ${FILENAME}"
  exit 1
fi

if command -v sha256sum >/dev/null 2>&1; then
  actual="$(sha256sum "${TMPDIR}/${FILENAME}" | awk '{print $1}')"
elif command -v shasum >/dev/null 2>&1; then
  actual="$(shasum -a 256 "${TMPDIR}/${FILENAME}" | awk '{print $1}')"
else
  echo "Need sha256sum or shasum to verify checksums.txt"
  exit 1
fi

expected="$(printf '%s' "$expected" | tr '[:upper:]' '[:lower:]')"
actual="$(printf '%s' "$actual" | tr '[:upper:]' '[:lower:]')"
if [ -z "$expected" ] || [ "$actual" != "$expected" ]; then
  echo "checksum mismatch for ${FILENAME}: got ${actual} want ${expected}"
  exit 1
fi

tar xzf "${TMPDIR}/${FILENAME}" -C "$TMPDIR"

if ! mkdir -p "$INSTALL_DIR"; then
  echo "Could not create install directory: $INSTALL_DIR"
  exit 1
fi

mv "${TMPDIR}/no-mistakes" "$BIN_PATH"
chmod 755 "$BIN_PATH" 2>/dev/null || true

resolve_path() {
  (cd "$1" 2>/dev/null && pwd -P)
}

REAL_INSTALL_DIR="$(resolve_path "$INSTALL_DIR")"
REAL_LINK_DIR="$(resolve_path "$LINK_DIR" 2>/dev/null || echo "")"

if [ -n "$REAL_INSTALL_DIR" ] && [ "$REAL_INSTALL_DIR" = "$REAL_LINK_DIR" ]; then
  echo "Install dir and link dir resolve to the same path; skipping symlink."
else
  if [ -w "$LINK_DIR" ] || (mkdir -p "$LINK_DIR" 2>/dev/null && [ -w "$LINK_DIR" ]); then
    rm -f "$LINK_PATH"
    ln -s "$BIN_PATH" "$LINK_PATH"
  else
    echo "Linking ${LINK_PATH} to ${BIN_PATH} (requires sudo)..."
    sudo mkdir -p "$LINK_DIR"
    sudo rm -f "$LINK_PATH"
    sudo ln -s "$BIN_PATH" "$LINK_PATH"
  fi
fi

echo "no-mistakes ${VERSION} installed to ${BIN_PATH}"
echo "Command path: ${LINK_PATH} -> ${BIN_PATH}"

"$BIN_PATH" daemon restart >/dev/null

case ":$PATH:" in
  *":$LINK_DIR:"*) ;;
  *) echo "Add ${LINK_DIR} to your PATH and restart your terminal." ;;
esac
