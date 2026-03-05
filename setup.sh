#!/usr/bin/env bash
set -euo pipefail

TASK_VERSION="3.43.3"
INSTALL_DIR="${HOME}/.local/bin"
PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"

mkdir -p "$INSTALL_DIR"

# Download task if not present
if ! command -v task &>/dev/null; then
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64)  TASK_ARCH="amd64" ;;
        aarch64) TASK_ARCH="arm64" ;;
        *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
    esac

    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    URL="https://github.com/go-task/task/releases/download/v${TASK_VERSION}/task_${OS}_${TASK_ARCH}.tar.gz"

    echo "Downloading task v${TASK_VERSION} for ${OS}/${TASK_ARCH}..."
    curl -fsSL "$URL" | tar xz -C "$INSTALL_DIR" task
    echo "Installed task to $INSTALL_DIR/task"
else
    echo "task already installed at $(command -v task)"
fi

# Build and install ov
echo "Building and installing ov..."
task -d "$PROJECT_DIR" build:install

echo ""
echo "Setup complete. Both task and ov are in $INSTALL_DIR"
