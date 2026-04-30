#!/usr/bin/env bash
# Top-level driver for the jupyter concurrency + data-safety harness.
#
# Provisions two ephemeral containers via podman (no operator deploys touched):
#   - ov-jupyter-concurrency-test  (jupyter image; host port 18888)
#   - ov-sway-browser-vnc-concurrency-test  (sway-browser-vnc; ports 15900/19222/19224)
#
# Both join the `ov` podman network so cross-pod hostname resolution works.
# Containers run with --rm so they vanish on stop.
#
# Usage:
#   bash tests/jupyter-concurrency/run.sh
#   JC_MCP_WRITERS=8 JC_CDP_TABS=4 bash ...   # stress
#   JC_SCENARIOS=disjoint-cells,burst bash ... # subset
#   JC_KEEP_BEDS=1 bash ...                    # don't tear down on exit
set -euo pipefail

HERE="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
JUPYTER_IMAGE="${JC_JUPYTER_IMAGE:-jupyter}"
BROWSER_IMAGE="${JC_BROWSER_IMAGE:-sway-browser-vnc}"
INSTANCE="${JC_INSTANCE:-concurrency-test}"
MCP_WRITERS="${JC_MCP_WRITERS:-4}"
CDP_TABS="${JC_CDP_TABS:-2}"
SCENARIOS="${JC_SCENARIOS:-disjoint-cells,same-cell-mcp,insert-at-zero,delete-vs-edit,read-during-write,execute-vs-edit,burst,mixed-mcp-cdp,data-safety-kill}"

JUPYTER_HOST_PORT="${JC_JUPYTER_HOST_PORT:-18888}"
SWAY_VNC_HOST_PORT="${JC_SWAY_VNC_HOST_PORT:-15900}"
SWAY_CDP_HOST_PORT="${JC_SWAY_CDP_HOST_PORT:-19222}"
SWAY_OPENCLAW_HOST_PORT="${JC_SWAY_OPENCLAW_HOST_PORT:-19224}"
JUPYTER_REF="${JC_JUPYTER_REF:-ghcr.io/overthinkos/${JUPYTER_IMAGE}:latest}"
BROWSER_REF="${JC_BROWSER_REF:-ghcr.io/overthinkos/${BROWSER_IMAGE}:latest}"

JUPYTER_C="ov-${JUPYTER_IMAGE}-${INSTANCE}"
BROWSER_C="ov-${BROWSER_IMAGE}-${INSTANCE}"
WORKSPACE_VOL="jc-${INSTANCE}-workspace"

echo "==> Resolving images (cached if present)"
podman image exists "$JUPYTER_REF" || podman pull "$JUPYTER_REF"
podman image exists "$BROWSER_REF" || podman pull "$BROWSER_REF"

echo "==> Tearing down any stale test-bed containers"
podman rm -f "$JUPYTER_C" 2>/dev/null || true
podman rm -f "$BROWSER_C" 2>/dev/null || true

echo "==> Ensuring podman network 'ov' exists"
podman network exists ov || podman network create ov

echo "==> Spinning up $JUPYTER_C on port $JUPYTER_HOST_PORT"
podman volume exists "$WORKSPACE_VOL" || podman volume create "$WORKSPACE_VOL"
# The image's CMD is a stub (/bin/bash). ov-deployed containers override it
# with the supervisord init system command from the org.overthinkos.init OCI
# label. We mirror that here.
podman run -d --rm \
    --name "$JUPYTER_C" \
    --network ov \
    -p "${JUPYTER_HOST_PORT}:8888" \
    -v "${WORKSPACE_VOL}:/home/user/workspace" \
    -e "MCP_SERVER_NAME=${JUPYTER_IMAGE}" \
    "$JUPYTER_REF" \
    supervisord -n -c /etc/supervisord.conf >/dev/null

