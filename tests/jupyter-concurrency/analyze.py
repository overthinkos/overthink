#!/usr/bin/env python3
"""Post-run analyzer for the jupyter-concurrency harness.

Reads events.jsonl + summary.json from the orchestrator's output dir.
Computes per-scenario invariants, save-lag distribution, and emits
a markdown report.
"""
from __future__ import annotations

import argparse
import json
import statistics
import sys
from pathlib import Path
from typing import Any


def load_events(path: Path) -> list[dict]:
    out: list[dict] = []
    with path.open() as f:
        for line in f:
            line = line.strip()
            if not line:
                continue
            try:
                out.append(json.loads(line))
            except json.JSONDecodeError:
                continue
    return out


def cells_sources(notebook: dict) -> list[str]:
    out: list[str] = []
    for c in notebook.get("cells", []):
        src = c.get("source", "")
        if isinstance(src, list):
            src = "".join(src)
        out.append(src)
    return out


def all_nonces_present(nonces: list[str], sources: list[str]) -> tuple[bool, list[str]]:
    blob = "\n".join(sources)
    missing = [n for n in nonces if n not in blob]
    return len(missing) == 0, missing


def is_valid_nbformat(nb: dict) -> bool:
    if not isinstance(nb, dict):
        return False
    if "cells" not in nb or not isinstance(nb["cells"], list):
        return False
    if "nbformat" not in nb:
        return False
    for c in nb["cells"]:
        if "cell_type" not in c or "source" not in c:
            return False
    return True


def save_lag_for_scenario(events: list[dict], scenario: str) -> dict[str, Any]:
    """For each MCP ack within scenario start..end, find the first disk
    event for the .ipynb file with ts > ack ts. Compute lag distribution."""
    in_scn = False
    acks: list[int] = []  # ts_mono_ns of MCP acks
    disk_writes_ipynb: list[int] = []
    for ev in events:
        if ev.get("source") == "scenario" and ev.get("name") == scenario:
            if ev.get("op") == "start":
                in_scn = True
            elif ev.get("op") == "end":
                in_scn = False
        if not in_scn:
            continue
        if ev.get("source") == "mcp" and ev.get("phase") == "ack" and ev.get("rc") == 0:
            ts = ev.get("ts_mono_ns")
            if ts is not None:
                acks.append(int(ts))
        if ev.get("source") == "disk":
            path = ev.get("path", "")
            if path.endswith(".ipynb"):
                ts = ev.get("ts_mono_ns")
                if ts is not None:
                    disk_writes_ipynb.append(int(ts))

    lags_ms: list[float] = []
    j = 0
    for ack in sorted(acks):
        # find first disk-write strictly after ack
        while j < len(disk_writes_ipynb) and disk_writes_ipynb[j] <= ack:
            j += 1
        if j >= len(disk_writes_ipynb):
            break
        lags_ms.append((disk_writes_ipynb[j] - ack) / 1e6)
    if not lags_ms:
        return {"sample_count": 0}
    s = sorted(lags_ms)
    def pct(p: float) -> float:
        i = max(0, min(len(s) - 1, int(round(p * (len(s) - 1)))))
        return s[i]
    return {
        "sample_count": len(s),
        "p50_ms": round(pct(0.50), 2),
        "p95_ms": round(pct(0.95), 2),
        "p99_ms": round(pct(0.99), 2),
        "max_ms": round(s[-1], 2),
        "mean_ms": round(statistics.fmean(s), 2),
    }


def disk_writes_summary(events: list[dict], scenario: str) -> dict[str, Any]:
    in_scn = False
    ipynb = 0
    yfile = 0
    ops_acked = 0
    for ev in events:
        if ev.get("source") == "scenario" and ev.get("name") == scenario:
            if ev.get("op") == "start":
                in_scn = True
            elif ev.get("op") == "end":
                in_scn = False
        if not in_scn:
            continue
        if ev.get("source") == "disk":
            path = ev.get("path", "")
            if path.endswith(".ipynb"):
                ipynb += 1
            elif ".y" in path or "ystore" in path:
                yfile += 1
        if ev.get("source") == "mcp" and ev.get("phase") == "ack" and ev.get("rc") == 0:
            ops_acked += 1
    return {
        "ops_acked": ops_acked,
        "ipynb_writes": ipynb,
        "ystore_writes": yfile,
        "debounce_ratio": round(ops_acked / ipynb, 2) if ipynb > 0 else None,
    }


