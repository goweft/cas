"""CAS workspace types and lifecycle — contract-enforced, store-persisted."""

from __future__ import annotations

import logging
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import TYPE_CHECKING, Any

from cas.contracts import AgentContract

if TYPE_CHECKING:
    from cas.store import CASStore

logger = logging.getLogger(__name__)

# Supported workspace types and their display labels
WORKSPACE_TYPES = {"document", "code", "list"}

WORKSPACE_TYPE_LABELS = {
    "document": "document",
    "code": "code",
    "list": "list",
}


class WorkspaceError(Exception):
    pass

class WorkspaceNotFound(WorkspaceError):
    pass

class WorkspaceClosed(WorkspaceError):
    pass


@dataclass
class Workspace:
    """A single workspace instance."""
    id: str
    type: str
    title: str
    content: str
    created_at: datetime
    contract: AgentContract
    closed_at: datetime | None = field(default=None, repr=False)

    @property
    def is_active(self) -> bool:
        return self.closed_at is None

    def to_dict(self) -> dict[str, Any]:
        return {
            "id": self.id,
            "type": self.type,
            "title": self.title,
            "content": self.content,
            "created_at": self.created_at.isoformat(),
            "closed_at": self.closed_at.isoformat() if self.closed_at else None,
            "is_active": self.is_active,
        }


class WorkspaceManager:
    """In-memory workspace lifecycle with optional SQLite persistence."""

    def __init__(self, store: "CASStore | None" = None) -> None:
        self._workspaces: dict[str, Workspace] = {}
        self._store = store

    def restore(self, workspaces: dict[str, Any]) -> None:
        for ws_id, data in workspaces.items():
            ws = Workspace(
                id=data["id"], type=data["type"], title=data["title"],
                content=data["content"], created_at=data["created_at"],
                contract=data["contract"], closed_at=data["closed_at"],
            )
            ws._session_id = data.get("session_id", "")  # type: ignore[attr-defined]
            self._workspaces[ws_id] = ws
        logger.info("Restored %d workspaces from store", len(workspaces))

    def create(
        self,
        title: str,
        content: str,
        contract: AgentContract,
        workspace_type: str = "document",
        session_id: str = "",
    ) -> Workspace:
        if workspace_type not in WORKSPACE_TYPES:
            raise WorkspaceError(
                f"Unknown workspace type '{workspace_type}'. "
                f"Supported: {sorted(WORKSPACE_TYPES)}"
            )

        workspace_id = uuid.uuid4().hex[:12]
        action = {"operation": "create", "workspace_type": workspace_type, "title": title}

        contract.check_preconditions(action)
        contract.check_invariants()
        contract.check_postconditions(action, {"content": content})

        ws = Workspace(
            id=workspace_id, type=workspace_type, title=title, content=content,
            created_at=datetime.now(timezone.utc), contract=contract,
        )
        ws._session_id = session_id  # type: ignore[attr-defined]
        self._workspaces[workspace_id] = ws

        if self._store:
            self._store.save_workspace(ws, session_id)

        logger.info("Created workspace %s (%s): %s", workspace_id, workspace_type, title)
        return ws

    def get(self, workspace_id: str) -> Workspace:
        ws = self._workspaces.get(workspace_id)
        if ws is None:
            raise WorkspaceNotFound(f"No workspace with id '{workspace_id}'")
        return ws

    def update(
        self,
        workspace_id: str,
        title: str | None = None,
        content: str | None = None,
        _skip_store: bool = False,
    ) -> Workspace:
        ws = self.get(workspace_id)
        if not ws.is_active:
            raise WorkspaceClosed(f"Workspace '{workspace_id}' is closed")

        action = {"operation": "update", "workspace_type": ws.type, "workspace_id": workspace_id}
        ws.contract.check_preconditions(action)
        ws.contract.check_invariants()

        new_content = content if content is not None else ws.content
        ws.contract.check_postconditions(action, {"content": new_content})

        if title is not None:
            ws.title = title
        if content is not None:
            ws.content = content

        if self._store and not _skip_store:
            self._store.update_workspace(ws)

        logger.info("Updated workspace %s", workspace_id)
        return ws

    def close(self, workspace_id: str) -> Workspace:
        ws = self.get(workspace_id)
        if not ws.is_active:
            return ws

        ws.closed_at = datetime.now(timezone.utc)

        if self._store:
            self._store.close_workspace(workspace_id, ws.closed_at)

        logger.info("Closed workspace %s", workspace_id)
        return ws

    def list_active(self) -> list[Workspace]:
        return sorted(
            (ws for ws in self._workspaces.values() if ws.is_active),
            key=lambda ws: ws.created_at,
        )

    def list_all(self) -> list[Workspace]:
        return sorted(self._workspaces.values(), key=lambda ws: ws.created_at)
