#!/bin/sh
# ================================================================
# OK Universal Agent — Linux/macOS One-Line Installer
# Usage: curl -sSL https://ok.sh/install.sh | sh
# ================================================================

set -e
OK_VERSION="${OK_VERSION:-latest}"
OK_REPO="${OK_REPO:-esengine/ok}"
OK_BIN="${OK_BIN:-$HOME/.ok/bin}"
OK_HOME="${OK_HOME:-$HOME/.config/ok}"

RED='\033[31m'
GREEN='\033[32m'
BOLD='\033[1m'
NC='\033[0m'

echo ""
echo "  ${BOLD}◆ OK Universal Agent Installer${NC}"
echo "  ============================="
echo ""

# Check if already installed
if command -v ok >/dev/null 2>&1; then
    echo "  ✓ OK is already installed ($(ok --version 2>/dev/null || echo 'unknown'))"
    echo "  Run 'ok' to start."
    exit 0
fi

# Detect OS and architecture
OS=$(uname -s | tr '[:upper:]' '[:lower:]')
ARCH=$(uname -m)
case "$ARCH" in
    x86_64|amd64) ARCH="amd64" ;;
    aarch64|arm64) ARCH="arm64" ;;
    *) echo "${RED}✗ Unsupported architecture: $ARCH${NC}"; exit 1 ;;
esac

echo "  → Downloading OK for ${OS}/${ARCH}..."

mkdir -p "$OK_BIN" "$OK_HOME"

# Download binary
URL="https://github.com/${OK_REPO}/releases/latest/download/ok-${OS}-${ARCH}"
if [ "$OS" = "darwin" ]; then
    URL="${URL}.tar.gz"
    curl -sSL "$URL" | tar xz -C "$OK_BIN" ok
else
    curl -sSL "$URL" -o "$OK_BIN/ok"
fi
chmod +x "$OK_BIN/ok"

# Add to PATH
SHELL_RC=""
case "$SHELL" in
    */zsh) SHELL_RC="$HOME/.zshrc" ;;
    */bash) SHELL_RC="$HOME/.bashrc" ;;
    */fish) SHELL_RC="$HOME/.config/fish/config.fish" ;;
esac
if [ -n "$SHELL_RC" ] && [ -f "$SHELL_RC" ]; then
    if ! grep -q "$OK_BIN" "$SHELL_RC" 2>/dev/null; then
        echo "export PATH=\"\$PATH:$OK_BIN\"" >> "$SHELL_RC"
    fi
fi

# Symlink to /usr/local/bin if possible
if [ -w /usr/local/bin ]; then
    ln -sf "$OK_BIN/ok" /usr/local/bin/ok 2>/dev/null || true
fi

echo ""
echo "  ${GREEN}✓ Installed to $OK_BIN/ok${NC}"
echo ""
echo "  ◆ Run 'ok' to start the universal agent."
echo "  ◆ Run 'ok doctor' to verify installation."
echo "  ◆ Run 'ok setup' to configure your API keys."
echo ""
echo "  Join the community: https://discord.gg/XF78rEME2D"
echo ""
