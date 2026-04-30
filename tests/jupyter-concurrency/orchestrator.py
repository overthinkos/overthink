#!/usr/bin/env python3
"""Concurrent jupyter notebook orchestrator.

Spawns N MCP writers + M CDP browser tabs + 1 watch_notebook observer + 1
disk-watcher tail against a fresh notebook on a disposable jupyter pod.
Emits a single merged JSONL log per scenario.

Subprocess-driven: shells out to ov eval mcp / ov eval cdp / ov shell.
Avoids a Python MCP SDK dependency.

Run via run.sh; not normally invoked directly.
"""
from __future__ import annotations

import argparse
import asyncio
import contextlib
import json
import os
import shlex
import shutil
import sys
import time
import uuid
from pathlib import Path
from typing import Any

JUPYTER_IMAGE = os.environ.get("JC_JUPYTER_IMAGE", "jupyter")
BROWSER_IMAGE = os.environ.get("JC_BROWSER_IMAGE", "sway-browser-vnc")
INSTANCE = os.environ.get("JC_INSTANCE", "concurrency-test")
JUPYTER_CONTAINER = f"ov-{JUPYTER_IMAGE}-{INSTANCE}"
BROWSER_CONTAINER = f"ov-{BROWSER_IMAGE}-{INSTANCE}"
WORKSPACE = "/home/user/workspace"


def now_event(source: str, op: str, **kw: Any) -> dict[str, Any]:
    return {
        "ts_wall": time.strftime("%Y-%m-%dT%H:%M:%S.", time.gmtime()) + f"{time.time_ns() % 1_000_000_000 // 1_000_000:03d}Z",
        "ts_mono_ns": time.monotonic_ns(),
        "source": source,
        "op": op,
        **kw,
    }


class EventLog:
    def __init__(self, path: Path):
        self.path = path
        self.f = path.open("w", buffering=1)
        self.lock = asyncio.Lock()

    async def emit(self, ev: dict[str, Any]) -> None:
        async with self.lock:
            self.f.write(json.dumps(ev) + "\n")

    def close(self) -> None:
        self.f.close()


async def run_proc(*cmd: str, timeout: float = 60.0) -> tuple[int, str, str]:
    proc = await asyncio.create_subprocess_exec(
        *cmd, stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE
    )
    try:
        out, err = await asyncio.wait_for(proc.communicate(), timeout=timeout)
    except asyncio.TimeoutError:
        with contextlib.suppress(ProcessLookupError):
            proc.kill()
        return -1, "", "timeout"
    return proc.returncode or 0, out.decode("utf-8", "replace"), err.decode("utf-8", "replace")


async def mcp_call(events: EventLog, session: str, tool: str, args: dict, timeout: float = 30.0) -> dict:
    submit = now_event("mcp", tool, session=session, phase="submit", args=args)
    await events.emit(submit)
    t0 = time.monotonic()
    rc, out, err = await run_proc(
        "ov", "eval", "mcp", "call", JUPYTER_IMAGE, "-i", INSTANCE, tool, json.dumps(args),
        "--json", timeout=timeout,
    )
    latency_ms = (time.monotonic() - t0) * 1000.0
    ack = now_event(
        "mcp", tool, session=session, phase="ack",
        rc=rc, latency_ms=round(latency_ms, 2),
        stderr_tail=err[-200:] if err else "",
        stdout_tail=out[-200:] if out else "",
    )
    await events.emit(ack)
    return {"rc": rc, "out": out, "err": err, "latency_ms": latency_ms}


