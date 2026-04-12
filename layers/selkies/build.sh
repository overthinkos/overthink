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

# Patch selkies.py to reuse a single pixelflux ScreenCapture() per display_id
# across reconfigure_displays cycles.
#
# Root cause: pixelflux_wayland's WaylandBackend has no Drop impl and its
# run_wayland_thread runs event_loop.run(None, ...) with no exit path — the
# CalloopEvent::Closed handler is empty (lib.rs:928). Upstream selkies
# creates a fresh ScreenCapture() on every _start_capture_for_display call,
# so every user-triggered reconfigure (framerate/resolution/encoder change)
# leaks an entire Wayland compositor AppState — Space, ShmState, GBM device,
# GLES renderer, offscreen Dmabuf, frame_buffer Vec<u8> — into the container
# cgroup, showing up as memfd-backed shmem that can only be reclaimed by
# destroying the cgroup (container restart).
#
# Observed live: ~3.67 GB shmem accumulated over 48 minutes of active
# streaming under CPU JPEG encoder backlog on a 3058x1604@60 session,
# matching ~60 leaked compositor instances at ~50 MB each.
#
# Fix: cache ScreenCapture per display_id on self, so stop_capture/start_capture
# cycles reuse the same Rust thread instead of leaking a new one each time.
python3 -c "
f = 'src/selkies/selkies.py'
with open(f) as fh:
    code = fh.read()

needle = '            capture_module = ScreenCapture()\n'
replacement = (
    '            if not hasattr(self, \"_shared_screen_captures\"):\n'
    '                self._shared_screen_captures = {}\n'
    '            if display_id not in self._shared_screen_captures:\n'
    '                self._shared_screen_captures[display_id] = ScreenCapture()\n'
    '            capture_module = self._shared_screen_captures[display_id]\n'
)

if needle not in code:
    raise SystemExit('FATAL: could not locate ScreenCapture() instantiation in selkies.py — upstream changed?')
if code.count(needle) != 1:
    raise SystemExit('FATAL: ScreenCapture() instantiation is not unique in selkies.py — patch ambiguity')

code = code.replace(needle, replacement)

with open(f, 'w') as fh:
    fh.write(code)

print('Patched selkies.py: _shared_screen_captures cache prevents pixelflux WaylandBackend leak on reconfigure')
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
