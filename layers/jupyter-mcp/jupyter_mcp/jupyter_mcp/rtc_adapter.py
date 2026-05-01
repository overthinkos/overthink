"""RTC Adapter: accesses jupyter-collaboration CRDT documents for MCP tools.

Room creation is ALWAYS explicit: only ``open_room`` creates a CRDT room.
Every room-mutation method (cell ops, get_notebook, watch_notebook) raises
``RoomNotOpenError`` when no room exists for the given path.

Single-room-per-path invariant: ``open_room`` delegates to upstream
``YDocExtension.get_document(create=True)`` so MCP-driven and UI-driven
opens share the same code path; an orphan sweep before each open closes
any rooms whose ``file_id`` has rotated. ``close_room`` tolerates the
upstream ``_clean_room`` race (benign ``ValueError`` from a concurrent
WebSocket disconnect deleting the room first).
"""

from __future__ import annotations

import asyncio
import logging
import os
from typing import Any

log = logging.getLogger(__name__)


class RoomNotOpenError(RuntimeError):
    """Raised when a room-mutation method is called against a path with no active room.

    The caller should call ``open_room(path)`` first.
    """

    def __init__(self, path: str):
        super().__init__(
            f"No active CRDT room for '{path}'. Call room_open('{path}') first."
        )
        self.path = path


