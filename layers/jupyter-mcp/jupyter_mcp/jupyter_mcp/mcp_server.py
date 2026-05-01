"""FastMCP server definition with tools for notebook manipulation via CRDT.

Tool naming convention: every tool uses ``<noun>_<verb>`` form.
Three nouns partition the catalog:

  - ``notebook_*`` — filesystem operations on .ipynb files
  - ``cell_*``     — in-memory cell mutations (require an open room)
  - ``room_*``     — CRDT room lifecycle and introspection

Room creation is ALWAYS explicit: only ``room_open`` creates a room.
Every room-mutation tool (cell_*, notebook_get, notebook_watch) raises
RoomNotOpenError when the path has no active room — call ``room_open``
first.
"""

from __future__ import annotations

import os
from typing import Any

from fastmcp import FastMCP

from .rtc_adapter import RTCAdapter, RoomNotOpenError


def create_mcp_server(adapter: RTCAdapter) -> FastMCP:
    """Create and configure the FastMCP server with all tools."""
    mcp = FastMCP(
        os.environ.get("MCP_SERVER_NAME", "jupyter"),
        instructions=(
            "JupyterLab MCP server with real-time collaboration. "
            "Tools are prefixed by domain: notebook_* (filesystem), "
            "cell_* (in-memory mutations), room_* (CRDT room lifecycle). "
            "Room creation is explicit: call room_open(path) before any "
            "cell_* or notebook_get/watch operation, and room_close(path) "
            "when done. Cell mutations propagate live to all connected "
            "JupyterLab UI clients via the same CRDT room."
        ),
    )

    # ── notebook_* — filesystem operations ───────────────────────────

    @mcp.tool()
    async def notebook_list() -> list[dict[str, str]]:
        """List all notebooks accessible in the workspace.

        Filesystem-only. Does not touch CRDT rooms.

        Returns a list of dicts with 'path' and 'name' for each notebook.
        """
        return await adapter.list_notebooks()

    @mcp.tool()
    async def notebook_create(path: str) -> dict[str, str]:
        """Create a new empty notebook on disk.

        Filesystem-only. Does NOT open a CRDT room — call ``room_open``
        afterwards if you need to read or mutate cells.

        Args:
            path: Path for the new notebook (e.g. "experiments/new.ipynb")

        Returns:
            Dict with 'path' and 'name' of the created notebook.
        """
        return await adapter.create_notebook(path)

    @mcp.tool()
    async def notebook_get(path: str) -> dict[str, Any] | None:
        """Get full notebook content (cells, metadata, kernel info).

        Requires an open CRDT room for the path. Call ``room_open(path)``
        first; raises RoomNotOpenError otherwise.

        Args:
            path: Notebook path relative to the workspace root
        """
        return await adapter.get_notebook(path)

    @mcp.tool()
    async def notebook_watch(path: str, timeout: int = 30) -> dict[str, Any]:
        """Watch for changes to a notebook. Blocks until a cell is changed
        by another client or a human in JupyterLab, or until timeout.

        Requires an open CRDT room. Call ``room_open(path)`` first.

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

    # ── cell_* — in-memory cell mutations (require open room) ────────

    @mcp.tool()
    async def cell_get(path: str, index: int) -> dict[str, Any]:
        """Get a specific cell's content from an open notebook.

        Requires an open CRDT room. Call ``room_open(path)`` first.

        Args:
            path: Notebook path relative to workspace root
            index: Zero-based cell index

        Returns:
            Cell dict with 'source', 'cell_type', 'metadata', and
            'outputs'/'execution_count' for code cells.
        """
        return await adapter.get_cell(path, index)

    @mcp.tool()
    async def cell_update(
        path: str,
        index: int,
        source: str,
        cell_type: str | None = None,
    ) -> str:
        """Update a cell's content. The change syncs live to all collaborators.

        Requires an open CRDT room. Call ``room_open(path)`` first.

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
    async def cell_insert(
        path: str,
        index: int,
        source: str,
        cell_type: str = "code",
    ) -> str:
        """Insert a new cell at the given position. Syncs live to all collaborators.

        Requires an open CRDT room. Call ``room_open(path)`` first.

        Args:
            path: Notebook path
            index: Position to insert at (0 = beginning, -1 or cell_count = end)
            source: Cell source code/text
            cell_type: Cell type — "code" (default), "markdown", or "raw"
        """
        await adapter.insert_cell(path, index, source, cell_type)
        return f"Cell inserted at index {index}"

    @mcp.tool()
    async def cell_delete(path: str, index: int) -> str:
        """Delete a cell from an open notebook. Syncs live to all collaborators.

        Requires an open CRDT room. Call ``room_open(path)`` first.

        Args:
            path: Notebook path
            index: Zero-based index of the cell to delete
        """
        await adapter.delete_cell(path, index)
        return f"Cell {index} deleted"

    @mcp.tool()
    async def cell_execute(path: str, index: int) -> list[dict[str, Any]]:
        """Execute a cell and return its outputs.

        Requires an open CRDT room. Call ``room_open(path)`` first.
        Starts a kernel if one isn't already running for this notebook.

        Args:
            path: Notebook path
            index: Zero-based index of the cell to execute

        Returns:
            List of output dicts, each with 'type' (stream, display_data,
            execute_result, error) and 'content'.
        """
        return await adapter.execute_cell(path, index)

    # ── room_* — CRDT room lifecycle and introspection ───────────────

    @mcp.tool()
    async def room_open(path: str) -> str:
        """Open a CRDT collaboration room for a notebook (idempotent).

        If a room already exists for this path, returns it unchanged.
        Once open, any subsequent JupyterLab UI tab that opens the same
        notebook automatically joins the same room — there is no second
        room. Required before any cell_* or notebook_get/watch call.

        Args:
            path: Notebook path relative to workspace root
        """
        await adapter.open_room(path)
        return f"Room opened for {path}"

    @mcp.tool()
    async def room_close(path: str) -> str:
        """Close a notebook's CRDT room and save its state to disk.

        Hard-fails if no room exists for the given path.

        Args:
            path: Notebook path relative to workspace root
        """
        await adapter.close_room(path)
        return f"Room closed for {path}"

    @mcp.tool()
    async def room_close_all() -> dict[str, Any]:
        """Close every active CRDT room — blanket cleanup.

        Iterates every room currently in the server's room registry,
        saves it to disk, and deletes it. Disconnects any active
        JupyterLab UI clients from their rooms. Use for end-of-task
        cleanup or orphan recovery, not for routine multi-client
        workflows.

        Returns:
            ``{"closed": [{"room_id": ..., "path": ...}, ...],
               "errors": [{"room_id": ..., "error": "..."}, ...]}``
        """
        return await adapter.close_all_rooms()

    @mcp.tool()
    async def room_pick(
        path: str | None = None, room_id: str | None = None
    ) -> dict[str, Any]:
        """Look up an existing CRDT room without creating one.

        Hard-fails if no room exists for the given path or room_id.
        Distinct from ``room_open`` (which creates if absent). Useful
        when verifying cleanup, attaching to a UI-created room, or
        operating on rooms whose path mapping has been lost.

        Provide exactly one of ``path`` or ``room_id``.

        Args:
            path: Notebook path to look up.
            room_id: Direct room id (escape hatch for orphans).

        Returns:
            One ``room_list`` entry: room_id, path, file_id, users,
            user_count, has_kernel.
        """
        if (path is None) == (room_id is None):
            raise ValueError("Provide exactly one of 'path' or 'room_id'.")
        return await adapter.pick_room(path=path, room_id=room_id)

    @mcp.tool()
    async def room_list() -> list[dict[str, Any]]:
        """List every active CRDT room with full metadata.

        Reports all rooms in the server's registry — UI-created and
        MCP-created alike — with each room's notebook path (reverse-
        resolved via the file id manager), the underlying file_id,
        the list of currently-attached awareness users, the user
        count, and whether a Jupyter kernel session is currently
        bound to the path.

        Returns:
            List of dicts with keys: room_id, path, file_id, users,
            user_count, has_kernel.
        """
        return await adapter.list_rooms()

    @mcp.tool()
    async def room_list_users() -> list[dict[str, str]]:
        """List awareness users currently connected to any CRDT room.

        Returns:
            List of dicts with 'id' and 'name' for each connected user.
        """
        return await adapter.list_room_users()

    return mcp