echo "==> Spinning up $BROWSER_C on ports $SWAY_VNC_HOST_PORT/$SWAY_CDP_HOST_PORT/$SWAY_OPENCLAW_HOST_PORT"
podman run -d --rm \
    --name "$BROWSER_C" \
    --network ov \
    -p "${SWAY_VNC_HOST_PORT}:5900" \
    -p "${SWAY_CDP_HOST_PORT}:9222" \
    -p "${SWAY_OPENCLAW_HOST_PORT}:9224" \
    "$BROWSER_REF" \
    supervisord -n -c /etc/supervisord.conf >/dev/null

echo "==> Waiting for jupyter MCP to come up (max 90s)"
for i in $(seq 1 45); do
    if curl -fsS "http://127.0.0.1:${JUPYTER_HOST_PORT}/api" >/dev/null 2>&1; then
        echo "    jupyter API up after ${i}*2s"
        break
    fi
    sleep 2
done
curl -fsS "http://127.0.0.1:${JUPYTER_HOST_PORT}/api" >/dev/null || {
    echo "ERROR: jupyter did not become ready"
    podman logs --tail 50 "$JUPYTER_C" 2>&1 | sed 's/^/  jupyter-log: /'
    exit 1
}

echo "==> Waiting for sway-browser CDP (max 60s)"
for i in $(seq 1 30); do
    if curl -fsS "http://127.0.0.1:${SWAY_CDP_HOST_PORT}/json/version" >/dev/null 2>&1; then
        echo "    CDP up after ${i}*2s"
        break
    fi
    sleep 2
done
curl -fsS "http://127.0.0.1:${SWAY_CDP_HOST_PORT}/json/version" >/dev/null || {
    echo "WARNING: CDP did not become ready (CDP scenarios may fail)"
    podman logs --tail 30 "$BROWSER_C" 2>&1 | sed 's/^/  browser-log: /'
}

echo "==> Installing disk_watcher.sh into ${JUPYTER_C}:/tmp/"
WATCHER_B64="$(base64 -w 0 "$HERE/disk_watcher.sh")"
podman exec "$JUPYTER_C" bash -lc "echo $WATCHER_B64 | base64 -d > /tmp/disk_watcher.sh && chmod +x /tmp/disk_watcher.sh"

echo "==> Sanity: MCP ping via ov eval"
ov eval mcp ping "$JUPYTER_IMAGE" -i "$INSTANCE" || {
    echo "ERROR: MCP ping failed; see container logs"
    podman logs --tail 50 "$JUPYTER_C" 2>&1 | sed 's/^/  jupyter-log: /'
    exit 1
}

echo "==> Running orchestrator"
export JC_JUPYTER_IMAGE="$JUPYTER_IMAGE"
export JC_BROWSER_IMAGE="$BROWSER_IMAGE"
export JC_INSTANCE="$INSTANCE"
export JC_JUPYTER_REF="$JUPYTER_REF"
export JC_JUPYTER_HOST_PORT="$JUPYTER_HOST_PORT"
export JC_JUPYTER_WORKSPACE_VOLUME="$WORKSPACE_VOL"
RUN_TS="$(date -u +%Y%m%dT%H%M%SZ)"
OUT_DIR="$HERE/reports/$RUN_TS"
ANALYZE_RC=0
python3 "$HERE/orchestrator.py" \
    --scenarios "$SCENARIOS" \
    --mcp-writers "$MCP_WRITERS" \
    --cdp-tabs "$CDP_TABS" \
    --out-dir "$OUT_DIR"

echo "==> Analyzing"
python3 "$HERE/analyze.py" "$OUT_DIR" || ANALYZE_RC=$?

if [[ -z "${JC_KEEP_BEDS:-}" ]]; then
    echo "==> Tearing down test beds"
    podman rm -f "$JUPYTER_C" 2>/dev/null || true
    podman rm -f "$BROWSER_C" 2>/dev/null || true
    podman volume rm "$WORKSPACE_VOL" 2>/dev/null || true
fi

echo
echo "==> Outputs in: $OUT_DIR"
echo "    events.jsonl  — merged event stream"
echo "    summary.json  — orchestrator scenario states"
echo "    report.md     — human-readable analysis"
echo "    report.json   — machine-readable analysis"
echo
echo "==> Overall analyzer rc: $ANALYZE_RC (0 = all scenarios pass)"
exit "$ANALYZE_RC"