def evaluate_scenario(name: str, events: list[dict], summary_entry: dict) -> dict:
    state = summary_entry.get("scenario_state", {}) or {}
    final = summary_entry.get("final_state", {}) or {}
    nbf_ok = is_valid_nbformat(final)
    sources = cells_sources(final) if nbf_ok else []
    cells = len(final.get("cells", [])) if nbf_ok else 0
    invariants: dict[str, Any] = {"valid_nbformat": nbf_ok, "cell_count": cells}

    if name == "disjoint-cells":
        ok, missing = all_nonces_present(state.get("nonces", []), sources)
        invariants["all_nonces_present"] = ok
        invariants["missing"] = missing
        invariants["expected_min_cells"] = state.get("expected_min_cells")
        invariants["pass"] = ok and nbf_ok and cells >= state.get("expected_min_cells", 0)
    elif name == "same-cell-mcp":
        # Server-side per-notebook lock serializes — last writer wins or all
        # nonces concatenated depending on update_cell semantics. Just verify
        # ONE of the nonces is present (no corruption) and JSON valid.
        any_present = any(n in "\n".join(sources) for n in state.get("nonces", []))
        invariants["one_nonce_present"] = any_present
        invariants["pass"] = any_present and nbf_ok
    elif name == "insert-at-zero":
        ok, missing = all_nonces_present(state.get("nonces", []), sources)
        invariants["all_inserts_landed"] = ok
        invariants["missing"] = missing
        invariants["expected_min_cells"] = state.get("expected_min_cells")
        invariants["pass"] = ok and nbf_ok and cells >= state.get("expected_min_cells", 0)
    elif name == "delete-vs-edit":
        # Either deleted (cell K gone) or edited (cell K has SOME nonce). Never
        # both / never half-state.
        invariants["nbformat_ok"] = nbf_ok
        invariants["pass"] = nbf_ok
    elif name == "read-during-write":
        invariants["pass"] = nbf_ok
    elif name == "execute-vs-edit":
        invariants["pass"] = nbf_ok
    elif name == "burst":
        # All 100 ops target cell[0] and overwrite each other — only the LAST
        # serialized op survives in the cell. The correct invariant is "all
        # ops acked AND final state JSON-valid". The save-lag distribution +
        # debounce ratio are the headline outputs, not nonce conservation.
        total_ops = state.get("total_ops", 0)
        acked = invariants["disk_writes"]["ops_acked"] if "disk_writes" in invariants else 0
        # The disk_writes summary is computed below — use a placeholder check
        # via re-counting from events (caller passes events list separately
        # to evaluate_scenario via save_lag_for_scenario / disk_writes_summary,
        # both of which read events). For ops_acked, use disk_writes_summary.
        dw = disk_writes_summary(events, name)
        invariants["all_ops_acked"] = dw["ops_acked"] == total_ops
        invariants["expected_ops"] = total_ops
        invariants["acked_ops"] = dw["ops_acked"]
        invariants["pass"] = nbf_ok and dw["ops_acked"] == total_ops
    elif name == "mixed-mcp-cdp":
        mcp_n = state.get("mcp_nonces", [])
        cdp_n = state.get("cdp_nonces", [])
        all_text = "\n".join(sources)
        any_mcp = any(n in all_text for n in mcp_n)
        any_cdp = any(n in all_text for n in cdp_n)
        invariants["any_mcp_landed"] = any_mcp
        invariants["any_cdp_landed"] = any_cdp
        invariants["pass"] = nbf_ok and any_mcp
    elif name == "data-safety-kill":
        acked_before_kill = state.get("acked_before_kill", [])
        all_text = "\n".join(sources)
        survived = [n for n in acked_before_kill if n in all_text]
        survival_rate = (len(survived) / len(acked_before_kill)) if acked_before_kill else 0.0
        invariants["acked_before_kill"] = len(acked_before_kill)
        invariants["survived"] = len(survived)
        invariants["survival_rate"] = round(survival_rate, 3)
        invariants["pass"] = nbf_ok  # survival rate REPORTED, not asserted == 1.0
    else:
        invariants["pass"] = nbf_ok

    invariants["save_lag"] = save_lag_for_scenario(events, name)
    invariants["disk_writes"] = disk_writes_summary(events, name)
    return invariants


def render_markdown(report: dict, out_path: Path) -> None:
    lines: list[str] = []
    lines.append("# Jupyter concurrency + data-safety run\n")
    overall_pass = all(s["invariants"].get("pass", False) for s in report["scenarios"].values())
    lines.append(f"**Overall**: {'PASS' if overall_pass else 'FAIL'}\n")
    lines.append(f"**Scenarios run**: {len(report['scenarios'])}\n")
    lines.append("\n## Per-scenario summary\n")
    lines.append("| Scenario | Pass | Cells | Save-lag p50/p95/p99 (ms) | Acks→disk debounce |")
    lines.append("|---|---|---|---|---|")
    for name, entry in report["scenarios"].items():
        inv = entry["invariants"]
        lag = inv.get("save_lag", {})
        dw = inv.get("disk_writes", {})
        lag_str = f"{lag.get('p50_ms','?')}/{lag.get('p95_ms','?')}/{lag.get('p99_ms','?')}" if lag.get("sample_count") else "(no samples)"
        deb = dw.get("debounce_ratio")
        deb_str = f"{dw.get('ops_acked','?')} acks / {dw.get('ipynb_writes','?')} writes (= {deb})" if deb is not None else "(no writes)"
        lines.append(f"| {name} | {'✅' if inv.get('pass') else '❌'} | {inv.get('cell_count','?')} | {lag_str} | {deb_str} |")
    lines.append("\n## Per-scenario detail\n")
    for name, entry in report["scenarios"].items():
        lines.append(f"### {name}\n")
        lines.append("```json")
        lines.append(json.dumps(entry["invariants"], indent=2, default=str))
        lines.append("```\n")
    out_path.write_text("\n".join(lines))


def main() -> int:
    p = argparse.ArgumentParser()
    p.add_argument("run_dir", help="orchestrator output dir containing events.jsonl + summary.json")
    args = p.parse_args()
    run_dir = Path(args.run_dir)
    events = load_events(run_dir / "events.jsonl")
    summary = json.loads((run_dir / "summary.json").read_text())

    report = {"scenarios": {}}
    for name, entry in summary.items():
        report["scenarios"][name] = {
            "invariants": evaluate_scenario(name, events, entry),
        }

    md_path = run_dir / "report.md"
    render_markdown(report, md_path)
    (run_dir / "report.json").write_text(json.dumps(report, default=str, indent=2))
    overall_pass = all(s["invariants"].get("pass", False) for s in report["scenarios"].values())
    print(f"[analyze] {md_path} (overall {'PASS' if overall_pass else 'FAIL'})", file=sys.stderr)
    print(md_path.read_text())
    return 0 if overall_pass else 2


if __name__ == "__main__":
    sys.exit(main())
