"""FastMCP server definition with tools for notebook manipulation via CRDT.

Tools are organized into three groups:
  - Notebook management (list, get, create)
  - Cell operations (get, update, insert, delete, execute) — mutations sync live
  - Collaboration awareness (active users, active sessions)
"""

from __future__ import annotations

import os
from typing import Any

from fastmcp import FastMCP

from .rtc_adapter import RTCAdapter


def create_mcp_server(adapter: RTCAdapter) -> FastMCP:
    """Create and configure the FastMCP server with all tools."""
    mcp = FastMCP(
        os.environ.get("MCP_SERVER_NAME", "jupyter"),
        instructions=(
            "JupyterLab MCP server with real-time collaboration. "
            "Cell operations mutate the live CRDT document — changes appear "
            "instantly in all connected JupyterLab clients. "
            "Notebooks must be open in JupyterLab for CRDT tools to work."
        ),
    )

    # ── Notebook management ──────────────────────────────────────────

    @mcp.tool()
    async def list_notebooks() -> list[dict[str, str]]:
        """List all notebooks accessible in the workspace.

        Returns a list of dicts with 'path' and 'name' for each notebook.
        """
        return await adapter.list_notebooks()

    @mcp.tool()
    async def get_notebook(path: str) -> dict[str, Any] | None:
        """Get full notebook content (cells, metadata, kernel info).

        If the notebook is open in a collaboration session, returns the live
        CRDT state. Otherwise, reads from disk.

        Args:
            path: Notebook path relative to the workspace root (e.g. "analysis.ipynb")
        """
        return await adapter.get_notebook(path)

    @mcp.tool()
    async def create_notebook(path: str) -> dict[str, str]:
        """Create a new empty notebook.

        Args:
            path: Path for the new notebook (e.g. "experiments/new.ipynb")

        Returns:
            Dict with 'path' and 'name' of the created notebook.
        """
        return await adapter.create_notebook(path)

    # ── Cell operations (CRDT — changes sync to all collaborators) ───

    @mcp.tool()
    async def get_cell(path: str, index: int) -> dict[str, Any]:
        """Get a specific cell's content from an open notebook.

        The notebook must be open in JupyterLab (CRDT room must exist).

        Args:
            path: Notebook path relative to workspace root
            index: Zero-based cell index

        Returns:
            Cell dict with 'source', 'cell_type', 'metadata', and
            'outputs'/'execution_count' for code cells.
        """
        return await adapter.get_cell(path, index)

    @mcp.tool()
    async def update_cell(
        path: str,
        index: int,
        source: str,
        cell_type: str | None = None,
    ) -> str:
        """Update a cell's content. The change syncs live to all collaborators.

        Args:
            path: Notebook path
            index: Zero-based cell index
            source: New cell source code/text
            cell_type: Optional new cell type ("code", "markdown", "raw").
                       If not provided, keeps the existing type.
        """
        existing = await adapter.get_cell(path, index)
        cell_value = {
            "cell_type": cell_type or existing["cell_type"],
            "source": source,
            "metadata": existing.get("metadata", {}),
        }
        if cell_value["cell_type"] == "code":
            cell_value["outputs"] = []
            cell_value["execution_count"] = None
        await adapter.set_cell(path, index, cell_value)
        return f"Cell {index} updated"

    @mcp.tool()
    async def insert_cell(
        path: str,
        index: int,
        source: str,
        cell_type: str = "code",
    ) -> str:
        """Insert a new cell at the given position. Syncs live to all collaborators.

        Args:
            path: Notebook path
            index: Position to insert at (0 = beginning, -1 or cell_count = end)
            source: Cell source code/text
            cell_type: Cell type — "code" (default), "markdown", or "raw"
        """
        await adapter.insert_cell(path, index, source, cell_type)
        return f"Cell inserted at index {index}"

    @mcp.tool()
    async def delete_cell(path: str, index: int) -> str:
        """Delete a cell from an open notebook. Syncs live to all collaborators.

        Args:
            path: Notebook path
            index: Zero-based index of the cell to delete
        """
        await adapter.delete_cell(path, index)
        return f"Cell {index} deleted"

    @mcp.tool()
    async def execute_cell(path: str, index: int) -> list[dict[str, Any]]:
        """Execute a cell and return its outputs.

        Starts a kernel if one isn't already running for this notebook.

        Args:
            path: Notebook path
            index: Zero-based index of the cell to execute

        Returns:
            List of output dicts, each with 'type' (stream, display_data,
            execute_result, error) and 'content'.
        """
        return await adapter.execute_cell(path, index)

    # ── Collaboration awareness ──────────────────────────────────────

    @mcp.tool()
    async def get_active_users() -> list[dict[str, str]]:
        """List users currently connected via real-time collaboration.

        Returns a list of dicts with 'id' and 'name' for each user.
        """
        return await adapter.get_active_users()

    @mcp.tool()
    async def get_active_sessions() -> list[dict[str, str]]:
        """List active collaboration sessions (open documents).

        Returns a list of dicts with 'room_id' identifying each active
        document session.
        """
        return await adapter.get_active_sessions()

    # ── Room management ──────────────────────────────────────────────

    @mcp.tool()
    async def open_notebook_session(path: str) -> str:
        """Open a notebook and create a CRDT collaboration room.

        This makes the notebook available for real-time cell operations
        (get_cell, update_cell, etc.) without needing a browser open.
        The room persists until close_notebook_session is called.

        Args:
            path: Notebook path relative to workspace root
        """
        await adapter.open_room(path)
        return f"Collaboration room opened for {path}"

    @mcp.tool()
    async def close_notebook_session(path: str) -> str:
        """Close a notebook's CRDT collaboration room and save to disk.

        Args:
            path: Notebook path relative to workspace root
        """
        await adapter.close_room(path)
        return f"Collaboration room closed for {path}"

    # ── Change watching ──────────────────────────────────────────────

    @mcp.tool()
    async def watch_notebook(path: str, timeout: int = 30) -> dict[str, Any]:
        """Watch for changes to a notebook. Blocks until a cell is changed
        by another client or a human in JupyterLab, or until timeout expires.

        Use this to get notified of collaborative edits in real-time.
        The notebook must have an open session (call open_notebook_session first).

        Multiple clients can watch the same notebook simultaneously — each
        gets independently notified.

        Args:
            path: Notebook path relative to workspace root
            timeout: Seconds to wait before returning (default: 30, max: 300)

        Returns:
            {"changed": true, "cell_count": N} if a change was detected,
            {"changed": false} if timeout expired with no changes.
        """
        clamped_timeout = min(max(timeout, 1), 300)
        return await adapter.watch_notebook(path, timeout=clamped_timeout)

    return mcp
