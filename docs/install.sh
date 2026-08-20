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

if [ -n "${NO_MISTAKES_VERSION:-}" ]; then
  VERSION="$NO_MISTAKES_VERSION"
else
  VERSION="$(curl -fsSL "https://api.github.com/repos/${REPO}/releases/latest" | sed -n 's/.*"tag_name"[[:space:]]*:[[:space:]]*"\([^"]*\)".*/\1/p' | head -n 1)"
fi
if [ -z "$VERSION" ]; then
  echo "Could not determine latest release"
  exit 1
fi
case "$VERSION" in
  *[!A-Za-z0-9._-]*)
    echo "Invalid release version: ${VERSION}"
    exit 1
    ;;
esac

FILENAME="no-mistakes-${VERSION}-${OS}-${ARCH}.tar.gz"
ASSET_BASE="https://github.com/${REPO}/releases/download/${VERSION}"
URL="${ASSET_BASE}/${FILENAME}"
CHECKSUMS_URL="${ASSET_BASE}/checksums.txt"

TMPDIR="$(mktemp -d)"
trap 'rm -rf "$TMPDIR"' EXIT

echo "Downloading no-mistakes ${VERSION} for ${OS}/${ARCH}..."
curl -fsSL "$URL" -o "${TMPDIR}/${FILENAME}"
curl -fsSL "$CHECKSUMS_URL" -o "${TMPDIR}/checksums.txt"

verify_archive_checksum() {
  archive="$1"
  checksums="$2"
  filename="$3"

  if [ ! -s "$checksums" ]; then
    echo "checksums.txt is missing or empty"
    exit 1
  fi

  expected="$(awk -v f="$filename" '$2 == f { print $1; found=1 } END { if (!found) exit 1 }' "$checksums")" || {
    echo "checksums.txt has no entry for ${filename}"
    exit 1
  }
  if [ -z "$expected" ]; then
    echo "checksums.txt has no entry for ${filename}"
    exit 1
  fi

  if command -v sha256sum >/dev/null 2>&1; then
    actual="$(sha256sum "$archive" | awk '{print $1}')"
  elif command -v shasum >/dev/null 2>&1; then
    actual="$(shasum -a 256 "$archive" | awk '{print $1}')"
  else
    echo "no SHA-256 tool found (sha256sum or shasum)"
    exit 1
  fi

  if [ "$actual" != "$expected" ]; then
    echo "checksum mismatch for ${filename}: got ${actual} want ${expected}"
    exit 1
  fi
}

verify_archive_checksum "${TMPDIR}/${FILENAME}" "${TMPDIR}/checksums.txt" "$FILENAME"
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
