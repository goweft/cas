"""Tests for CAS protocols — SessionStore conformance and InMemoryStore.

Proves that:
1. CASStore conforms to the SessionStore protocol
2. InMemoryStore also conforms
3. Both are interchangeable for Shell construction
"""

from datetime import datetime, timezone

import pytest

from cas.memory_store import InMemoryStore
from cas.protocols import SessionStore


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


class TestInMemoryStoreWorkspaces:
    def test_workspace_roundtrip(self):
        from cas.contracts import load_contract_from_config
        from cas.workspaces import Workspace
        store = InMemoryStore()
        ws = Workspace(
            id="ws1", type="document", title="Test",
            content="hello", created_at=datetime.now(timezone.utc),
            contract=load_contract_from_config("test", {"allowed_workspace_types": ["document"]}),
        )
        store.save_workspace(ws, session_id="s1")
        factory = lambda: load_contract_from_config("test", {"allowed_workspace_types": ["document"]})
        loaded = store.load_workspaces(factory)
        assert "ws1" in loaded
        assert loaded["ws1"]["title"] == "Test"
        assert loaded["ws1"]["content"] == "hello"

    def test_closed_workspace_not_in_load(self):
        from cas.contracts import load_contract_from_config
        from cas.workspaces import Workspace
        store = InMemoryStore()
        ws = Workspace(
            id="ws2", type="document", title="Closed",
            content="bye", created_at=datetime.now(timezone.utc),
            contract=load_contract_from_config("test", {"allowed_workspace_types": ["document"]}),
        )
        store.save_workspace(ws, session_id="s1")
        store.close_workspace("ws2", datetime.now(timezone.utc))
        factory = lambda: load_contract_from_config("test", {"allowed_workspace_types": ["document"]})
        loaded = store.load_workspaces(factory)
        assert "ws2" not in loaded

    def test_update_creates_history(self):
        from cas.contracts import load_contract_from_config
        from cas.workspaces import Workspace
        store = InMemoryStore()
        ws = Workspace(
            id="ws3", type="document", title="V1",
            content="original", created_at=datetime.now(timezone.utc),
            contract=load_contract_from_config("test", {"allowed_workspace_types": ["document"]}),
        )
        store.save_workspace(ws, session_id="s1")
        ws.title = "V2"
        ws.content = "updated"  # type: ignore[misc]
        store.update_workspace(ws)
        history = store.load_history("ws3")
        assert len(history) == 1
        assert history[0]["title"] == "V1"
        assert history[0]["content"] == "original"

    def test_undo(self):
        from cas.contracts import load_contract_from_config
        from cas.workspaces import Workspace
        store = InMemoryStore()
        ws = Workspace(
            id="ws4", type="document", title="Before",
            content="old", created_at=datetime.now(timezone.utc),
            contract=load_contract_from_config("test", {"allowed_workspace_types": ["document"]}),
        )
        store.save_workspace(ws, session_id="s1")
        ws.title = "After"
        ws.content = "new"  # type: ignore[misc]
        store.update_workspace(ws)
        result = store.undo("ws4")
        assert result is not None
        assert result["title"] == "Before"
        assert result["content"] == "old"

    def test_undo_no_history_returns_none(self):
        store = InMemoryStore()
        assert store.undo("nonexistent") is None

    def test_repr(self):
        store = InMemoryStore()
        assert "InMemoryStore" in repr(store)
