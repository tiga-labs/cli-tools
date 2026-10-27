#!/usr/bin/env sh
# One-line installer for tiga-labs CLI tools (defaults to `enrich`).
# Usage:
#   curl -fsSL <BASE_URL>/install.sh | sh
#   TOOL=enrich INSTALL_DIR=/usr/local/bin curl -fsSL <BASE_URL>/install.sh | sh

set -eu

TOOL="${TOOL:-enrich}"
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
BASE_URL="${BASE_URL:-https://tiga-labs-prod.s3.amazonaws.com/cli-tools/latest}"

OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
ARCH="$(uname -m)"

case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH" >&2
    exit 1
    ;;
esac

case "$OS" in
  darwin|linux) ;;
  *)
    echo "Unsupported OS: $OS" >&2
    exit 1
    ;;
esac

BINARY_NAME="${TOOL}-${OS}-${ARCH}"
DOWNLOAD_URL="${BASE_URL}/${BINARY_NAME}"
TMP_BIN="$(mktemp)"
trap 'rm -f "$TMP_BIN"' EXIT

echo "Downloading ${TOOL} (${OS}/${ARCH}) from ${DOWNLOAD_URL}..."
curl -fsSL "$DOWNLOAD_URL" -o "$TMP_BIN"
chmod +x "$TMP_BIN"

if [ -w "$INSTALL_DIR" ]; then
  mv "$TMP_BIN" "${INSTALL_DIR}/${TOOL}"
else
  sudo mv "$TMP_BIN" "${INSTALL_DIR}/${TOOL}"
  sudo chmod 755 "${INSTALL_DIR}/${TOOL}"
fi

if [ "$OS" = "darwin" ]; then
  xattr -d com.apple.quarantine "${INSTALL_DIR}/${TOOL}" 2>/dev/null || \
    sudo xattr -d com.apple.quarantine "${INSTALL_DIR}/${TOOL}" 2>/dev/null || true
fi

echo "Installed ${TOOL} to ${INSTALL_DIR}/${TOOL}"
"${INSTALL_DIR}/${TOOL}" --version || true
