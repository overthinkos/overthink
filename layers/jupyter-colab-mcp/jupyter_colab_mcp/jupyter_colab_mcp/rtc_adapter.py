"""RTC Adapter: accesses jupyter-collaboration CRDT documents for MCP tools.

All cell mutations go through jupyter_ydoc's YNotebook API, which wraps
pycrdt CRDT types. Mutations propagate automatically to all connected
JupyterLab clients via the existing WebSocket infrastructure.
"""

from __future__ import annotations

import asyncio
import logging
import os
from typing import Any

log = logging.getLogger(__name__)


class NotebookWatcher:
    """Fan-out observer for a single notebook's CRDT changes.

    YNotebook.observe() only supports one callback at a time, so this class
    provides a single observer that dispatches to multiple asyncio.Event waiters.
    """

    def __init__(self):
        self._events: set[asyncio.Event] = set()

    def add_waiter(self) -> asyncio.Event:
        """Register a new waiter. Returns an Event that fires on the next change."""
        event = asyncio.Event()
        self._events.add(event)
        return event

    def remove_waiter(self, event: asyncio.Event) -> None:
        """Remove a waiter."""
        self._events.discard(event)

    def notify_all(self) -> None:
        """Signal all waiters that a change occurred."""
        for event in self._events:
            event.set()

    @property
    def has_waiters(self) -> bool:
        return len(self._events) > 0