async def cdp_invoke(events: EventLog, tab: str, method: str, *extra: str, timeout: float = 30.0) -> dict:
    submit = now_event("cdp", method, tab=tab, phase="submit", extra=list(extra))
    await events.emit(submit)
    t0 = time.monotonic()
    cmd = ["ov", "eval", "cdp", method, BROWSER_IMAGE, "-i", INSTANCE]
    if tab:
        cmd += ["--tab", tab]
    cmd += list(extra)
    rc, out, err = await run_proc(*cmd, timeout=timeout)
    latency_ms = (time.monotonic() - t0) * 1000.0
    ack = now_event(
        "cdp", method, tab=tab, phase="ack",
        rc=rc, latency_ms=round(latency_ms, 2),
        stdout_tail=out[-200:] if out else "",
        stderr_tail=err[-200:] if err else "",
    )
    await events.emit(ack)
    return {"rc": rc, "out": out, "err": err}


async def in_jupyter(events: EventLog, cmd: str, timeout: float = 30.0) -> dict:
    rc, out, err = await run_proc("podman", "exec", JUPYTER_CONTAINER, "bash", "-lc", cmd, timeout=timeout)
    return {"rc": rc, "out": out, "err": err}


async def disk_watcher_tail(events: EventLog, stop: asyncio.Event) -> None:
    """Tail /tmp/disk-events.jsonl from inside the jupyter pod, merge into log."""
    proc = await asyncio.create_subprocess_exec(
        "podman", "exec", JUPYTER_CONTAINER, "tail", "-n", "+1", "-F", "/tmp/disk-events.jsonl",
        stdout=asyncio.subprocess.PIPE, stderr=asyncio.subprocess.PIPE,
    )
    assert proc.stdout is not None
    try:
        while not stop.is_set():
            try:
                line = await asyncio.wait_for(proc.stdout.readline(), timeout=0.5)
            except asyncio.TimeoutError:
                continue
            if not line:
                break
            text = line.decode("utf-8", "replace").strip()
            if not text:
                continue
            try:
                ev = json.loads(text)
            except json.JSONDecodeError:
                ev = {"source": "disk", "raw": text}
            ev.setdefault("ts_mono_ns", time.monotonic_ns())
            await events.emit(ev)
    finally:
        with contextlib.suppress(ProcessLookupError):
            proc.terminate()
        with contextlib.suppress(Exception):
            await asyncio.wait_for(proc.wait(), timeout=2.0)


async def crdt_watcher(events: EventLog, notebook: str, stop: asyncio.Event) -> None:
    """Loop watch_notebook against the MCP server, log every CRDT change event.
    Per /ov-jupyter:jupyter, watch_notebook blocks server-side for the requested
    timeout — make the subprocess timeout slightly longer than the MCP timeout."""
    while not stop.is_set():
        result = await mcp_call(
            events, "watch-observer", "watch_notebook",
            {"path": notebook, "timeout": 5},
            timeout=15.0,
        )
        try:
            payload = json.loads(result["out"]) if result["out"].strip().startswith("{") else None
        except json.JSONDecodeError:
            payload = None
        ev = now_event(
            "crdt", "watch_notebook_return",
            session="watch-observer",
            payload=payload,
            stdout_tail=result["out"][:500],
        )
        await events.emit(ev)


async def setup_notebook(events: EventLog, scenario: str) -> str:
    """Copy seed.ipynb into the pod's workspace as <scenario>.ipynb,
    delete any prior CRDT log, open a fresh collaboration room.
    Returns the notebook path used inside the pod."""
    notebook_name = f"jc-{scenario}.ipynb"
    seed_local = Path(__file__).parent / "seed.ipynb"
    seed_b64 = ""
    import base64
    seed_b64 = base64.b64encode(seed_local.read_bytes()).decode("ascii")
    install_cmd = (
        f'mkdir -p {WORKSPACE} && '
        f'echo {shlex.quote(seed_b64)} | base64 -d > {WORKSPACE}/{notebook_name} && '
        f'rm -f {WORKSPACE}/.notebook:*.y 2>/dev/null; '
        f'rm -f $HOME/.jupyter_ystore.db* 2>/dev/null; '
        f'true'
    )
    await events.emit(now_event("setup", "install_seed", scenario=scenario, notebook=notebook_name))
    res = await in_jupyter(events, install_cmd, timeout=15.0)
    if res["rc"] != 0:
        await events.emit(now_event("setup", "install_failed", stderr=res["err"][:500]))
    # Open a CRDT room — first call after container startup needs a longer
    # timeout since jupyter-collaboration warms its YDoc machinery on first hit.
    await mcp_call(events, "setup", "open_notebook_session", {"path": notebook_name}, timeout=60.0)
    return notebook_name


