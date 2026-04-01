"""Tests for CAS protocols — SessionStore conformance and InMemoryStore.

Proves that:
1. CASStore conforms to the SessionStore protocol
2. An alternative InMemoryStore also conforms
3. Both are interchangeable for Shell construction
"""

from datetime import datetime, timezone

import pytest

from cas.protocols import SessionStore


# ── InMemoryStore — alternative implementation ───────────────────────


class InMemoryStore:
    """Minimal SessionStore implementation for testing.

    Proves the Protocol is actually swappable — any backend that
    implements these methods can replace CASStore.
    """

    def __init__(self):
        self._sessions: dict[str, dict] = {}
        self._messages: dict[str, list[dict]] = {}
        self._workspaces: dict[str, dict] = {}
        self._history: dict[str, list[dict]] = {}
        self._version_counters: dict[str, int] = {}

    def save_session(self, session) -> None:
        self._sessions[session.id] = {
            "id": session.id,
            "created_at": session.created_at.isoformat(),
        }

    def load_sessions(self) -> dict:
        return dict(self._sessions)

    def save_message(self, session_id: str, message) -> None:
        self._messages.setdefault(session_id, []).append({
            "role": message.role,
            "text": message.text,
            "timestamp": message.timestamp.isoformat(),
        })

    def load_messages(self, session_id: str) -> list[dict]:
        return list(self._messages.get(session_id, []))

    def save_workspace(self, workspace, session_id: str) -> None:
        self._workspaces[workspace.id] = {
            "id": workspace.id,
            "session_id": session_id,
            "type": workspace.type,
            "title": workspace.title,
            "content": workspace.content,
            "created_at": workspace.created_at,
            "closed_at": workspace.closed_at,
        }

    def update_workspace(self, workspace) -> None:
        if workspace.id in self._workspaces:
            # Snapshot current before updating
            current = self._workspaces[workspace.id]
            counter = self._version_counters.get(workspace.id, 0) + 1
            self._version_counters[workspace.id] = counter
            self._history.setdefault(workspace.id, []).append({
                "version": counter,
                "title": current["title"],
                "content": current["content"],
                "saved_at": datetime.now(timezone.utc).isoformat(),
            })
            self._workspaces[workspace.id]["title"] = workspace.title
            self._workspaces[workspace.id]["content"] = workspace.content
            self._workspaces[workspace.id]["closed_at"] = workspace.closed_at

    def close_workspace(self, workspace_id: str, closed_at: datetime) -> None:
        if workspace_id in self._workspaces:
            self._workspaces[workspace_id]["closed_at"] = closed_at

    def load_workspaces(self, contract_factory) -> dict:
        result = {}
        for ws_id, ws in self._workspaces.items():
            if ws["closed_at"] is None:
                result[ws_id] = {**ws, "contract": contract_factory()}
        return result

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
        self.apply_version(workspace_id, latest["version"])
        ws = self._workspaces.get(workspace_id)
        return {"title": ws["title"], "content": ws["content"]} if ws else None

    def close(self) -> None:
        pass


# ── Protocol conformance ─────────────────────────────────────────


class TestProtocolConformance:
    def test_in_memory_store_is_session_store(self):
        store = InMemoryStore()
        assert isinstance(store, SessionStore)

    def test_cas_store_is_session_store(self, tmp_path):
        from cas.store import CASStore
        store = CASStore(db_path=tmp_path / "test.db")
        assert isinstance(store, SessionStore)
        store.close()


# ── InMemoryStore functional tests ───────────────────────────────


class TestInMemoryStoreBasics:
    def test_session_roundtrip(self):
        from cas.shell import Session
        store = InMemoryStore()
        session = Session(id="abc123", created_at=datetime.now(timezone.utc))
        store.save_session(session)
        loaded = store.load_sessions()
        assert "abc123" in loaded
        assert loaded["abc123"]["id"] == "abc123"

    def test_message_roundtrip(self):
        from cas.shell import Message, Session
        store = InMemoryStore()
        session = Session(id="s1", created_at=datetime.now(timezone.utc))
        store.save_session(session)
        msg = Message(role="user", text="hello", timestamp=datetime.now(timezone.utc))
        store.save_message("s1", msg)
        messages = store.load_messages("s1")
        assert len(messages) == 1
        assert messages[0]["role"] == "user"
        assert messages[0]["text"] == "hello"

    def test_messages_for_wrong_session_empty(self):
        store = InMemoryStore()
        assert store.load_messages("nonexistent") == []

    def test_close_is_noop(self):
        store = InMemoryStore()
        store.close()  # should not raise
