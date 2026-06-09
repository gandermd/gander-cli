#!/bin/bash
set -e

SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
DIST_DIR="$SCRIPT_DIR/dist"
INSTALL_DIR=""
BIN_NAME="mdp"

# Detect OS
OS_TYPE=""
if [[ "$OSTYPE" == "darwin"* ]]; then
    OS_TYPE="darwin"
elif [[ "$OSTYPE" == "linux"* ]]; then
    OS_TYPE="linux"
fi

if [[ -z "$OS_TYPE" ]]; then
    echo "Error: mdp currently supports macOS and Linux only."
    exit 1
fi

# Detect arch
ARCH=$(uname -m)
PLATFORM=""
if [[ "$OS_TYPE" == "darwin" ]]; then
    if [[ "$ARCH" == "arm64" ]]; then
        PLATFORM="darwin-arm64"
    elif [[ "$ARCH" == "x86_64" ]]; then
        PLATFORM="darwin-amd64"
    fi
elif [[ "$OS_TYPE" == "linux" ]]; then
    if [[ "$ARCH" == "x86_64" ]]; then
        PLATFORM="linux-amd64"
    elif [[ "$ARCH" == "aarch64" ]]; then
        PLATFORM="linux-arm64"
    fi
fi

if [[ -z "$PLATFORM" ]]; then
    echo "Error: unsupported architecture: $ARCH on $OS_TYPE"
    exit 1
fi

BINARY="$DIST_DIR/mdp-$PLATFORM"

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