#!/bin/bash
# Selkies build script — runs in pixi builder stage.
# Builder image provides: gcc, nodejs, npm, python3-devel, etc.
set -euo pipefail

# 1. Install selkies Python package (C extensions: evdev, xkbcommon)
cd /tmp
curl -sL https://github.com/selkies-project/selkies/archive/af1a1c252563d2f136d641b81d6b1dd38a3a0d93.tar.gz | tar xzf -
cd selkies-*
sed -i '/"av>/d' pyproject.toml
sed -i '/cryptography/d' pyproject.toml
"$HOME/.pixi/envs/default/bin/pip" install .

# 2. Build web UI dashboard (needs nodejs/npm from builder image)
cd addons/gst-web-core
npm install && npm run build
cd ../selkies-dashboard
cp ../gst-web-core/dist/selkies-core.js src/
npm install && npm run build

# 3. Stage web UI assets under $HOME (pixi copy_artifacts copies $HOME)
mkdir -p "$HOME/.local/share/selkies-build/web"
cp -r dist/* "$HOME/.local/share/selkies-build/web/"
cp ../gst-web-core/dist/selkies-core.js "$HOME/.local/share/selkies-build/web/src/" 2>/dev/null || true

# 4. Create NVRTC symlinks for pixelflux NVENC
NVRTC_DIR="$HOME/.pixi/envs/default/lib/python3.13/site-packages/nvidia/cu13/lib"
if [ -f "$NVRTC_DIR/libnvrtc.so.13" ] && [ ! -f "$NVRTC_DIR/libnvrtc.so" ]; then
    ln -s libnvrtc.so.13 "$NVRTC_DIR/libnvrtc.so"
    ln -s libnvrtc-builtins.so.13.2 "$NVRTC_DIR/libnvrtc-builtins.so" 2>/dev/null || true
fi

# Cleanup
cd /
rm -rf /tmp/selkies-*
