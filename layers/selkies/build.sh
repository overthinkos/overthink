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

# Patch keyboard input for generic XKB layout support (not US-only):
# - Read XKB_DEFAULT_* from env instead of hardcoding US layout
# - Scan level 2 (AltGr) keys into scancode map
# - Add AltGr modifier check for level-2 key injection
# - Remove Latin-1 and Euro bypass so all chars use scancode map
python3 -c "
import re

f = 'src/selkies/input_handler.py'
with open(f) as fh:
    code = fh.read()

# 1. Read XKB layout from env instead of hardcoding US
code = code.replace(
    'rules=\"evdev\", model=\"pc105\", layout=\"us\", variant=\"\", options=\"\"',
    'rules=os.environ.get(\"XKB_DEFAULT_RULES\",\"evdev\"), model=os.environ.get(\"XKB_DEFAULT_MODEL\",\"pc105\"), layout=os.environ.get(\"XKB_DEFAULT_LAYOUT\",\"us\"), variant=os.environ.get(\"XKB_DEFAULT_VARIANT\",\"\"), options=os.environ.get(\"XKB_DEFAULT_OPTIONS\",\"\")'
)

# 2. Remove Latin-1 + Euro bypass — all chars go through scancode map
code = code.replace(
    'if (0xA0 <= keysym <= 0xFF) or keysym == 0x20AC or ((keysym & 0xFF000000) == 0x01000000):',
    'if (keysym & 0xFF000000) == 0x01000000:'
)

# 3. Add level 2 (AltGr) scanning to _build_wayland_keymap
# Find the end of level 1 scanning block and add level 2 after it
level1_block = '''            syms_1 = self.xkb_keymap.key_get_syms_by_level(kc, 0, 1)
            if syms_1:
                for sym in syms_1:
                    if sym not in self.wayland_scancode_map:
                        self.wayland_scancode_map[sym] = kc
                        if sym not in level_0_keys:
                            self.wayland_shift_required_keys.add(sym)'''

level2_addition = '''            syms_1 = self.xkb_keymap.key_get_syms_by_level(kc, 0, 1)
            if syms_1:
                for sym in syms_1:
                    if sym not in self.wayland_scancode_map:
                        self.wayland_scancode_map[sym] = kc
                        if sym not in level_0_keys:
                            self.wayland_shift_required_keys.add(sym)

            syms_2 = self.xkb_keymap.key_get_syms_by_level(kc, 0, 2)
            if syms_2:
                for sym in syms_2:
                    if sym not in self.wayland_scancode_map:
                        self.wayland_scancode_map[sym] = kc
                        self.wayland_altgr_required_keys.add(sym)'''

code = code.replace(level1_block, level2_addition)

# 4. Initialize wayland_altgr_required_keys alongside shift_required_keys
code = code.replace(
    'self.wayland_shift_required_keys = set()',
    'self.wayland_shift_required_keys = set()\\n        self.wayland_altgr_required_keys = set()'
)

# 5. For AltGr keys: inject AltGr + scancode directly via pixelflux (not wtype)
# The browser sends AltGr as momentary press/release, so active_modifiers is empty
# when the character arrives. Direct injection through the same input device avoids
# cross-device race conditions between pixelflux and wtype.
inject_block = '''            if scancode:
                try:
                    self.wayland_input.inject_key(scancode, 1 if down else 0)'''

altgr_inject = '''            if scancode is not None and hasattr(self, 'wayland_altgr_required_keys') and keysym in self.wayland_altgr_required_keys:
                if 65027 not in self.active_modifiers:
                    altgr_sc = self.wayland_scancode_map.get(0xfe03)
                    if altgr_sc:
                        try:
                            if down:
                                self.wayland_input.inject_key(altgr_sc, 1)
                                self.wayland_input.inject_key(scancode, 1)
                                self.wayland_input.inject_key(scancode, 0)
                                self.wayland_input.inject_key(altgr_sc, 0)
                            return
                        except Exception as e:
                            logger_webrtc_input.warning(f\"AltGr inject failed: {e}\")

            if scancode:
                try:
                    self.wayland_input.inject_key(scancode, 1 if down else 0)'''

code = code.replace(inject_block, altgr_inject)

with open(f, 'w') as fh:
    fh.write(code)

print('Patched input_handler.py: env-based layout, level 2 AltGr scanning, modifier checks')
"

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
