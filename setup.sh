#!/usr/bin/env bash
set -euo pipefail

TASK_VERSION="3.43.3"
INSTALL_DIR="${HOME}/.local/bin"
PROJECT_DIR="$(cd "$(dirname "$0")" && pwd)"

mkdir -p "$INSTALL_DIR"

# Ensure INSTALL_DIR is in PATH for this session
case ":$PATH:" in
    *":$INSTALL_DIR:"*) ;;
    *) export PATH="$INSTALL_DIR:$PATH" ;;
esac

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

# Install all ov dependencies
install_deps() {
    if command -v pacman &>/dev/null; then
        install_arch_deps
    elif command -v dnf &>/dev/null; then
        install_dnf_deps
    elif command -v apt-get &>/dev/null; then
        install_apt_deps
    else
        echo "Warning: no supported package manager found — install dependencies manually"
        echo "Run 'ov doctor' after building to see what's missing"
    fi
}

install_arch_deps() {
    # All packages from official Arch repos for full ov support
    PKGS=(
        # Core: build and container engine
        go git docker podman
        # VM: QEMU and libvirt backends
        qemu-full qemu-img virtiofsd libvirt
        # Encryption: gocryptfs bind mounts
        gocryptfs fuse3
        # Merge: multi-platform manifest handling
        skopeo
        # Tunnels: Tailscale
        tailscale
        # Already in base but ensure present
        openssh util-linux
    )

    echo "Installing ov dependencies (Arch Linux)..."
    sudo pacman -S --needed --noconfirm "${PKGS[@]}"

    # Post-install service setup
    echo ""
    echo "Enabling services..."

    # Docker daemon
    if ! systemctl is-active --quiet docker; then
        sudo systemctl enable --now docker
        echo "  Docker daemon enabled and started"
    fi

    # Add user to docker group if not already a member
    if ! id -nG "$USER" | grep -qw docker; then
        sudo usermod -aG docker "$USER"
        echo "  Added $USER to docker group (log out and back in to take effect)"
    fi

    # Tailscale daemon
    if ! systemctl is-active --quiet tailscaled; then
        sudo systemctl enable --now tailscaled
        echo "  Tailscale daemon enabled and started"
    fi

    # Libvirt: enable virtqemud (modular daemon, replaces monolithic libvirtd)
    if ! systemctl is-active --quiet virtqemud.socket 2>/dev/null; then
        sudo systemctl enable --now virtqemud.socket 2>/dev/null || true
        echo "  virtqemud socket enabled"
    fi

    # Add user to libvirt group for session access
    if ! id -nG "$USER" | grep -qw libvirt; then
        sudo usermod -aG libvirt "$USER"
        echo "  Added $USER to libvirt group (log out and back in to take effect)"
    fi

    # Rootless podman: ensure subuid/subgid are configured
    if ! grep -q "^${USER}:" /etc/subuid 2>/dev/null; then
        echo "  Warning: $USER not in /etc/subuid — rootless podman needs:"
        echo "    sudo usermod --add-subuids 100000-165535 --add-subgids 100000-165535 $USER"
    fi

    # AUR packages (not available in official repos)
    AUR_MISSING=()
    if ! command -v cloudflared &>/dev/null; then
        AUR_MISSING+=("cloudflared-bin  (Cloudflare tunnels)")
    fi
    if ! command -v gvproxy &>/dev/null && [ ! -f /usr/lib/podman/gvproxy ]; then
        AUR_MISSING+=("gvisor-tap-vsock  (podman machine networking)")
    fi
    if [ ${#AUR_MISSING[@]} -gt 0 ]; then
        echo ""
        echo "The following packages are only available from AUR."
        echo "Install with yay (or another AUR helper):"
        for pkg in "${AUR_MISSING[@]}"; do
            echo "  yay -S ${pkg%% *}"
        done
    fi
}

install_dnf_deps() {
    # Fedora/RHEL: only install VM deps when podman is present (original behavior)
    if ! command -v podman &>/dev/null; then
        return
    fi

    MISSING_PKGS=()

    if ! command -v gvproxy &>/dev/null && [ ! -f /usr/libexec/podman/gvproxy ]; then
        MISSING_PKGS+=(gvisor-tap-vsock)
    fi
    if [ ! -f /usr/libexec/qemu-kvm ] && ! command -v qemu-system-x86_64 &>/dev/null; then
        MISSING_PKGS+=(qemu-kvm)
    fi
    if ! command -v qemu-img &>/dev/null; then
        MISSING_PKGS+=(qemu-img)
    fi
    if ! command -v virtiofsd &>/dev/null; then
        MISSING_PKGS+=(virtiofsd)
    fi

    if [ ${#MISSING_PKGS[@]} -gt 0 ]; then
        echo "Installing VM dependencies: ${MISSING_PKGS[*]}..."
        sudo dnf install -y "${MISSING_PKGS[@]}"
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
}

install_apt_deps() {
    # Debian/Ubuntu: only install VM deps when podman is present (original behavior)
    if ! command -v podman &>/dev/null; then
        return
    fi

    MISSING_PKGS=()

    if ! command -v qemu-system-x86_64 &>/dev/null; then
        MISSING_PKGS+=(qemu-system-x86)
    fi
    if ! command -v qemu-img &>/dev/null; then
        MISSING_PKGS+=(qemu-utils)
    fi
    if ! command -v virtiofsd &>/dev/null; then
        MISSING_PKGS+=(virtiofsd)
    fi

    if [ ${#MISSING_PKGS[@]} -gt 0 ]; then
        echo "Installing VM dependencies: ${MISSING_PKGS[*]}..."
        sudo apt-get install -y "${MISSING_PKGS[@]}"
    fi
}

install_deps

# Build and install ov
echo ""
echo "Building and installing ov..."
task -d "$PROJECT_DIR" build:install

echo ""
echo "Setup complete. Both task and ov are in $INSTALL_DIR"
echo "Run 'ov doctor' to verify all dependencies."

# Check if INSTALL_DIR is in the user's persistent PATH
if ! grep -q "\.local/bin" ~/.bashrc 2>/dev/null && \
   ! grep -q "\.local/bin" ~/.zshrc 2>/dev/null && \
   ! grep -q "\.local/bin" ~/.profile 2>/dev/null; then
    echo ""
    echo "NOTE: $INSTALL_DIR may not be in your login PATH. Add it permanently:"
    if [ -n "${ZSH_VERSION:-}" ] || [ -f ~/.zshrc ]; then
        echo "  echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.zshrc"
    else
        echo "  echo 'export PATH=\"\$HOME/.local/bin:\$PATH\"' >> ~/.bashrc"
    fi
fi