class NotebookWatcher:
    """Fan-out observer for a single notebook's CRDT changes.

    YNotebook.observe() only supports one callback at a time, so this class
    provides a single observer that dispatches to multiple asyncio.Event waiters.
    """

    def __init__(self):
        self._events: set[asyncio.Event] = set()

    def add_waiter(self) -> asyncio.Event:
        event = asyncio.Event()
        self._events.add(event)
        return event

    def remove_waiter(self, event: asyncio.Event) -> None:
        self._events.discard(event)

    def notify_all(self) -> None:
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

        self.kernel_manager = server_app.kernel_manager
        self.session_manager = server_app.session_manager
        self.contents_manager = server_app.contents_manager

        self.notebook_dir = getattr(
            server_app, "root_dir", os.path.expanduser("~/notebooks")
        )

    def _lock_for(self, path: str) -> asyncio.Lock:
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

    # ── Filesystem operations (do not touch CRDT rooms) ──────────────

    async def list_notebooks(self) -> list[dict[str, str]]:
        """List all .ipynb files accessible via the contents manager."""
        model = self.contents_manager.get("", content=True, type="directory")
        if asyncio.iscoroutine(model):
            model = await model
        notebooks: list[dict[str, str]] = []
        await self._collect_notebooks(model, notebooks)
        return notebooks

    async def _collect_notebooks(self, model: dict, result: list[dict]) -> None:
        if model["type"] == "notebook":
            result.append({"path": model["path"], "name": model["name"]})
        elif model["type"] == "directory":
            content = model.get("content")
            if content is None:
                sub = self.contents_manager.get(
                    model["path"], content=True, type="directory"
                )
                if asyncio.iscoroutine(sub):
                    sub = await sub
                content = sub.get("content") if sub else None
            if content:
                for item in content:
                    await self._collect_notebooks(item, result)

    async def create_notebook(self, path: str) -> dict[str, str]:
        """Create a new empty notebook on disk. Does NOT open a CRDT room."""
        import nbformat

        nb = nbformat.v4.new_notebook()
        model = {"type": "notebook", "content": nb, "format": "json"}
        result = self.contents_manager.save(model, path)
        if asyncio.iscoroutine(result):
            result = await result
        return {"path": result["path"], "name": result["name"]}

    # ── Notebook-level room operations (require open room) ───────────

    async def get_notebook(self, path: str) -> dict[str, Any]:
        """Get full notebook content from the live CRDT document.

        Raises RoomNotOpenError if no room is open for this path.
        """
        doc = await self._resolve_notebook_doc(path)
        return doc.get()

    # ── Cell operations (require open room) ──────────────────────────

    async def get_cell(self, path: str, index: int) -> dict[str, Any]:
        doc = await self._resolve_notebook_doc(path)
        if index < 0 or index >= doc.cell_number:
            raise IndexError(
                f"Cell index {index} out of range (notebook has {doc.cell_number} cells)"
            )
        return doc.get_cell(index)

    async def set_cell(self, path: str, index: int, value: dict[str, Any]) -> None:
        async with self._lock_for(path):
            doc = await self._resolve_notebook_doc(path)
            if index < 0 or index >= doc.cell_number:
                raise IndexError(
                    f"Cell index {index} out of range (notebook has {doc.cell_number} cells)"
                )
            doc.set_cell(index, value)

    async def insert_cell(
        self, path: str, index: int, source: str, cell_type: str = "code"
    ) -> None:
        async with self._lock_for(path):
            doc = await self._resolve_notebook_doc(path)
            cell_value = self._make_cell(source, cell_type)
            ycell = doc.create_ycell(cell_value)
            doc.ycells.insert(index, ycell)

    async def append_cell(
        self, path: str, source: str, cell_type: str = "code"
    ) -> None:
        async with self._lock_for(path):
            doc = await self._resolve_notebook_doc(path)
            cell_value = self._make_cell(source, cell_type)
            doc.append_cell(cell_value)

    async def delete_cell(self, path: str, index: int) -> None:
        async with self._lock_for(path):
            doc = await self._resolve_notebook_doc(path)
            if index < 0 or index >= doc.cell_number:
                raise IndexError(
                    f"Cell index {index} out of range (notebook has {doc.cell_number} cells)"
                )
            doc.ycells.pop(index)

    # ── Cell execution ───────────────────────────────────────────────

    async def execute_cell(self, path: str, index: int) -> list[dict[str, Any]]:
        """Execute a cell via the Jupyter kernel and return outputs.

        Outputs are also written back to the cell via the CRDT-aware set_cell
        path so they persist to disk on the next room save.
        """
        from nbformat.v4 import output_from_msg

        cell = await self.get_cell(path, index)
        source = cell.get("source", "")
        if not source.strip():
            return []

        kernel_id = await self._ensure_kernel(path)
        km = self.kernel_manager.get_kernel(kernel_id)
        client = km.client()
        client.start_channels()

        try:
            await asyncio.wait_for(client.wait_for_ready(), timeout=30)

            msg_id = client.execute(source)
            outputs: list[dict[str, Any]] = []
            iopub_msgs: list[dict[str, Any]] = []
            execution_count: int | None = None

            async def _read_shell_reply() -> None:
                nonlocal execution_count
                try:
                    while True:
                        reply = await asyncio.wait_for(
                            asyncio.ensure_future(client.get_shell_msg()),
                            timeout=120,
                        )
                        if reply["parent_header"].get("msg_id") != msg_id:
                            continue
                        if reply["msg_type"] == "execute_reply":
                            execution_count = reply["content"].get("execution_count")
                            return
                except asyncio.TimeoutError:
                    return

            shell_task = asyncio.ensure_future(_read_shell_reply())

            try:
                while True:
                    try:
                        msg = await asyncio.wait_for(
                            asyncio.ensure_future(client.get_iopub_msg()),
                            timeout=120,
                        )
                    except asyncio.TimeoutError:
                        outputs.append(
                            {
                                "type": "error",
                                "content": {
                                    "ename": "Timeout",
                                    "evalue": "Cell execution timed out",
                                },
                            }
                        )
                        break

                    if msg["parent_header"].get("msg_id") != msg_id:
                        continue

                    msg_type = msg["msg_type"]
                    if msg_type in ("stream", "display_data", "execute_result", "error"):
                        outputs.append({"type": msg_type, "content": msg["content"]})
                        iopub_msgs.append(msg)
                        if msg_type == "execute_result" and execution_count is None:
                            execution_count = msg["content"].get("execution_count")
                    elif (
                        msg_type == "status"
                        and msg["content"]["execution_state"] == "idle"
                    ):
                        break
            finally:
                try:
                    await asyncio.wait_for(shell_task, timeout=5)
                except (asyncio.TimeoutError, asyncio.CancelledError):
                    shell_task.cancel()

            try:
                nbf_outputs = [output_from_msg(m) for m in iopub_msgs]
            except Exception:
                nbf_outputs = []

            try:
                latest = await self.get_cell(path, index)
                if latest.get("cell_type") == "code":
                    latest["outputs"] = nbf_outputs
                    if execution_count is not None:
                        latest["execution_count"] = execution_count
                    await self.set_cell(path, index, latest)
            except IndexError:
                pass

            return outputs
        finally:
            client.stop_channels()

    # ── Room introspection ───────────────────────────────────────────

    async def list_room_users(self) -> list[dict[str, str]]:
        """List awareness users currently connected via collaboration."""
        if self.ydoc_extension is None:
            return []
        server = self.ydoc_extension.ywebsocket_server
        users = []
        for user_id, name in getattr(server, "connected_users", {}).items():
            users.append({"id": str(user_id), "name": name})
        return users

    async def list_rooms(self) -> list[dict[str, Any]]:
        """List every active CRDT room with full metadata.

        Returns one entry per room with: room_id, path (reverse-resolved),
        file_id, users (awareness), user_count, has_kernel.
        """
        if self.ydoc_extension is None:
            return []
        server = self.ydoc_extension.ywebsocket_server
        file_id_manager = self.server_app.web_app.settings.get("file_id_manager")
        result: list[dict[str, Any]] = []
        for room_id in list(getattr(server, "rooms", {}).keys()):
            entry = await self._room_metadata(room_id, file_id_manager)
            result.append(entry)
        return result

    async def pick_room(
        self, path: str | None = None, room_id: str | None = None
    ) -> dict[str, Any]:
        """Look up an existing room without creating one. Hard-fail if absent.

        Provide exactly one of ``path`` or ``room_id``.
        """
        if (path is None) == (room_id is None):
            raise ValueError("Provide exactly one of 'path' or 'room_id'.")
        if self.ydoc_extension is None:
            raise RoomNotOpenError(path or room_id or "<unknown>")
        server = self.ydoc_extension.ywebsocket_server
        file_id_manager = self.server_app.web_app.settings.get("file_id_manager")

        target_room_id = room_id
        if path is not None:
            if file_id_manager is None:
                raise RoomNotOpenError(path)
            try:
                file_id = file_id_manager.index(path)
            except Exception as e:
                raise RoomNotOpenError(path) from e
            from jupyter_server_ydoc.utils import (
                encode_file_path,
                room_id_from_encoded_path,
            )

            target_room_id = room_id_from_encoded_path(
                encode_file_path("json", "notebook", file_id)
            )
            # Change 0: sweep stale orphans for this path before lookup
            await self._sweep_stale_rooms_for_path(path)

        if not server.room_exists(target_room_id):
            raise RoomNotOpenError(path or target_room_id)
        return await self._room_metadata(target_room_id, file_id_manager)

    async def close_all_rooms(self) -> dict[str, Any]:
        """Close every active room — blanket cleanup.

        Returns ``{"closed": [...], "errors": [...]}``.
        """
        if self.ydoc_extension is None:
            return {"closed": [], "errors": []}
        server = self.ydoc_extension.ywebsocket_server
        file_id_manager = self.server_app.web_app.settings.get("file_id_manager")

        closed: list[dict[str, Any]] = []
        errors: list[dict[str, Any]] = []

        for room_id in list(server.rooms.keys()):
            path = self._room_id_to_path(room_id, file_id_manager)
            try:
                room = server.rooms.get(room_id)
                if room is not None:
                    try:
                        save_task = room._save_to_disc()
                        if save_task is not None:
                            await save_task
                    except Exception as e:
                        log.warning(
                            "Sync save before close failed for %s: %s", path, e
                        )
                try:
                    await server.delete_room(name=room_id)
                except ValueError:
                    # Benign upstream race: room already cleaned up.
                    pass
                closed.append({"room_id": room_id, "path": path})
                if path is not None:
                    self._detach_watcher(path)
                    self._notebook_locks.pop(path, None)
            except Exception as e:
                errors.append({"room_id": room_id, "error": str(e)})

        return {"closed": closed, "errors": errors}

    # ── Change watching ──────────────────────────────────────────────

    async def watch_notebook(
        self, path: str, timeout: float = 30.0
    ) -> dict[str, Any]:
        """Block until a CRDT change occurs on the notebook, or timeout.

        Requires an open room.
        """
        doc = await self._resolve_notebook_doc(path)
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
        if path not in self._watchers:
            watcher = NotebookWatcher()
            self._watchers[path] = watcher

            def on_change(event_name, changes):
                watcher.notify_all()

            doc.observe(on_change)
            log.debug("Attached CRDT observer for %s", path)
        return self._watchers[path]

    def _detach_watcher(self, path: str) -> None:
        if path in self._watchers:
            del self._watchers[path]
            log.debug("Detached CRDT observer for %s", path)

    # ── Room lifecycle ───────────────────────────────────────────────

    async def open_room(self, path: str) -> None:
        """Open or join a CRDT room (idempotent).

        Sweeps stale-file_id orphans for this path, then either reuses
        the existing room (deterministic-room-id short-circuit) or
        constructs one mirroring ``YDocWebSocketHandler.prepare()``.
        Finally pushes an MCP awareness presence so UI clients see the
        join.

        Note: jupyter_server_ydoc 2.3.0 does not expose a one-call
        ``get_document(create=True)`` helper, so ``_create_room``
        replicates the public-API construction sequence
        (``server.start_room`` → ``room.initialize`` → ``server.add_room``)
        that the upstream WebSocket handler runs on UI-side opens.
        Both code paths land on the same ``room_id`` derived from
        ``file_id_manager.index(path)``, so MCP and UI converge on the
        same ``server.rooms[room_id]`` entry.
        """
        if self.ydoc_extension is None:
            raise RuntimeError(
                "jupyter_server_ydoc extension not available — cannot create CRDT room."
            )
        async with self._lock_for(path):
            # Change 0: sweep stale-file_id orphans for this path
            await self._sweep_stale_rooms_for_path(path)
            # Idempotent: skip if a room already exists for this path
            doc = await self._get_notebook_doc(path)
            if doc is None:
                await self._create_room(path)
            # Change 5: push MCP awareness presence
            await self._push_mcp_awareness(path)

    async def _create_room(self, path: str) -> None:
        """Create a CRDT DocumentRoom for a notebook, mirroring
        ``YDocWebSocketHandler.prepare()`` from jupyter_server_ydoc.

        On install 2.3.0 ``YDocExtension`` does NOT expose a one-call
        create helper. Both this method and the upstream WebSocket
        handler land on the same deterministic ``room_id`` (derived from
        ``file_id_manager.index(path)``), so an MCP-driven open and a
        UI-driven open converge on the same ``server.rooms[room_id]``
        entry. The single-room invariant is enforced by the
        ``server.room_exists(room_id)`` short-circuit + the
        ``_sweep_stale_rooms_for_path`` pass that runs before this
        method.
        """
        from jupyter_server_ydoc.rooms import DocumentRoom
        from jupyter_server_ydoc.utils import (
            encode_file_path,
            room_id_from_encoded_path,
        )

        ext = self.ydoc_extension
        server = ext.ywebsocket_server

        if not server.started.is_set():
            asyncio.ensure_future(server.start())
            await server.started.wait()

        file_id_manager = self.server_app.web_app.settings["file_id_manager"]
        file_id = file_id_manager.index(path)

        room_id = room_id_from_encoded_path(
            encode_file_path("json", "notebook", file_id)
        )

        if server.room_exists(room_id):
            return

        def _exception_logger(exception, logger):
            logger.error(
                "Document Room Exception (room_id=%s): ", room_id, exc_info=exception
            )
            return True

        file_loader = ext.file_loaders[file_id]
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
        await room.initialize()
        server.add_room(room_id, room)
        log.info("Created CRDT room: %s", room_id)

    async def close_room(self, path: str) -> None:
        """Close a CRDT room and save its state to disk.

        Hard-fails if no room exists for the path. Tolerates the upstream
        ``_clean_room`` race when a UI client is concurrently disconnecting.
        """
        if self.ydoc_extension is None:
            raise RoomNotOpenError(path)
        from jupyter_server_ydoc.utils import (
            encode_file_path,
            room_id_from_encoded_path,
        )

        server = self.ydoc_extension.ywebsocket_server
        file_id_manager = self.server_app.web_app.settings.get("file_id_manager")
        if file_id_manager is None:
            raise RoomNotOpenError(path)

        try:
            file_id = file_id_manager.index(path)
        except Exception as e:
            raise RoomNotOpenError(path) from e

        room_id = room_id_from_encoded_path(
            encode_file_path("json", "notebook", file_id)
        )

        if not server.room_exists(room_id):
            raise RoomNotOpenError(path)

        self._detach_watcher(path)

        # Synchronously flush before close
        room = server.rooms.get(room_id)
        if room is not None:
            try:
                save_task = room._save_to_disc()
                if save_task is not None:
                    await save_task
            except Exception as e:
                log.warning("Synchronous save before close failed for %s: %s", path, e)

        # Change B: try/except for benign upstream race. We deliberately
        # do NOT call server.rooms.pop(...) afterwards — the upstream
        # delete_room already pops, and the redundant pop was the trigger
        # for the YDocWebSocketHandler._clean_room ValueError we observed.
        try:
            await server.delete_room(name=room_id)
        except ValueError as e:
            log.info(
                "Benign upstream race on delete_room for %s "
                "(room already cleaned by concurrent _clean_room): %s",
                path,
                e,
            )

        self._notebook_locks.pop(path, None)
        log.info("Closed CRDT room: %s", room_id)

    # ── Internal helpers ─────────────────────────────────────────────

    async def _get_notebook_doc(self, path: str):
        """Get the live YNotebook CRDT document, or None if no room exists."""
        if self.ydoc_extension is None:
            return None
        try:
            doc = await self.ydoc_extension.get_document(
                path=path,
                content_type="notebook",
                file_format="json",
                copy=False,
            )
            return doc
        except Exception as e:
            log.debug("Could not get CRDT document for %s: %s", path, e)
            return None

    async def _resolve_notebook_doc(self, path: str):
        """Get the live YNotebook CRDT document, or raise RoomNotOpenError.

        Replaces the previous ``_require_notebook_doc`` which auto-created
        the room. Per the no-implicit-creation policy, this never creates;
        callers must call ``open_room(path)`` explicitly first.
        """
        doc = await self._get_notebook_doc(path)
        if doc is None:
            raise RoomNotOpenError(path)
        return doc

    def _room_id_to_path(self, room_id: str, file_id_manager) -> str | None:
        """Reverse-map ``json:notebook:<file_id>`` → notebook path, or None."""
        if file_id_manager is None:
            return None
        parts = room_id.split(":")
        if len(parts) < 3 or parts[1] != "notebook":
            return None
        try:
            return file_id_manager.get_path(parts[2])
        except Exception:
            return None

    async def _room_metadata(
        self, room_id: str, file_id_manager
    ) -> dict[str, Any]:
        """Build the rich-metadata entry for one room (used by list_rooms / pick_room)."""
        server = self.ydoc_extension.ywebsocket_server
        room = server.rooms.get(room_id)

        parts = room_id.split(":")
        file_id = parts[2] if len(parts) >= 3 else None
        path = self._room_id_to_path(room_id, file_id_manager)

        users: list[str] = []
        if room is not None:
            try:
                ydoc = getattr(room, "ydoc", None) or getattr(room, "_document", None)
                awareness = (
                    getattr(ydoc, "awareness", None) if ydoc is not None else None
                )
                if awareness is not None:
                    states = awareness.states
                    for client_id, state in states.items():
                        user = state.get("user", {}) if isinstance(state, dict) else {}
                        name = user.get("name") if isinstance(user, dict) else None
                        users.append(name or str(client_id))
            except Exception:
                pass

        has_kernel = False
        if path is not None:
            try:
                exists = self.session_manager.session_exists(path=path)
                if asyncio.iscoroutine(exists):
                    exists = await exists
                has_kernel = bool(exists)
            except Exception:
                pass

        return {
            "room_id": room_id,
            "path": path,
            "file_id": file_id,
            "users": users,
            "user_count": len(users),
            "has_kernel": has_kernel,
        }

    async def _sweep_stale_rooms_for_path(self, path: str) -> None:
        """Close any rooms whose reverse-mapped path equals ``path`` but
        whose ``room_id`` differs from the current ``file_id_manager.index(path)``.

        Guards against orphans left behind when a file is renamed or
        recreated and ``file_id_manager`` rotates the file_id.
        """
        if self.ydoc_extension is None:
            return
        server = self.ydoc_extension.ywebsocket_server
        file_id_manager = self.server_app.web_app.settings.get("file_id_manager")
        if file_id_manager is None:
            return
        try:
            current_file_id = file_id_manager.index(path)
        except Exception:
            return
        from jupyter_server_ydoc.utils import (
            encode_file_path,
            room_id_from_encoded_path,
        )

        current_room_id = room_id_from_encoded_path(
            encode_file_path("json", "notebook", current_file_id)
        )

        stale_ids: list[str] = []
        for rid in list(server.rooms.keys()):
            if rid == current_room_id:
                continue
            mapped_path = self._room_id_to_path(rid, file_id_manager)
            if mapped_path == path:
                stale_ids.append(rid)

        for stale_id in stale_ids:
            log.warning(
                "Sweeping stale room %s for path %s before opening current room %s",
                stale_id,
                path,
                current_room_id,
            )
            try:
                room = server.rooms.get(stale_id)
                if room is not None:
                    try:
                        save_task = room._save_to_disc()
                        if save_task is not None:
                            await save_task
                    except Exception:
                        pass
                try:
                    await server.delete_room(name=stale_id)
                except ValueError:
                    pass
            except Exception as e:
                log.warning("Failed to sweep stale room %s: %s", stale_id, e)

    async def _push_mcp_awareness(self, path: str) -> None:
        """Push an awareness presence entry identifying the MCP client.

        Surfaces the MCP as a collaborator in JupyterLab's standard
        collaboration sidebar so a human user opening the notebook in
        the UI explicitly sees the join.
        """
        try:
            doc = await self._get_notebook_doc(path)
            if doc is None:
                return
            ydoc = getattr(doc, "_ydoc", None) or getattr(doc, "ydoc", None)
            awareness = getattr(ydoc, "awareness", None) if ydoc is not None else None
            if awareness is None:
                return
            awareness.set_local_state(
                {
                    "user": {
                        "name": "Claude (MCP)",
                        "color": "#7C3AED",
                    }
                }
            )
        except Exception as e:
            log.debug("Could not push MCP awareness for %s: %s", path, e)

    async def _ensure_kernel(self, path: str) -> str:
        exists = self.session_manager.session_exists(path=path)
        if asyncio.iscoroutine(exists):
            exists = await exists
        if exists:
            session = self.session_manager.get_session(path=path)
            if asyncio.iscoroutine(session):
                session = await session
            return session["kernel"]["id"]
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
        cell: dict[str, Any] = {
            "cell_type": cell_type,
            "source": source,
            "metadata": {},
        }
        if cell_type == "code":
            cell["outputs"] = []
            cell["execution_count"] = None
        return cell
