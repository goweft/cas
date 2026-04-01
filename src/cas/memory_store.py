"""CAS in-memory store — lightweight SessionStore for tests and embedded use.

Implements the same SessionStore protocol as CASStore (SQLite) but keeps
everything in Python dicts. Zero dependencies, zero filesystem side effects.

Use cases:
    - Unit tests (no ~/.cas/cas.db touched)
    - Embedded CAS in short-lived processes
    - Future: remote session relay where persistence lives elsewhere
"""

from __future__ import annotations

import logging
from datetime import datetime, timezone
from typing import Any, Callable, TYPE_CHECKING

if TYPE_CHECKING:
    from cas.contracts import AgentContract

logger = logging.getLogger(__name__)


class InMemoryStore:
    """Dict-backed SessionStore — drop-in replacement for CASStore."""

    def __init__(self) -> None:
        self._sessions: dict[str, dict[str, Any]] = {}
        self._messages: dict[str, list[dict[str, Any]]] = {}
        self._workspaces: dict[str, dict[str, Any]] = {}
        self._history: dict[str, list[dict[str, Any]]] = {}
        self._version_counters: dict[str, int] = {}

    # ── Sessions ─────────────────────────────────────────────────

    def save_session(self, session: Any) -> None:
        self._sessions[session.id] = {
            "id": session.id,
            "created_at": session.created_at.isoformat(),
        }

    def load_sessions(self) -> dict[str, Any]:
        return dict(self._sessions)

    # ── Messages ─────────────────────────────────────────────────

    def save_message(self, session_id: str, message: Any) -> None:
        self._messages.setdefault(session_id, []).append({
            "role": message.role,
            "text": message.text,
            "timestamp": message.timestamp.isoformat(),
        })

    def load_messages(self, session_id: str) -> list[dict]:
        return list(self._messages.get(session_id, []))

    # ── Workspaces ───────────────────────────────────────────────

    def save_workspace(self, workspace: Any, session_id: str) -> None:
        self._workspaces[workspace.id] = {
            "id": workspace.id,
            "session_id": session_id,
            "type": workspace.type,
            "title": workspace.title,
            "content": workspace.content,
            "created_at": workspace.created_at,
            "closed_at": workspace.closed_at,
        }

    def update_workspace(self, workspace: Any) -> None:
        if workspace.id not in self._workspaces:
            return
        # Snapshot current state before updating
        current = self._workspaces[workspace.id]
        counter = self._version_counters.get(workspace.id, 0) + 1
        self._version_counters[workspace.id] = counter
        self._history.setdefault(workspace.id, []).append({
            "version": counter,
            "title": current["title"],
            "content": current["content"],
            "saved_at": datetime.now(timezone.utc).isoformat(),
        })
        # Apply update
        self._workspaces[workspace.id].update({
            "title": workspace.title,
            "content": workspace.content,
            "closed_at": workspace.closed_at,
        })

    def close_workspace(self, workspace_id: str, closed_at: datetime) -> None:
        if workspace_id in self._workspaces:
            self._workspaces[workspace_id]["closed_at"] = closed_at

    def load_workspaces(
        self, contract_factory: Callable[[], "AgentContract"]
    ) -> dict[str, Any]:
        result = {}
        for ws_id, ws in self._workspaces.items():
            if ws["closed_at"] is None:
                result[ws_id] = {**ws, "contract": contract_factory()}
        return result

    # ── History ──────────────────────────────────────────────────

    def load_history(self, workspace_id: str) -> list[dict]:
        return list(reversed(self._history.get(workspace_id, [])))

    def get_version(self, workspace_id: str, version: int) -> dict | None:
        for entry in self._history.get(workspace_id, []):
            if entry["version"] == version:
                return entry
        return None

    def apply_version(self, workspace_id: str, version: int) -> bool:
        target = self.get_version(workspace_id, version)
        if not target or workspace_id not in self._workspaces:
            return False
        self._workspaces[workspace_id]["title"] = target["title"]
        self._workspaces[workspace_id]["content"] = target["content"]
        return True

    def undo(self, workspace_id: str) -> dict | None:
        history = self.load_history(workspace_id)
        if not history:
            return None
        latest = history[0]
        if not self.apply_version(workspace_id, latest["version"]):
            return None
        ws = self._workspaces.get(workspace_id)
        return {"title": ws["title"], "content": ws["content"]} if ws else None

    def close(self) -> None:
        """No-op — nothing to clean up."""
        pass

    def __repr__(self) -> str:
        return (
            f"InMemoryStore(sessions={len(self._sessions)}, "
            f"workspaces={len(self._workspaces)})"
        )