async def teardown_notebook(events: EventLog, notebook: str) -> None:
    await mcp_call(events, "teardown", "close_notebook_session", {"path": notebook}, timeout=30.0)


async def fetch_disk_state(notebook: str) -> dict:
    """Read the .ipynb from disk inside the pod. Returns the parsed JSON or
    {error:...} if it doesn't parse."""
    rc, out, err = await run_proc(
        "podman", "exec", JUPYTER_CONTAINER, "cat", f"{WORKSPACE}/{notebook}", timeout=10.0,
    )
    if rc != 0:
        return {"error": f"cat rc={rc}: {err[:200]}"}
    try:
        return json.loads(out)
    except json.JSONDecodeError as e:
        return {"error": f"json parse failed: {e}", "raw": out[:500]}


# ── Scenarios ────────────────────────────────────────────────────────────

async def scenario_disjoint_cells(events: EventLog, args, notebook: str) -> dict:
    """N MCP writers each editing a distinct cell index."""
    nonces = [f"nonce-{uuid.uuid4().hex[:8]}-w{i}" for i in range(args.mcp_writers)]
    # Seed has 3 cells (idx 0,1,2). For N>3 we insert at index=cell_count for "end".
    # The MCP server's insert_cell rejects index=-1 with "Index out of range".
    if args.mcp_writers > 3:
        for i in range(args.mcp_writers - 3):
            await mcp_call(events, "setup", "insert_cell",
                           {"path": notebook, "index": 3 + i, "source": "# placeholder", "cell_type": "code"})
    tasks = [
        mcp_call(events, f"mcp-{i}", "update_cell",
                 {"path": notebook, "index": i, "source": f"# {nonces[i]}", "cell_type": "code"})
        for i in range(args.mcp_writers)
    ]
    await asyncio.gather(*tasks)
    return {"nonces": nonces, "expected_min_cells": max(3, args.mcp_writers)}


async def scenario_same_cell_mcp(events: EventLog, args, notebook: str) -> dict:
    """All N MCP writers race to update_cell(index=0)."""
    nonces = [f"nonce-{uuid.uuid4().hex[:8]}-w{i}" for i in range(args.mcp_writers)]
    tasks = [
        mcp_call(events, f"mcp-{i}", "update_cell",
                 {"path": notebook, "index": 0, "source": f"# {nonces[i]}", "cell_type": "code"})
        for i in range(args.mcp_writers)
    ]
    await asyncio.gather(*tasks)
    return {"nonces": nonces, "race_target": "cell[0]"}


async def scenario_insert_at_zero(events: EventLog, args, notebook: str) -> dict:
    """All N MCP writers race to insert_cell(index=0)."""
    nonces = [f"nonce-{uuid.uuid4().hex[:8]}-w{i}" for i in range(args.mcp_writers)]
    tasks = [
        mcp_call(events, f"mcp-{i}", "insert_cell",
                 {"path": notebook, "index": 0, "source": f"# {nonces[i]}", "cell_type": "code"})
        for i in range(args.mcp_writers)
    ]
    await asyncio.gather(*tasks)
    return {"nonces": nonces, "expected_min_cells": 3 + args.mcp_writers}


