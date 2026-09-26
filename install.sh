#!/usr/bin/env bash
# tether-browser universal one-line installer
# Usage: curl -fsSL https://raw.githubusercontent.com/jeffhuen/tether-browser/main/install.sh | bash

set -euo pipefail
REPO="jeffhuen/tether-browser"
VERSION="0.1.38"
# Color helpers
if [ -t 1 ]; then
    BOLD="\033[1m"
    GREEN="\033[32m"
    CYAN="\033[36m"
    RED="\033[31m"
    RESET="\033[0m"
else
    BOLD=""
    GREEN=""
    CYAN=""
    RED=""
    RESET=""
fi

echo -e "${BOLD}${CYAN}Installing tether-browser v${VERSION}...${RESET}"

# 1. Detect OS
OS="$(uname -s | tr '[:upper:]' '[:lower:]')"
case "$OS" in
    darwin)
        OS="darwin"
        ;;
    linux)
        OS="linux"
        ;;
    *)
        echo -e "${RED}Error: Unsupported operating system: ${OS}${RESET}" >&2
        exit 1
        ;;
esac

# 2. Detect Architecture
ARCH="$(uname -m)"
case "$ARCH" in
    x86_64|amd64)
        ARCH="amd64"
        ;;
    arm64|aarch64)
        ARCH="arm64"
        ;;
    *)
        echo -e "${RED}Error: Unsupported architecture: ${ARCH}${RESET}" >&2
        exit 1
        ;;
esac

echo "Detected platform: ${OS}/${ARCH}"

# 3. Choose installation directory
INSTALL_DIR="/usr/local/bin"
USE_SUDO=false

if [ ! -d "$INSTALL_DIR" ]; then
    INSTALL_DIR="${HOME}/.local/bin"
    mkdir -p "$INSTALL_DIR"
elif [ ! -w "$INSTALL_DIR" ]; then
    if command -v sudo >/dev/null 2>&1 && [ -t 0 ]; then
        USE_SUDO=true
    else
        INSTALL_DIR="${HOME}/.local/bin"
        mkdir -p "$INSTALL_DIR"
    fi
fi

TMP_DIR="$(mktemp -d)"
cleanup() {
    rm -rf "$TMP_DIR"
}
trap cleanup EXIT

BIN_PATH="${TMP_DIR}/tether"

# 4. Download release artifact or compile via Go
DOWNLOAD_URL="https://github.com/${REPO}/releases/download/v${VERSION}/tether-${OS}-${ARCH}"
DOWNLOAD_TAR_URL="https://github.com/${REPO}/releases/download/v${VERSION}/tether-v${VERSION}-${OS}-${ARCH}.tar.gz"

download_success=false
if command -v curl >/dev/null 2>&1; then
    if curl -fsSL -o "$BIN_PATH" "$DOWNLOAD_URL" 2>/dev/null; then
        chmod +x "$BIN_PATH"
        download_success=true
    elif curl -fsSL -o "${TMP_DIR}/tether.tar.gz" "$DOWNLOAD_TAR_URL" 2>/dev/null; then
        tar -xzf "${TMP_DIR}/tether.tar.gz" -C "$TMP_DIR"
        EXTRACTED_BIN="$(find "$TMP_DIR" -type f -name tether | head -n 1)"
        if [ -n "$EXTRACTED_BIN" ]; then
            mv "$EXTRACTED_BIN" "$BIN_PATH"
            chmod +x "$BIN_PATH"
            download_success=true
        fi
    fi
fi
if [ "$download_success" = false ]; then
    if command -v go >/dev/null 2>&1; then
        echo "Compiling via local Go toolchain..."
        if ! GOBIN="$TMP_DIR" CGO_ENABLED=0 go install -ldflags="-s -w" "github.com/${REPO}/cmd/tether@v${VERSION}" 2>/dev/null; then
            GOBIN="$TMP_DIR" CGO_ENABLED=0 go install -ldflags="-s -w" "github.com/${REPO}/cmd/tether@main"
        fi
        if [ -f "${TMP_DIR}/tether" ]; then
            BIN_PATH="${TMP_DIR}/tether"
        fi
    else
        echo -e "${RED}Error: Could not download release artifact and Go is not installed.${RESET}" >&2
        echo "Please visit https://github.com/${REPO}/releases to download manually." >&2
        exit 1
    fi
fi

chmod +x "$BIN_PATH"

# 5. Install to destination
DEST_BIN="${INSTALL_DIR}/tether"
DEST_ALIAS="${INSTALL_DIR}/tether-browser"

if [ "$USE_SUDO" = true ]; then
    echo "Installing to ${INSTALL_DIR} (requires sudo)..."
    sudo mv "$BIN_PATH" "$DEST_BIN"
    sudo ln -sf "$DEST_BIN" "$DEST_ALIAS"
else
    echo "Installing to ${INSTALL_DIR}..."
    mv "$BIN_PATH" "$DEST_BIN"
    ln -sf "$DEST_BIN" "$DEST_ALIAS"
fi

# Ensure PATH includes INSTALL_DIR if ~/.local/bin
if [ "$INSTALL_DIR" = "${HOME}/.local/bin" ]; then
    case ":${PATH}:" in
        *":${INSTALL_DIR}:"*)
            ;;
        *)
            SHELL_RC=""
            if [ -n "${ZSH_VERSION:-}" ] || [ -f "${HOME}/.zshrc" ]; then
                SHELL_RC="${HOME}/.zshrc"
            elif [ -f "${HOME}/.bashrc" ]; then
                SHELL_RC="${HOME}/.bashrc"
            fi
            if [ -n "$SHELL_RC" ]; then
                echo "export PATH=\"${INSTALL_DIR}:\$PATH\"" >> "$SHELL_RC"
                echo "Added ${INSTALL_DIR} to PATH in ${SHELL_RC}"
            fi
            ;;
    esac
fi
# Auto-register native messaging host if Chrome, Brave, or Edge is present
if [ -x "$DEST_BIN" ]; then
    "$DEST_BIN" extension install >/dev/null 2>&1 || true
fi

echo ""
echo -e "${GREEN}${BOLD}✓ tether-browser v${VERSION} installed successfully!${RESET}"
echo "  Binary: ${DEST_BIN}"
echo "  Alias:  ${DEST_ALIAS}"
echo ""
echo "Quick start:"
echo "  1. On your Mac:    tether connect user@server"
echo "  2. On your server: tether open https://example.com"
echo "                     tether snapshot -i"
echo "                     tether click @e1"
