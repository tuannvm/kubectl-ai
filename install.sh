#!/usr/bin/env bash
set -e

REPO="GoogleCloudPlatform/kubectl-ai"
BINARY="kubectl-ai"

# Detect OS
OS="$(uname | tr '[:upper:]' '[:lower:]')"
case "$OS" in
  linux)   OS="linux" ;;
  darwin)  OS="darwin" ;;
  *) echo "Unsupported OS: $OS"; exit 1 ;;
esac

# Detect ARCH
ARCH="$(uname -m)"
case "$ARCH" in
  x86_64|amd64) ARCH="amd64" ;;
  arm64|aarch64) ARCH="arm64" ;;
  *) echo "Unsupported architecture: $ARCH"; exit 1 ;;
esac

# Get latest version tag from GitHub API (portable, no grep -P)
LATEST_TAG=$(curl -s "https://api.github.com/repos/$REPO/releases/latest" | sed -n 's/.*"tag_name": *"\([^"]*\)".*/\1/p')
if [ -z "$LATEST_TAG" ]; then
  echo "Failed to fetch latest release tag."
  exit 1
fi

# Compose download URL
TARBALL="kubectl-ai_${OS}_${ARCH}.tar.gz"
URL="https://github.com/$REPO/releases/download/$LATEST_TAG/$TARBALL"

# Download and extract
echo "Downloading $URL ..."
curl -LO "$URL"
tar -xzf "$TARBALL"

# Move binary to /usr/local/bin (may require sudo)
echo "Installing $BINARY to /usr/local/bin (may require sudo)..."
sudo mv "$BINARY" /usr/local/bin/

# Ensure the binary is executable
sudo chmod +x /usr/local/bin/$BINARY

# Clean up
rm "$TARBALL"

echo "✅ $BINARY installed successfully! Run '$BINARY --help' to get started."

