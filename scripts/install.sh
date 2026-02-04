#!/bin/bash
set -e

# Freeman Installation Script
# This script downloads the latest release from GitHub and installs it.

VERSION="1.0.0-draft"
BINARY_NAME="freeman"
INSTALL_DIR="/usr/local/bin"

echo "🎙️ Starting Freeman Installation..."

# Detect OS
OS="$(uname -s)"
ARCH="$(uname -m)"

case "$OS" in
    Darwin)
        PLATFORM="darwin"
        ;;
    Linux)
        PLATFORM="linux"
        ;;
    *)
        echo "❌ Unsupported OS: $OS"
        exit 1
        ;;
esac

echo "✅ Detected $OS ($ARCH)"

# In a real scenario, we'd find the latest release URL
# For now, this is a template for the final script.
echo "🔄 Downloading Freeman $VERSION..."
# curl -L -o /tmp/freeman "https://github.com/yourname/freeman/releases/download/v$VERSION/freeman-$PLATFORM-$ARCH"

# Manual installation from dist for now (if available)
if [ -f "./dist/freeman" ]; then
    echo "📦 Installing from local build..."
    sudo cp ./dist/freeman "$INSTALL_DIR/$BINARY_NAME"
    sudo chmod +x "$INSTALL_DIR/$BINARY_NAME"
    echo "🎉 Freeman installed successfully to $INSTALL_DIR/$BINARY_NAME"
    echo "🚀 Run 'freeman --help' to get started."
else
    echo "⚠️ Binary not found in ./dist/freeman. Please run 'python3 build.py' first."
fi