class RTCAdapter:
    """Bridges MCP tool calls to jupyter-collaboration's CRDT documents."""

    def __init__(self, server_app):
        self.server_app = server_app
        self._ydoc_extension = None  # Resolved lazily to avoid load-order issues
        self._notebook_locks: dict[str, asyncio.Lock] = {}
        self._watchers: dict[str, NotebookWatcher] = {}  # path → watcher

        # Jupyter Server managers for kernel execution and file operations
        self.kernel_manager = server_app.kernel_manager
        self.session_manager = server_app.session_manager
        self.contents_manager = server_app.contents_manager

        # Resolve notebook directory from server config
        self.notebook_dir = getattr(
            server_app, "root_dir", os.path.expanduser("~/notebooks")
        )

    def _lock_for(self, path: str) -> asyncio.Lock:
        """Get or create a per-notebook lock for serializing mutations."""
        if path not in self._notebook_locks:
            self._notebook_locks[path] = asyncio.Lock()
        return self._notebook_locks[path]

    @property
    def ydoc_extension(self):
        """Lazily resolve the YDocExtension — it may load after us."""
        if self._ydoc_extension is None:
            ext_pkg = self.server_app.extension_manager.extensions.get(
                "jupyter_server_ydoc"
            )
            if ext_pkg is not None:
                # ExtensionPackage → ExtensionPoint → YDocExtension app
                for point in ext_pkg.extension_points.values():
                    if point.app is not None:
                        self._ydoc_extension = point.app
                        break
            if self._ydoc_extension is None:
                log.warning(
                    "jupyter_server_ydoc extension not available — "
                    "CRDT-based notebook tools will not work."
                )
        return self._ydoc_extension

    # ── Notebook operations ──────────────────────────────────────────

    async def list_notebooks(self) -> list[dict[str, str]]:
        """List all .ipynb files accessible via the contents manager."""
        model = self.contents_manager.get("", content=True, type="directory")
        if asyncio.iscoroutine(model):
            model = await model
        notebooks = []
        await self._collect_notebooks(model, notebooks)
        return notebooks

    async def _collect_notebooks(self, model: dict, result: list[dict]) -> None:
        """Recursively collect notebooks from a directory model."""
        if model["type"] == "notebook":
            result.append({"path": model["path"], "name": model["name"]})
        elif model["type"] == "directory":
            content = model.get("content")
            if content is None:
                # Subdirectory not yet fetched — fetch it recursively
                sub = self.contents_manager.get(model["path"], content=True, type="directory")
                if asyncio.iscoroutine(sub):
                    sub = await sub
                content = sub.get("content") if sub else None
            if content:
                for item in content:
                    await self._collect_notebooks(item, result)

    async def get_notebook(self, path: str) -> dict[str, Any] | None:
        """Get full notebook content. Uses CRDT document if room exists, else file."""
        doc = await self._get_notebook_doc(path)
        if doc is not None:
            return doc.get()
        # Fallback: read from disk via contents manager
        model = self.contents_manager.get(path, content=True, type="notebook")
        if asyncio.iscoroutine(model):
            model = await model
        return model.get("content")

    async def create_notebook(self, path: str) -> dict[str, str]:
        """Create a new empty notebook at the given path."""
        import nbformat

        nb = nbformat.v4.new_notebook()
        model = {"type": "notebook", "content": nb, "format": "json"}
        result = self.contents_manager.save(model, path)
        if asyncio.iscoroutine(result):
            result = await result
        return {"path": result["path"], "name": result["name"]}

    # ── Cell operations (via CRDT — auto-sync to all collaborators) ──

    async def get_cell(self, path: str, index: int) -> dict[str, Any]:
        """Get a cell's content by index."""
        doc = await self._require_notebook_doc(path)
        if index < 0 or index >= doc.cell_number:
            raise IndexError(
                f"Cell index {index} out of range (notebook has {doc.cell_number} cells)"
            )
        return doc.get_cell(index)

    async def set_cell(self, path: str, index: int, value: dict[str, Any]) -> None:
        """Update a cell's content. Mutation syncs to all collaborators via CRDT."""
        async with self._lock_for(path):
            doc = await self._require_notebook_doc(path)
            if index < 0 or index >= doc.cell_number:
                raise IndexError(
                    f"Cell index {index} out of range (notebook has {doc.cell_number} cells)"
                )
            doc.set_cell(index, value)

    async def insert_cell(
        self, path: str, index: int, source: str, cell_type: str = "code"
    ) -> None:
        """Insert a new cell at the given position."""
        async with self._lock_for(path):
            doc = await self._require_notebook_doc(path)
            cell_value = self._make_cell(source, cell_type)
            ycell = doc.create_ycell(cell_value)
            doc.ycells.insert(index, ycell)

    async def append_cell(
        self, path: str, source: str, cell_type: str = "code"
    ) -> None:
        """Append a new cell at the end of the notebook."""
        async with self._lock_for(path):
            doc = await self._require_notebook_doc(path)
            cell_value = self._make_cell(source, cell_type)
            doc.append_cell(cell_value)

    async def delete_cell(self, path: str, index: int) -> None:
        """Delete a cell by index."""
        async with self._lock_for(path):
            doc = await self._require_notebook_doc(path)
            if index < 0 or index >= doc.cell_number:
                raise IndexError(
                    f"Cell index {index} out of range (notebook has {doc.cell_number} cells)"
                )
            doc.ycells.pop(index)

    # ── Cell execution ───────────────────────────────────────────────

    async def execute_cell(self, path: str, index: int) -> list[dict[str, Any]]:
        """Execute a cell via the Jupyter kernel and return outputs."""
        cell = await self.get_cell(path, index)
        source = cell.get("source", "")
        if not source.strip():
            return []

        kernel_id = await self._ensure_kernel(path)
        km = self.kernel_manager.get_kernel(kernel_id)
        client = km.client()
        client.start_channels()

        try:
            # Wait for kernel to be ready
            await asyncio.wait_for(client.wait_for_ready(), timeout=30)

            msg_id = client.execute(source)
            outputs: list[dict[str, Any]] = []

            while True:
                try:
                    msg = await asyncio.wait_for(
                        asyncio.ensure_future(client.get_iopub_msg()),
                        timeout=120,
                    )
                except asyncio.TimeoutError:
                    outputs.append(
                        {"type": "error", "content": {"ename": "Timeout", "evalue": "Cell execution timed out"}}
                    )
                    break

                if msg["parent_header"].get("msg_id") != msg_id:
                    continue

                msg_type = msg["msg_type"]
                if msg_type in ("stream", "display_data", "execute_result", "error"):
                    outputs.append({"type": msg_type, "content": msg["content"]})
                elif (
                    msg_type == "status"
                    and msg["content"]["execution_state"] == "idle"
                ):
                    break

            return outputs
        finally:
            client.stop_channels()

    # ── Collaboration awareness ──────────────────────────────────────

    async def get_active_users(self) -> list[dict[str, str]]:
        """List users currently connected via collaboration."""
        if self.ydoc_extension is None:
            return []
        server = self.ydoc_extension.ywebsocket_server
        users = []
        for user_id, name in getattr(server, "connected_users", {}).items():
            users.append({"id": str(user_id), "name": name})
        return users

    async def get_active_sessions(self) -> list[dict[str, str]]:
        """List active collaboration rooms/sessions."""
        if self.ydoc_extension is None:
            return []
        server = self.ydoc_extension.ywebsocket_server
        rooms = []
        for name in list(getattr(server, "rooms", {}).keys()):
            rooms.append({"room_id": name})
        return rooms

    # ── Change watching ─────────────────────────────────────────────

    async def watch_notebook(
        self, path: str, timeout: float = 30.0
    ) -> dict[str, Any]:
        """Block until a CRDT change occurs on the notebook, or timeout.

        Returns:
            {"changed": True, "cell_count": N} if a change was detected
            {"changed": False} if timeout expired
        """
        doc = await self._require_notebook_doc(path)
        watcher = self._ensure_watcher(path, doc)
        event = watcher.add_waiter()
        try:
            await asyncio.wait_for(event.wait(), timeout=timeout)
            return {"changed": True, "cell_count": doc.cell_number}
        except asyncio.TimeoutError:
            return {"changed": False}
        finally:
            watcher.remove_waiter(event)

    def _ensure_watcher(self, path: str, doc) -> NotebookWatcher:
        """Get or create a NotebookWatcher for a notebook, attaching
        a CRDT observer if needed."""
        if path not in self._watchers:
            watcher = NotebookWatcher()
            self._watchers[path] = watcher

            def on_change(event_name, changes):
                watcher.notify_all()

            doc.observe(on_change)
            log.debug("Attached CRDT observer for %s", path)
        return self._watchers[path]

    def _detach_watcher(self, path: str) -> None:
        """Detach the CRDT observer for a notebook."""
        if path in self._watchers:
            del self._watchers[path]
            log.debug("Detached CRDT observer for %s", path)

    # ── Room management ────────────────────────────────────────────

    async def open_room(self, path: str) -> None:
        """Explicitly open a CRDT room for a notebook."""
        async with self._lock_for(path):
            doc = await self._get_notebook_doc(path)
            if doc is not None:
                return  # Room already exists
            if self.ydoc_extension is None:
                raise RuntimeError(
                    "jupyter_server_ydoc extension not available — cannot create CRDT room."
                )
        await self._create_room(path)

    async def close_room(self, path: str) -> None:
        """Close a CRDT room, saving the notebook to disk."""
        self._detach_watcher(path)
        if self.ydoc_extension is None:
            return
        from jupyter_server_ydoc.utils import encode_file_path, room_id_from_encoded_path

        ext = self.ydoc_extension
        server = ext.ywebsocket_server
        file_id_manager = self.server_app.web_app.settings["file_id_manager"]

        try:
            file_id = file_id_manager.index(path)
        except Exception:
            return  # File not tracked

        room_id = room_id_from_encoded_path(
            encode_file_path("json", "notebook", file_id)
        )

        if not server.room_exists(room_id):
            return

        await server.delete_room(name=room_id)
        log.info("Closed CRDT room: %s", room_id)

    # ── Internal helpers ─────────────────────────────────────────────

    async def _get_notebook_doc(self, path: str):
        """Get the live YNotebook CRDT document, or None if unavailable."""
        if self.ydoc_extension is None:
            return None
        try:
            doc = await self.ydoc_extension.get_document(
                path=path,
                content_type="notebook",
                file_format="json",
                copy=False,  # Live document — mutations propagate via CRDT
            )
            return doc
        except Exception as e:
            log.debug("Could not get CRDT document for %s: %s", path, e)
            return None

    async def _require_notebook_doc(self, path: str):
        """Get or create the live YNotebook CRDT document for a notebook.

        If no CRDT room exists yet, creates one on demand — the MCP server
        doesn't require a browser to be open. This replicates the room
        creation logic from YDocWebSocketHandler.prepare().
        """
        doc = await self._get_notebook_doc(path)
        if doc is not None:
            return doc

        # Room doesn't exist — create it on demand
        if self.ydoc_extension is None:
            raise RuntimeError(
                "jupyter_server_ydoc extension not available — cannot create CRDT room."
            )

        await self._create_room(path)

        # Now get_document should find the room
        doc = await self._get_notebook_doc(path)
        if doc is None:
            raise RuntimeError(
                f"Failed to create CRDT room for '{path}'. "
                "Verify the notebook exists on disk."
            )
        return doc

    async def _create_room(self, path: str):
        """Create a CRDT DocumentRoom for a notebook, replicating the
        logic from YDocWebSocketHandler.prepare()."""
        from jupyter_server_ydoc.rooms import DocumentRoom
        from jupyter_server_ydoc.utils import encode_file_path, room_id_from_encoded_path

        ext = self.ydoc_extension
        server = ext.ywebsocket_server

        # Start the websocket server if not already running (same lazy-start
        # pattern as YDocWebSocketHandler.prepare() — the server is only
        # started on first use, not at extension load time)
        if not server.started.is_set():
            asyncio.ensure_future(server.start())
            await server.started.wait()

        # Resolve file_id via file_id_manager
        file_id_manager = self.server_app.web_app.settings["file_id_manager"]
        file_id = file_id_manager.index(path)

        room_id = room_id_from_encoded_path(
            encode_file_path("json", "notebook", file_id)
        )

        # Don't create if it already exists
        if server.room_exists(room_id):
            return

        def _exception_logger(exception, logger):
            logger.error(
                "Document Room Exception (room_id=%s): ", room_id, exc_info=exception
            )
            return True

        # Get FileLoader from the extension's file_loaders mapping
        # (FileLoaderMapping auto-creates loaders on access)
        file_loader = ext.file_loaders[file_id]

        # Create ystore for persistence
        ystore_class = ext.ystore_class
        updates_file_path = f".notebook:{file_id}.y"
        ystore = ystore_class(path=updates_file_path, log=log)

        room = DocumentRoom(
            room_id,
            file_format="json",
            file_type="notebook",
            file=file_loader,
            logger=ext.serverapp.event_logger,
            ystore=ystore,
            log=log,
            save_delay=ext.document_save_delay,
            exception_handler=_exception_logger,
        )

        await server.start_room(room)
        # Initialize the room — loads notebook content from disk into the
        # CRDT document. Without this, the YNotebook is empty (0 cells).
        # This mirrors YDocWebSocketHandler.open() which calls
        # room.initialize() after start_room().
        await room.initialize()
        server.add_room(room_id, room)
        log.info("Created CRDT room on demand: %s", room_id)

    async def _ensure_kernel(self, path: str) -> str:
        """Get or create a kernel session for a notebook, returning kernel_id."""
        exists = self.session_manager.session_exists(path=path)
        if asyncio.iscoroutine(exists):
            exists = await exists
        if exists:
            session = self.session_manager.get_session(path=path)
            if asyncio.iscoroutine(session):
                session = await session
            return session["kernel"]["id"]

        # Start a new kernel
        kernel_id = self.kernel_manager.start_kernel(path=path)
        if asyncio.iscoroutine(kernel_id):
            kernel_id = await kernel_id
        result = self.session_manager.create_session(
            path=path, type="notebook", kernel_id=kernel_id
        )
        if asyncio.iscoroutine(result):
            await result
        return kernel_id

    @staticmethod
    def _make_cell(source: str, cell_type: str = "code") -> dict[str, Any]:
        """Create a notebook cell dict in nbformat v4 structure."""
        cell: dict[str, Any] = {
            "cell_type": cell_type,
            "source": source,
            "metadata": {},
        }
        if cell_type == "code":
            cell["outputs"] = []
            cell["execution_count"] = None
        return cell