async def scenario_delete_vs_edit(events: EventLog, args, notebook: str) -> dict:
    """1 MCP writer deletes cell K while N-1 MCP writers update_cell(K)."""
    K = 1
    nonces = [f"nonce-{uuid.uuid4().hex[:8]}-w{i}" for i in range(args.mcp_writers - 1)]
    delete_task = mcp_call(events, "mcp-deleter", "delete_cell", {"path": notebook, "index": K})
    edit_tasks = [
        mcp_call(events, f"mcp-{i}", "update_cell",
                 {"path": notebook, "index": K, "source": f"# {nonces[i]}", "cell_type": "code"})
        for i in range(args.mcp_writers - 1)
    ]
    results = await asyncio.gather(delete_task, *edit_tasks, return_exceptions=True)
    return {"nonces": nonces, "K": K, "results_summary": [str(r)[:80] for r in results]}


async def scenario_read_during_write(events: EventLog, args, notebook: str) -> dict:
    """N writers + 1 reader polling get_cell at 50ms; reader logs every observation."""
    nonces = [f"nonce-{uuid.uuid4().hex[:8]}-w{i}" for i in range(args.mcp_writers)]
    stop = asyncio.Event()

    async def reader():
        observations: list[dict] = []
        while not stop.is_set():
            r = await mcp_call(events, "mcp-reader", "get_cell",
                               {"path": notebook, "index": 0}, timeout=10.0)
            try:
                cell = json.loads(r["out"]) if r["out"].strip().startswith("{") else None
            except json.JSONDecodeError:
                cell = None
            observations.append({"ts_ns": time.monotonic_ns(), "cell": cell})
            await asyncio.sleep(0.05)
        return observations

    reader_task = asyncio.create_task(reader())

    async def burst():
        for i, n in enumerate(nonces):
            await mcp_call(events, f"mcp-{i}", "update_cell",
                           {"path": notebook, "index": 0, "source": f"# {n}", "cell_type": "code"})
            await asyncio.sleep(0.03)

    burst_tasks = [asyncio.create_task(burst()) for _ in range(args.mcp_writers)]
    await asyncio.gather(*burst_tasks)
    await asyncio.sleep(0.5)
    stop.set()
    obs = await reader_task
    return {"nonces": nonces, "reader_observations": len(obs)}


async def scenario_execute_vs_edit(events: EventLog, args, notebook: str) -> dict:
    """1 MCP writer execute_cell(K) while N-1 update_cell(K)."""
    K = 1
    nonces = [f"nonce-{uuid.uuid4().hex[:8]}-w{i}" for i in range(args.mcp_writers - 1)]
    # Pre-load cell K with executable code.
    await mcp_call(events, "setup", "update_cell",
                   {"path": notebook, "index": K, "source": "import time; time.sleep(0.5); 42",
                    "cell_type": "code"})
    exec_task = mcp_call(events, "mcp-executor", "execute_cell",
                         {"path": notebook, "index": K}, timeout=20.0)
    edit_tasks = [
        mcp_call(events, f"mcp-{i}", "update_cell",
                 {"path": notebook, "index": K, "source": f"# {nonces[i]}", "cell_type": "code"})
        for i in range(args.mcp_writers - 1)
    ]
    results = await asyncio.gather(exec_task, *edit_tasks, return_exceptions=True)
    return {"nonces": nonces, "K": K, "results_summary": [str(r)[:80] for r in results]}


async def scenario_burst(events: EventLog, args, notebook: str) -> dict:
    """5 writers × 20 ops = 100 update_cell ops on cell[0]."""
    ops_per_writer = 20
    n_writers = max(args.mcp_writers, 5)
    nonces = [f"nonce-{uuid.uuid4().hex[:8]}-w{w}-o{o}"
              for w in range(n_writers) for o in range(ops_per_writer)]

    async def writer(w: int):
        for o in range(ops_per_writer):
            n = nonces[w * ops_per_writer + o]
            await mcp_call(events, f"mcp-{w}", "update_cell",
                           {"path": notebook, "index": 0, "source": f"# {n}", "cell_type": "code"})

    await asyncio.gather(*[writer(w) for w in range(n_writers)])
    return {"nonces": nonces, "total_ops": len(nonces)}


