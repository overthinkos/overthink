#!/usr/bin/env bash
set -euo pipefail

TASK_VERSION="3.43.3"
BIN_DIR="$(cd "$(dirname "$0")" && pwd)/bin"

mkdir -p "$BIN_DIR"

# Download task if not present
if [ ! -x "$BIN_DIR/task" ]; then
    ARCH=$(uname -m)
    case "$ARCH" in
        x86_64)  TASK_ARCH="amd64" ;;
        aarch64) TASK_ARCH="arm64" ;;
        *) echo "Unsupported architecture: $ARCH" >&2; exit 1 ;;
    esac

    OS=$(uname -s | tr '[:upper:]' '[:lower:]')
    URL="https://github.com/go-task/task/releases/download/v${TASK_VERSION}/task_${OS}_${TASK_ARCH}.tar.gz"

    echo "Downloading task v${TASK_VERSION} for ${OS}/${TASK_ARCH}..."
    curl -fsSL "$URL" | tar xz -C "$BIN_DIR" task
    chmod +x "$BIN_DIR/task"
    echo "Installed task to $BIN_DIR/task"
else
    echo "task already installed at $BIN_DIR/task"
fi

# Build ov
echo "Building ov..."
"$BIN_DIR/task" build:ov

echo ""
echo "Setup complete. Add bin/ to your PATH or use:"
echo "  bin/task build:ov      # rebuild ov"
echo "  bin/task build:install  # install ov to ~/.local/bin"
echo "  bin/task setup:builder  # create buildx builder"
