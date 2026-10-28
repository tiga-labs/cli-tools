#!/usr/bin/env sh
# One-line installer for tiga-labs CLI tools: `enrich` (the default) or `signals`.
# Downloads a release binary from GitHub Releases and verifies its SHA-256 checksum.
#
# Usage:
#   curl -fsSL https://github.com/tiga-labs/cli-tools/releases/latest/download/install.sh | sh
#   curl -fsSL .../install.sh | TOOL=signals sh
#   curl -fsSL .../install.sh | TOOL=enrich VERSION=v0.2.0 INSTALL_DIR="$HOME/.local/bin" sh

set -eu

TOOL="${TOOL:-enrich}"
case "$TOOL" in
  enrich|signals) ;;
  *)
    echo "Unknown tool: $TOOL (expected enrich or signals)" >&2
    exit 1
    ;;
esac
INSTALL_DIR="${INSTALL_DIR:-/usr/local/bin}"
REPO="${REPO:-tiga-labs/cli-tools}"
VERSION="${VERSION:-latest}"

if [ -n "${BASE_URL:-}" ]; then
  : # explicit mirror / local test location
elif [ "$VERSION" = "latest" ]; then
  BASE_URL="https://github.com/${REPO}/releases/latest/download"
else
  BASE_URL="https://github.com/${REPO}/releases/download/${VERSION}"
fi

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

if command -v sha256sum >/dev/null 2>&1; then
  sha256() { sha256sum "$1" | awk '{print $1}'; }
elif command -v shasum >/dev/null 2>&1; then
  sha256() { shasum -a 256 "$1" | awk '{print $1}'; }
else
  echo "Neither sha256sum nor shasum is available; cannot verify download" >&2
  exit 1
fi

BINARY_NAME="${TOOL}-${OS}-${ARCH}"
TMP_DIR="$(mktemp -d)"
trap 'rm -rf "$TMP_DIR"' EXIT

echo "Downloading ${TOOL} ${VERSION} (${OS}/${ARCH}) from ${BASE_URL}..."
curl -fsSL "${BASE_URL}/${BINARY_NAME}" -o "${TMP_DIR}/${BINARY_NAME}"
curl -fsSL "${BASE_URL}/checksums.txt" -o "${TMP_DIR}/checksums.txt"

EXPECTED="$(awk -v f="$BINARY_NAME" '$2 == f {print $1}' "${TMP_DIR}/checksums.txt")"
if [ -z "$EXPECTED" ]; then
  echo "No checksum for ${BINARY_NAME} in checksums.txt" >&2
  exit 1
fi
ACTUAL="$(sha256 "${TMP_DIR}/${BINARY_NAME}")"
if [ "$EXPECTED" != "$ACTUAL" ]; then
  echo "Checksum mismatch for ${BINARY_NAME}: expected ${EXPECTED}, got ${ACTUAL}" >&2
  exit 1
fi
echo "Checksum verified."

chmod +x "${TMP_DIR}/${BINARY_NAME}"
if [ -w "$INSTALL_DIR" ]; then
  mv "${TMP_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/${TOOL}"
else
  sudo mv "${TMP_DIR}/${BINARY_NAME}" "${INSTALL_DIR}/${TOOL}"
  sudo chmod 755 "${INSTALL_DIR}/${TOOL}"
fi

if [ "$OS" = "darwin" ]; then
  xattr -d com.apple.quarantine "${INSTALL_DIR}/${TOOL}" 2>/dev/null || \
    sudo xattr -d com.apple.quarantine "${INSTALL_DIR}/${TOOL}" 2>/dev/null || true
fi

echo "Installed ${TOOL} to ${INSTALL_DIR}/${TOOL}"
"${INSTALL_DIR}/${TOOL}" --version || true