async def scenario_mixed(events: EventLog, args, notebook: str) -> dict:
    """N MCP writers + M CDP-driven keystroke streams target the same cell.
    The CDP path goes through the JupyterLab WebSocket, NOT the MCP per-notebook
    asyncio lock — so this is the only scenario that exercises a true CRDT-merge
    race between the two write paths."""
    mcp_nonces = [f"mcp-nonce-{uuid.uuid4().hex[:8]}-w{i}" for i in range(args.mcp_writers)]
    cdp_nonces = [f"cdp-nonce-{uuid.uuid4().hex[:8]}-t{i}" for i in range(args.cdp_tabs)]

    # Open M browser tabs against the notebook, gathering tab IDs.
    tab_ids: list[str] = []
    notebook_url = f"http://{JUPYTER_CONTAINER}:8888/lab/tree/{notebook}"
    for i in range(args.cdp_tabs):
        r = await cdp_invoke(events, "", "open", "--url", notebook_url)
        # `cdp open` prints the tab id on stdout (or {"id":..} json). Extract.
        tab_id = ""
        for line in (r["out"] or "").splitlines():
            line = line.strip()
            if line and (line.isalnum() or line.startswith("{")):
                if line.startswith("{"):
                    try:
                        tab_id = json.loads(line).get("id", "")
                    except json.JSONDecodeError:
                        pass
                else:
                    tab_id = line
                if tab_id:
                    break
        tab_ids.append(tab_id)
        await events.emit(now_event("setup", "tab_opened", tab=tab_id, url=notebook_url))
        await asyncio.sleep(0.5)  # let collaboration register

    # Wait a moment for jupyter-collaboration to attach all tabs.
    await asyncio.sleep(2.0)

    async def cdp_writer(i: int):
        tab = tab_ids[i] if i < len(tab_ids) and tab_ids[i] else ""
        if not tab:
            await events.emit(now_event("cdp", "skip_no_tab", tab_index=i))
            return
        # Focus a code cell input area, then dispatch keystrokes that produce
        # the nonce. Mirrors the documented /ov-jupyter:jupyter pattern.
        nonce = cdp_nonces[i]
        focus_expr = "document.querySelector('.jp-Cell-inputArea .cm-content')?.focus()"
        await cdp_invoke(events, tab, "eval", "--expression", focus_expr)
        for ch in nonce:
            # ascii printable; use cdp type for whole strings
            pass
        await cdp_invoke(events, tab, "type", "--text", f"# {nonce}\n")

    async def mcp_writer(i: int):
        await mcp_call(events, f"mcp-{i}", "update_cell",
                       {"path": notebook, "index": 0, "source": f"# {mcp_nonces[i]}",
                        "cell_type": "code"})

    await asyncio.gather(
        *[mcp_writer(i) for i in range(args.mcp_writers)],
        *[cdp_writer(i) for i in range(args.cdp_tabs)],
    )

    return {"mcp_nonces": mcp_nonces, "cdp_nonces": cdp_nonces, "tab_ids": tab_ids}


