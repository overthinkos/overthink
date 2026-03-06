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

# Ensure podman machine and VM dependencies are available
if command -v podman &>/dev/null; then
    MISSING_PKGS=()

    # gvproxy (networking for podman machine)
    if ! command -v gvproxy &>/dev/null && [ ! -f /usr/libexec/podman/gvproxy ]; then
        MISSING_PKGS+=(gvisor-tap-vsock)
    fi

    # qemu-kvm (VM backend for podman machine and ov vm)
    if [ ! -f /usr/libexec/qemu-kvm ] && ! command -v qemu-system-x86_64 &>/dev/null; then
        MISSING_PKGS+=(qemu-kvm)
    fi

    # qemu-img (disk image conversion for ov vm build)
    if ! command -v qemu-img &>/dev/null; then
        MISSING_PKGS+=(qemu-img)
    fi

    # virtiofsd (filesystem sharing for VMs)
    if ! command -v virtiofsd &>/dev/null; then
        MISSING_PKGS+=(virtiofsd)
    fi

    if [ ${#MISSING_PKGS[@]} -gt 0 ]; then
        echo "Installing VM dependencies: ${MISSING_PKGS[*]}..."
        if command -v dnf &>/dev/null; then
            sudo dnf install -y "${MISSING_PKGS[@]}"
        elif command -v apt-get &>/dev/null; then
            sudo apt-get install -y "${MISSING_PKGS[@]}"
        else
            echo "Warning: could not install ${MISSING_PKGS[*]} — install manually"
        fi
    fi

    # On RHEL/CentOS, binaries install under /usr/libexec/ but podman machine
    # expects them in PATH. Create symlinks as needed.
    if [ -f /usr/libexec/qemu-kvm ] && ! command -v qemu-system-x86_64 &>/dev/null; then
        echo "Creating qemu-system-x86_64 symlink for podman machine..."
        sudo ln -sf /usr/libexec/qemu-kvm /usr/local/bin/qemu-system-x86_64
    fi
    if [ -f /usr/libexec/virtiofsd ] && ! command -v virtiofsd &>/dev/null; then
        echo "Creating virtiofsd symlink for podman machine..."
        sudo ln -sf /usr/libexec/virtiofsd /usr/local/bin/virtiofsd
    fi
fi

# Build and install ov
echo "Building and installing ov..."
task -d "$PROJECT_DIR" build:install

echo ""
echo "Setup complete. Both task and ov are in $INSTALL_DIR"
