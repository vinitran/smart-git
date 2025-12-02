#!/usr/bin/env bash
set -euo pipefail

REPO_OWNER="vinitran"
REPO_NAME="smart-git"
BINARY_NAME="sg"

# Detect OS
OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
  Linux)  GOOS="linux" ;;
  Darwin) GOOS="darwin" ;;
  *)
    echo "Unsupported OS: $OS"
    exit 1
    ;;
esac

case "$ARCH" in
  x86_64|amd64) GOARCH="amd64" ;;
  arm64|aarch64) GOARCH="arm64" ;;
  *)
    echo "Unsupported architecture: $ARCH"
    exit 1
    ;;
esac

SUFFIX="${GOOS}-${GOARCH}"

# Default install dir
INSTALL_DIR="${HOME}/.local/bin"
mkdir -p "${INSTALL_DIR}"

echo "📦 Detecting latest version..."
VERSION="$(curl -fsSL "https://raw.githubusercontent.com/${REPO_OWNER}/${REPO_NAME}/main/VERSION")"
if [ -z "${VERSION}" ]; then
  echo "Failed to determine latest version."
  exit 1
fi

TAG="v${VERSION}"
ASSET="sg-${SUFFIX}"

DOWNLOAD_URL="https://github.com/${REPO_OWNER}/${REPO_NAME}/releases/download/${TAG}/${ASSET}"

echo "⬇️  Downloading ${ASSET} (version ${TAG})..."
curl -fL "${DOWNLOAD_URL}" -o "${INSTALL_DIR}/${BINARY_NAME}"

chmod +x "${INSTALL_DIR}/${BINARY_NAME}"

echo ""
echo "✅ Installed ${BINARY_NAME} to ${INSTALL_DIR}/${BINARY_NAME}"

# Try to persist PATH update for zsh users.
ZSHRC="${HOME}/.zshrc"
EXPORT_LINE="export PATH=\"${INSTALL_DIR}:\$PATH\""

if [ -f "${ZSHRC}" ]; then
  if ! grep -Fq "${EXPORT_LINE}" "${ZSHRC}"; then
    echo "${EXPORT_LINE}" >> "${ZSHRC}"
    echo "🔧 Added PATH update to ${ZSHRC}:"
    echo "  ${EXPORT_LINE}"
  fi
else
  echo "${EXPORT_LINE}" >> "${ZSHRC}"
  echo "🔧 Created ${ZSHRC} and added PATH update:"
  echo "  ${EXPORT_LINE}"
fi

echo ""
echo "You can now run:"
echo "  ${BINARY_NAME} version"
echo "  ${BINARY_NAME} rv"
echo "  ${BINARY_NAME} cm"