async def scenario_data_safety_kill(events: EventLog, args, notebook: str) -> dict:
    """Burst from scenario_burst, then podman kill -s KILL mid-flight, then re-run
    a fresh container with the same name, then verify acked-before-kill ops survive."""
    ops_per_writer = 20
    n_writers = max(args.mcp_writers, 5)
    nonces = [f"nonce-{uuid.uuid4().hex[:8]}-w{w}-o{o}"
              for w in range(n_writers) for o in range(ops_per_writer)]
    acked: list[str] = []
    acked_lock = asyncio.Lock()

    async def writer(w: int):
        for o in range(ops_per_writer):
            n = nonces[w * ops_per_writer + o]
            r = await mcp_call(events, f"mcp-{w}", "update_cell",
                               {"path": notebook, "index": 0, "source": f"# {n}",
                                "cell_type": "code"}, timeout=10.0)
            if r["rc"] == 0:
                async with acked_lock:
                    acked.append(n)

    burst_tasks = [asyncio.create_task(writer(w)) for w in range(n_writers)]

    # Kill mid-burst — sleep enough that writers can complete several ops
    # apiece (with ~1.4s save-lag latency, 5s lets each writer rack up
    # multiple acks before kill, making the survival metric meaningful).
    await asyncio.sleep(5.0)
    await events.emit(now_event("data_safety", "killing", container=JUPYTER_CONTAINER))
    rc, out, err = await run_proc("podman", "kill", "-s", "KILL", JUPYTER_CONTAINER, timeout=10.0)
    await events.emit(now_event("data_safety", "killed", rc=rc, err=err[:200]))
    # Force-remove. --rm removes asynchronously; we need synchronous cleanup
    # before respawning under the same name.
    rc_rm, _, err_rm = await run_proc("podman", "rm", "-f", JUPYTER_CONTAINER, timeout=15.0)
    await events.emit(now_event("data_safety", "removed", rc=rc_rm, err=err_rm[:200]))

    # Cancel pending burst tasks (they will hit MCP errors as the pod is gone).
    for t in burst_tasks:
        t.cancel()
    with contextlib.suppress(BaseException):
        await asyncio.gather(*burst_tasks, return_exceptions=True)

    # Re-run the container fresh — same name, same image. The workspace
    # bind mount (or in-image workspace) preserves the .ipynb + .notebook:*.y
    # files, so the CRDT log is available for replay on cold start.
    await events.emit(now_event("data_safety", "respawning"))
    image_ref = os.environ.get("JC_JUPYTER_REF", f"ghcr.io/overthinkos/{JUPYTER_IMAGE}:latest")
    host_port = os.environ.get("JC_JUPYTER_HOST_PORT", "18888")
    workspace_volume = os.environ.get("JC_JUPYTER_WORKSPACE_VOLUME", f"jc-{INSTANCE}-workspace")
    rc, out, err = await run_proc(
        "podman", "run", "-d", "--rm",
        "--name", JUPYTER_CONTAINER,
        "--network", "ov",
        "-p", f"{host_port}:8888",
        "-v", f"{workspace_volume}:/home/user/workspace",
        "-e", f"MCP_SERVER_NAME={JUPYTER_IMAGE}",
        image_ref,
        "supervisord", "-n", "-c", "/etc/supervisord.conf",
        timeout=60.0,
    )
    await events.emit(now_event("data_safety", "respawned", rc=rc, err=err[-300:], stdout=out[-300:]))
    # Wait for jupyter to come up and replay the CRDT log from the YStore.
    for _ in range(30):
        await asyncio.sleep(1.0)
        rc_p, _, _ = await run_proc(
            "curl", "-fsS", f"http://127.0.0.1:{host_port}/api", timeout=3.0)
        if rc_p == 0:
            break
    # Re-install the disk watcher (the previous one died with the container).
    seed_local = Path(__file__).parent / "disk_watcher.sh"
    if seed_local.exists():
        import base64
        watcher_b64 = base64.b64encode(seed_local.read_bytes()).decode("ascii")
        await run_proc(
            "podman", "exec", JUPYTER_CONTAINER, "bash", "-c",
            f"echo {shlex.quote(watcher_b64)} | base64 -d > /tmp/disk_watcher.sh && chmod +x /tmp/disk_watcher.sh",
            timeout=10.0,
        )
    await mcp_call(events, "post-kill", "open_notebook_session",
                   {"path": notebook}, timeout=30.0)
    return {"nonces": nonces, "acked_before_kill": acked, "total_ops": len(nonces)}


