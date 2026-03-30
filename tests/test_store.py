"""Tests for CASStore — SQLite persistence layer."""
import tempfile
from datetime import datetime, timezone
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from cas.store import CASStore
from cas.shell import Message, Session, Shell
from cas.workspaces import WorkspaceManager


@pytest.fixture
def store(tmp_path):
    """Fresh in-memory (temp file) CASStore for each test.

    We use a temp file rather than :memory: so that we can test
    re-open / restore behaviour by creating a second CASStore on
    the same path.
    """
    db = tmp_path / "test.db"
    # Override the autouse mock_store for this module — we want a real store
    s = CASStore(db_path=db)
    yield s
    s.close()


# ── Schema / open ────────────────────────────────────────────────────

class TestStoreOpen:
    def test_creates_db_file(self, tmp_path):
        db = tmp_path / "new.db"
        assert not db.exists()
        s = CASStore(db_path=db)
        assert db.exists()
        s.close()

    def test_idempotent_open(self, tmp_path):
        db = tmp_path / "idem.db"
        s1 = CASStore(db_path=db)
        s1.close()
        s2 = CASStore(db_path=db)  # should not raise
        s2.close()


# ── Sessions ─────────────────────────────────────────────────────────

class TestSessions:
    def test_save_and_load_session(self, store):
        session = Session(id="abc123", created_at=datetime.now(timezone.utc))
        store.save_session(session)
        rows = store.load_sessions()
        assert "abc123" in rows
        assert rows["abc123"]["id"] == "abc123"

    def test_save_session_idempotent(self, store):
        session = Session(id="abc123", created_at=datetime.now(timezone.utc))
        store.save_session(session)
        store.save_session(session)  # INSERT OR IGNORE — should not raise
        assert len(store.load_sessions()) == 1

    def test_load_empty(self, store):
        assert store.load_sessions() == {}

    def test_multiple_sessions(self, store):
        for i in range(3):
            s = Session(id=f"sess{i}", created_at=datetime.now(timezone.utc))
            store.save_session(s)
        assert len(store.load_sessions()) == 3


# ── Messages ─────────────────────────────────────────────────────────

class TestMessages:
    def _make_session(self, store, sid="s1"):
        s = Session(id=sid, created_at=datetime.now(timezone.utc))
        store.save_session(s)
        return s

    def test_save_and_load_message(self, store):
        s = self._make_session(store)
        msg = Message(role="user", text="hello", timestamp=datetime.now(timezone.utc))
        store.save_message(s.id, msg)
        rows = store.load_messages(s.id)
        assert len(rows) == 1
        assert rows[0]["role"] == "user"
        assert rows[0]["text"] == "hello"

    def test_messages_ordered_by_insertion(self, store):
        s = self._make_session(store)
        for text in ["first", "second", "third"]:
            store.save_message(s.id, Message(role="user", text=text, timestamp=datetime.now(timezone.utc)))
        rows = store.load_messages(s.id)
        assert [r["text"] for r in rows] == ["first", "second", "third"]

    def test_messages_isolated_by_session(self, store):
        s1 = self._make_session(store, "s1")
        s2 = self._make_session(store, "s2")
        store.save_message(s1.id, Message(role="user", text="for s1", timestamp=datetime.now(timezone.utc)))
        store.save_message(s2.id, Message(role="user", text="for s2", timestamp=datetime.now(timezone.utc)))
        assert store.load_messages(s1.id)[0]["text"] == "for s1"
        assert store.load_messages(s2.id)[0]["text"] == "for s2"

    def test_load_messages_empty(self, store):
        assert store.load_messages("nonexistent") == []


# ── Workspaces ────────────────────────────────────────────────────────

class TestWorkspaces:
    def _make_ws(self, store, ws_id="ws1", title="Test Doc", session_id="s1"):
        from cas.contracts import load_contract_from_config
        from cas.workspaces import Workspace

        contract = load_contract_from_config("cas-workspace", {
            "allowed_workspace_types": ["document"],
            "max_workspace_size_kb": 512,
            "network_access": False,
        })
        ws = Workspace(
            id=ws_id, type="document", title=title,
            content=f"# {title}\n\nContent.",
            created_at=datetime.now(timezone.utc),
            contract=contract,
        )
        store.save_workspace(ws, session_id)
        return ws

    def test_save_and_load_workspace(self, store):
        self._make_ws(store)
        rows = store.load_workspaces(lambda: MagicMock())
        assert "ws1" in rows
        assert rows["ws1"]["title"] == "Test Doc"

    def test_load_only_active(self, store):
        ws = self._make_ws(store)
        store.close_workspace("ws1", datetime.now(timezone.utc))
        rows = store.load_workspaces(lambda: MagicMock())
        assert "ws1" not in rows

    def test_update_workspace(self, store):
        from cas.workspaces import Workspace
        from cas.contracts import load_contract_from_config
        ws = self._make_ws(store)
        ws.title = "Updated Title"
        ws.content = "# Updated\n\nNew content."
        store.update_workspace(ws)
        rows = store.load_workspaces(lambda: MagicMock())
        assert rows["ws1"]["title"] == "Updated Title"
        assert "New content." in rows["ws1"]["content"]

    def test_multiple_workspaces(self, store):
        for i in range(3):
            self._make_ws(store, ws_id=f"ws{i}", title=f"Doc {i}")
        rows = store.load_workspaces(lambda: MagicMock())
        assert len(rows) == 3

    def test_contract_factory_called_per_workspace(self, store):
        self._make_ws(store, "ws1")
        self._make_ws(store, "ws2")
        factory = MagicMock(return_value=MagicMock())
        store.load_workspaces(factory)
        assert factory.call_count == 2


