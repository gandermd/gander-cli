#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIST_DIR="$SCRIPT_DIR/dist"
INSTALL_DIR=""
BIN_NAME="mdp"

# Detect OS
if [[ "$OSTYPE" != "darwin"* ]]; then
    echo "Error: mdp currently supports macOS only."
    exit 1
fi

# Detect arch
ARCH=$(uname -m)
if [[ "$ARCH" == "arm64" ]]; then
    BINARY="$DIST_DIR/mdp-darwin-arm64"
else
    echo "Error: unsupported architecture: $ARCH"
    exit 1
fi

if [[ ! -f "$BINARY" ]]; then
    echo "Error: binary not found at $BINARY"
    echo "Make sure the dist/ directory exists and contains the mdp binary."
    exit 1
fi

# Find installation directory
# Priority: ~/go/bin (no sudo needed) > /usr/local/bin (requires sudo)
if [[ -d "$HOME/go/bin" ]]; then
    INSTALL_DIR="$HOME/go/bin"
elif [[ -w "/usr/local/bin" ]]; then
    INSTALL_DIR="/usr/local/bin"
else
    echo "Error: cannot find a writable install location."
    echo "Please ensure ~/go/bin exists or you have sudo access to /usr/local/bin."
    echo "You can create ~/go/bin with: mkdir -p \$HOME/go/bin"
    exit 1
fi

DEST="$INSTALL_DIR/$BIN_NAME"

# Copy and install
cp "$BINARY" "$DEST"
chmod +x "$DEST"

echo "Installed mdp to $DEST"

# Verify PATH
if [[ ":$PATH:" != *":$INSTALL_DIR:"* ]]; then
    echo ""
    echo "Warning: $INSTALL_DIR is not in your PATH."
    echo "Add it with: export PATH=\$PATH:$INSTALL_DIR"
    echo "Or restart your terminal."
fi

# Check if mdp is now accessible
if command -v mdp &> /dev/null; then
    echo "mdp is ready to use: mdp -h"
else
    echo "Run 'export PATH=\$PATH:$INSTALL_DIR' or restart your terminal, then: mdp -h"
fi