SCENARIOS: dict[str, Any] = {
    "disjoint-cells": scenario_disjoint_cells,
    "same-cell-mcp": scenario_same_cell_mcp,
    "insert-at-zero": scenario_insert_at_zero,
    "delete-vs-edit": scenario_delete_vs_edit,
    "read-during-write": scenario_read_during_write,
    "execute-vs-edit": scenario_execute_vs_edit,
    "burst": scenario_burst,
    "mixed-mcp-cdp": scenario_mixed,
    "data-safety-kill": scenario_data_safety_kill,
}


async def run_scenario(events: EventLog, args, name: str) -> dict:
    fn = SCENARIOS[name]
    notebook = await setup_notebook(events, name)

    # Start disk watcher inside the pod. /tmp/disk_watcher.sh is installed
    # by run.sh before the orchestrator runs. Use podman exec -d for truly
    # detached execution — `nohup ... &` inside `podman exec bash -lc` dies
    # when the exec session ends.
    await in_jupyter(events,
                     "pkill -f disk_watcher.sh 2>/dev/null; rm -f /tmp/disk-events.jsonl; true",
                     timeout=10.0)
    await run_proc(
        "podman", "exec", "-d", JUPYTER_CONTAINER,
        "bash", "-c", f"bash /tmp/disk_watcher.sh {WORKSPACE} {notebook} > /tmp/disk-watcher.out 2> /tmp/disk-watcher.err",
        timeout=5.0,
    )
    await asyncio.sleep(0.5)  # let it install initial state

    stop = asyncio.Event()
    tail_task = asyncio.create_task(disk_watcher_tail(events, stop))
    crdt_task = asyncio.create_task(crdt_watcher(events, notebook, stop))

    try:
        await events.emit(now_event("scenario", "start", name=name))
        scenario_state = await fn(events, args, notebook)
        await events.emit(now_event("scenario", "ops_complete", name=name))
        # Cooldown: let autosave fire.
        await asyncio.sleep(2.5)
        final_state = await fetch_disk_state(notebook)
        await events.emit(now_event("scenario", "end", name=name,
                                    final_cells=len(final_state.get("cells", [])) if "cells" in final_state else None))
    finally:
        stop.set()
        with contextlib.suppress(BaseException):
            await tail_task
        with contextlib.suppress(BaseException):
            crdt_task.cancel()
            await crdt_task
        with contextlib.suppress(BaseException):
            await teardown_notebook(events, notebook)
        await in_jupyter(events, "pkill -f disk_watcher.sh || true", timeout=5.0)

    return {"scenario": name, "scenario_state": scenario_state, "final_state": final_state}


async def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("--scenarios", default=",".join(SCENARIOS.keys()))
    p.add_argument("--mcp-writers", type=int, default=4)
    p.add_argument("--cdp-tabs", type=int, default=2)
    p.add_argument("--out-dir", default=str(Path(__file__).parent / "reports" / time.strftime("%Y%m%dT%H%M%SZ", time.gmtime())))
    args = p.parse_args()

    out_dir = Path(args.out_dir)
    out_dir.mkdir(parents=True, exist_ok=True)
    log_path = out_dir / "events.jsonl"
    summary_path = out_dir / "summary.json"

    events = EventLog(log_path)
    await events.emit(now_event("run", "start", args=vars(args)))

    summaries: dict[str, Any] = {}
    for name in args.scenarios.split(","):
        name = name.strip()
        if not name:
            continue
        if name not in SCENARIOS:
            print(f"unknown scenario: {name}", file=sys.stderr)
            continue
        print(f"[scenario] {name}", file=sys.stderr)
        try:
            summaries[name] = await run_scenario(events, args, name)
        except Exception as e:
            await events.emit(now_event("scenario", "exception", name=name, error=str(e)[:300]))
            summaries[name] = {"scenario": name, "exception": str(e)}

    await events.emit(now_event("run", "end"))
    events.close()
    summary_path.write_text(json.dumps(summaries, default=str, indent=2))
    print(f"[done] log={log_path} summary={summary_path}", file=sys.stderr)
    return 0


if __name__ == "__main__":
    sys.exit(asyncio.run(main()))
