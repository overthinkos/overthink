#!/usr/bin/env bash
# 100ms stat-poll disk-event watcher. Runs INSIDE the jupyter pod.
# Emits one JSONL line per detected change to /tmp/disk-events.jsonl.
#
# Usage: bash disk_watcher.sh <workspace-dir> <notebook-name>
#   workspace-dir: e.g. /home/user/workspace
#   notebook-name: e.g. test.ipynb (the .notebook:*.y CRDT log glob is derived)
set -euo pipefail

WORKSPACE="${1:?workspace dir required}"
NOTEBOOK="${2:?notebook name required}"
OUT="/tmp/disk-events.jsonl"

: > "$OUT"

# State table: path → "mtime|size"
declare -A LAST

watch_paths() {
    # Snapshot path: the .ipynb file (jupyter-collaboration's autosave target)
    printf '%s\n' "$WORKSPACE/$NOTEBOOK"
    # YStore: this image uses the SQLite-backed YStore at $HOME/.jupyter_ystore.db
    # (not the FileYStore's .notebook:*.y in workspace). Watch the db + its
    # WAL/journal files, where Yjs CRDT updates land first.
    find "$HOME" -maxdepth 1 -name '.jupyter_ystore.db*' 2>/dev/null || true
    # Also catch FileYStore form, in case the deploy ever switches backends.
    find "$WORKSPACE" -maxdepth 1 -name '.notebook:*.y' 2>/dev/null || true
}

emit() {
    local path="$1" mtime="$2" size="$3" sha=""
    if [[ -f "$path" && "$size" != "0" ]]; then
        sha=$(sha256sum "$path" 2>/dev/null | awk '{print $1}' || echo "")
    fi
    local now_mono
    now_mono=$(date +%s.%N)
    printf '{"ts_wall":"%s","ts_mono_s":%s,"source":"disk","path":"%s","mtime":"%s","size":%s,"sha256":"%s"}\n' \
        "$(date -u +%Y-%m-%dT%H:%M:%S.%3NZ)" \
        "$now_mono" \
        "$path" \
        "$mtime" \
        "$size" \
        "$sha" \
        >> "$OUT"
}

# Initial snapshot — emit baseline state for every existing watched path.
while IFS= read -r p; do
    [[ -e "$p" ]] || continue
    stat_out=$(stat -c '%Y.%N|%s' "$p" 2>/dev/null || echo "")
    [[ -z "$stat_out" ]] && continue
    LAST["$p"]="$stat_out"
    mtime="${stat_out%|*}"
    size="${stat_out#*|}"
    emit "$p" "$mtime" "$size"
done < <(watch_paths)

# Poll loop — 100ms cadence, emit on change.
while true; do
    while IFS= read -r p; do
        [[ -e "$p" ]] || continue
        stat_out=$(stat -c '%Y.%N|%s' "$p" 2>/dev/null || echo "")
        [[ -z "$stat_out" ]] && continue
        if [[ "${LAST[$p]:-}" != "$stat_out" ]]; then
            LAST["$p"]="$stat_out"
            mtime="${stat_out%|*}"
            size="${stat_out#*|}"
            emit "$p" "$mtime" "$size"
        fi
    done < <(watch_paths)
    sleep 0.1
done