# ── Persistence across restarts ───────────────────────────────────────

class TestPersistenceAcrossRestarts:
    """Verify that state survives a store close + reopen."""

    def test_session_survives_reopen(self, tmp_path):
        db = tmp_path / "persist.db"
        s1 = CASStore(db_path=db)
        session = Session(id="persist1", created_at=datetime.now(timezone.utc))
        s1.save_session(session)
        s1.close()

        s2 = CASStore(db_path=db)
        rows = s2.load_sessions()
        assert "persist1" in rows
        s2.close()

    def test_workspace_survives_reopen(self, tmp_path):
        from cas.workspaces import Workspace
        from cas.contracts import load_contract_from_config

        db = tmp_path / "persist.db"
        s1 = CASStore(db_path=db)
        contract = load_contract_from_config("cas-workspace", {
            "allowed_workspace_types": ["document"],
            "max_workspace_size_kb": 512,
            "network_access": False,
        })
        ws = Workspace(
            id="persist_ws", type="document", title="Persisted",
            content="# Persisted\n\nStill here.",
            created_at=datetime.now(timezone.utc),
            contract=contract,
        )
        s1.save_workspace(ws, "s1")
        s1.close()

        s2 = CASStore(db_path=db)
        rows = s2.load_workspaces(lambda: MagicMock())
        assert "persist_ws" in rows
        assert rows["persist_ws"]["title"] == "Persisted"
        s2.close()

    def test_messages_survive_reopen(self, tmp_path):
        db = tmp_path / "persist.db"
        s1 = CASStore(db_path=db)
        session = Session(id="s1", created_at=datetime.now(timezone.utc))
        s1.save_session(session)
        s1.save_message("s1", Message(role="user", text="hello", timestamp=datetime.now(timezone.utc)))
        s1.close()

        s2 = CASStore(db_path=db)
        msgs = s2.load_messages("s1")
        assert len(msgs) == 1
        assert msgs[0]["text"] == "hello"
        s2.close()


# ── Shell restore integration ─────────────────────────────────────────

class TestShellRestore:
    """Verify Shell._restore() wires up correctly from a real store."""

    def test_shell_restores_workspaces(self, tmp_path):
        from cas.workspaces import Workspace
        from cas.contracts import load_contract_from_config

        db = tmp_path / "shell.db"

        # First Shell instance — create workspace
        store1 = CASStore(db_path=db)
        contract = load_contract_from_config("cas-workspace", {
            "allowed_workspace_types": ["document"],
            "max_workspace_size_kb": 512,
            "network_access": False,
        })
        ws = Workspace(
            id="restore_ws", type="document", title="Restored Doc",
            content="# Restored\n\nContent.",
            created_at=datetime.now(timezone.utc),
            contract=contract,
        )
        store1.save_workspace(ws, "sess1")
        store1.close()

        # Second Shell instance — should see the workspace
        store2 = CASStore(db_path=db)
        with patch("cas.shell.get_cas_auditor", return_value=MagicMock()), \
             patch("cas.conductor.Conductor._save", return_value=None), \
             patch("cas.conductor.Conductor._load", return_value={
                 "doc_types": {}, "edit_verbs": {}, "phrases": [],
                 "session_count": 0, "message_count": 0,
                 "workspace_count": 0, "last_seen": None,
             }), \
             patch("cas.shell.generate_workspace_content", return_value="# T\n\n"), \
             patch("cas.shell.generate_workspace_edit", return_value=""), \
             patch("cas.shell.generate_chat_reply", return_value="ok"):
            shell = Shell(store=store2)
            active = shell.workspaces.list_active()
            assert len(active) == 1
            assert active[0].title == "Restored Doc"
        store2.close()
