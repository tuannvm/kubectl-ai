#!/usr/bin/env bash
set -euo pipefail

REPO="GoogleCloudPlatform/kubectl-ai"
BINARY="kubectl-ai"

# Detect OS
OS="$(uname | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux)   OS="linux" ;;
  darwin)  OS="darwin" ;;
  *)
    echo "If you are on Windows or another unsupported OS, please follow the manual installation instructions at:"
    echo "https://github.com/GoogleCloudPlatform/kubectl-ai#manual-installation"
    exit 1
    ;;
esac

# Detect ARCH
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="x86_64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *)
    echo "If you are on an unsupported architecture, please follow the manual installation instructions at:"
    echo "https://github.com/GoogleCloudPlatform/kubectl-ai#manual-installation"
    exit 1
    ;;
esac

# Get latest version tag from GitHub API, Use GITHUB_TOKEN if available to avoid potential rate limit
if [ -n "${GITHUB_TOKEN:-}" ]; then
  auth_hdr="Authorization: token $GITHUB_TOKEN"
else
  auth_hdr=""
fi
LATEST_TAG=$(curl -s -H "$auth_hdr" \
  "https://api.github.com/repos/$REPO/releases/latest" \
  | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
if [ -z "$LATEST_TAG" ]; then
  echo "Failed to fetch latest release tag."
  exit 1
fi

# Compose download URL
TARBALL="kubectl-ai_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$LATEST_TAG/$TARBALL"

# Download and extract
echo "Downloading $URL ..."
curl -fSL --retry 3 "$URL" -o "$TARBALL"
tar --no-same-owner -xzf "$TARBALL"

# Move binary to /usr/local/bin (may require sudo)
echo "Installing $BINARY to /usr/local/bin (may require sudo)..."
sudo install -m 0755 "$BINARY" /usr/local/bin/

# Clean up
rm "$TARBALL"

echo "✅ $BINARY installed successfully! Run '$BINARY --help' to get started."

