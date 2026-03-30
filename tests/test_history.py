"""Tests for workspace history and undo functionality."""
import pytest
from datetime import datetime, timezone
from unittest.mock import MagicMock
from fastapi import FastAPI
from fastapi.testclient import TestClient

from cas.store import CASStore
from cas.api import create_router
from cas.shell import Shell


@pytest.fixture
def real_store(tmp_path):
    """Real CASStore backed by a temp file — bypasses mock_store autouse."""
    s = CASStore(db_path=tmp_path / "hist.db")
    yield s
    s.close()


@pytest.fixture
def client_real(real_store):
    """TestClient backed by a Shell with a real store."""
    from unittest.mock import patch
    with patch("cas.shell.get_cas_auditor", return_value=MagicMock()), \
         patch("cas.conductor.Conductor._save", return_value=None), \
         patch("cas.conductor.Conductor._load", return_value={
             "doc_types":{}, "edit_verbs":{}, "phrases":[],
             "session_count":0, "message_count":0,
             "workspace_count":0, "last_seen":None}), \
         patch("cas.api.get_cas_auditor", return_value=MagicMock()), \
         patch("cas.api.stream_chat", side_effect=lambda m,**k: iter([])):
        shell = Shell(store=real_store)
        app = FastAPI()
        app.include_router(create_router(shell))
        yield TestClient(app), shell, real_store


class TestStoreHistory:
    """Unit tests for CASStore history methods."""

    def test_snapshot_on_update(self, real_store):
        from cas.workspaces import Workspace
        from cas.contracts import load_contract_from_config
        contract = load_contract_from_config("cas-workspace", {
            "allowed_workspace_types": ["document"],
            "max_workspace_size_kb": 512,
            "network_access": False,
        })
        ws = Workspace(id="h1", type="document", title="Doc",
                       content="# Doc\n\nVersion 1.", created_at=datetime.now(timezone.utc),
                       contract=contract)
        real_store.save_workspace(ws, "s1")

        # First update should create version 1 snapshot of original
        ws.content = "# Doc\n\nVersion 2."
        real_store.update_workspace(ws)

        history = real_store.load_history("h1")
        assert len(history) == 1
        assert "Version 1." in history[0]["content"]

    def test_multiple_snapshots(self, real_store):
        from cas.workspaces import Workspace
        from cas.contracts import load_contract_from_config
        contract = load_contract_from_config("cas-workspace", {
            "allowed_workspace_types": ["document"],
            "max_workspace_size_kb": 512,
            "network_access": False,
        })
        ws = Workspace(id="h2", type="document", title="Doc",
                       content="v1", created_at=datetime.now(timezone.utc),
                       contract=contract)
        real_store.save_workspace(ws, "s1")

        for i in range(2, 5):
            ws.content = f"v{i}"
            real_store.update_workspace(ws)

        history = real_store.load_history("h2")
        assert len(history) == 3  # 3 snapshots (before each of 3 updates)
        # Newest first
        assert history[0]["version"] > history[1]["version"]

    def test_undo_restores_previous(self, real_store):
        from cas.workspaces import Workspace
        from cas.contracts import load_contract_from_config
        contract = load_contract_from_config("cas-workspace", {
            "allowed_workspace_types": ["document"],
            "max_workspace_size_kb": 512,
            "network_access": False,
        })
        ws = Workspace(id="h3", type="document", title="Doc",
                       content="original content", created_at=datetime.now(timezone.utc),
                       contract=contract)
        real_store.save_workspace(ws, "s1")

        ws.content = "changed content"
        real_store.update_workspace(ws)

        result = real_store.undo("h3")
        assert result is not None
        assert result["content"] == "original content"

    def test_undo_returns_none_with_no_history(self, real_store):
        from cas.workspaces import Workspace
        from cas.contracts import load_contract_from_config
        contract = load_contract_from_config("cas-workspace", {
            "allowed_workspace_types": ["document"],
            "max_workspace_size_kb": 512,
            "network_access": False,
        })
        ws = Workspace(id="h4", type="document", title="Doc",
                       content="only version", created_at=datetime.now(timezone.utc),
                       contract=contract)
        real_store.save_workspace(ws, "s1")

        result = real_store.undo("h4")
        assert result is None

    def test_get_specific_version(self, real_store):
        from cas.workspaces import Workspace
        from cas.contracts import load_contract_from_config
        contract = load_contract_from_config("cas-workspace", {
            "allowed_workspace_types": ["document"],
            "max_workspace_size_kb": 512,
            "network_access": False,
        })
        ws = Workspace(id="h5", type="document", title="Doc",
                       content="v1", created_at=datetime.now(timezone.utc),
                       contract=contract)
        real_store.save_workspace(ws, "s1")
        ws.content = "v2"
        real_store.update_workspace(ws)

        entry = real_store.get_version("h5", 1)
        assert entry is not None
        assert entry["content"] == "v1"

    def test_get_nonexistent_version_returns_none(self, real_store):
        assert real_store.get_version("nonexistent", 999) is None


class TestHistoryAPI:
    """Integration tests for history/undo API endpoints."""

    def _create_ws(self, client):
        r = client.post("/api/cas/message", json={"message": "write a proposal"})
        return r.json()["workspace"]["id"]

    def test_history_empty_initially(self, client_real):
        client, shell, store = client_real
        ws_id = self._create_ws(client)
        r = client.get(f"/api/cas/workspace/{ws_id}/history")
        assert r.status_code == 200
        assert r.json() == []

    def test_history_after_update(self, client_real):
        client, shell, store = client_real
        ws_id = self._create_ws(client)
        client.put(f"/api/cas/workspace/{ws_id}", json={"content": "# Doc\n\nEdited."})
        r = client.get(f"/api/cas/workspace/{ws_id}/history")
        assert r.status_code == 200
        assert len(r.json()) == 1
        assert "version" in r.json()[0]
        assert "saved_at" in r.json()[0]
        assert "content_length" in r.json()[0]
        # Full content not in summary
        assert "content" not in r.json()[0]

    def test_history_version_detail(self, client_real):
        client, shell, store = client_real
        ws_id = self._create_ws(client)
        original_content = shell.workspaces.get(ws_id).content
        client.put(f"/api/cas/workspace/{ws_id}", json={"content": "# Updated\n\nNew."})
        history = client.get(f"/api/cas/workspace/{ws_id}/history").json()
        version = history[0]["version"]
        r = client.get(f"/api/cas/workspace/{ws_id}/history/{version}")
        assert r.status_code == 200
        assert r.json()["content"] == original_content

    def test_history_nonexistent_workspace(self, client_real):
        client, _, _ = client_real
        r = client.get("/api/cas/workspace/nonexistent/history")
        assert r.status_code == 404

    def test_undo_restores_content(self, client_real):
        client, shell, store = client_real
        ws_id = self._create_ws(client)
        original = shell.workspaces.get(ws_id).content
        client.put(f"/api/cas/workspace/{ws_id}", json={"content": "# Changed\n\nDifferent."})
        r = client.post(f"/api/cas/workspace/{ws_id}/undo")
        assert r.status_code == 200
        assert r.json()["content"] == original

    def test_undo_no_history_returns_404(self, client_real):
        client, _, _ = client_real
        ws_id = self._create_ws(client)
        r = client.post(f"/api/cas/workspace/{ws_id}/undo")
        assert r.status_code == 404

    def test_undo_nonexistent_workspace(self, client_real):
        client, _, _ = client_real
        r = client.post("/api/cas/workspace/nonexistent/undo")
        assert r.status_code == 404

    def test_undo_is_undoable(self, client_real):
        """Undo should itself be undoable (snapshots before restoring)."""
        client, shell, store = client_real
        ws_id = self._create_ws(client)
        original = shell.workspaces.get(ws_id).content
        client.put(f"/api/cas/workspace/{ws_id}", json={"content": "# Edit 1\n\nA."})
        client.post(f"/api/cas/workspace/{ws_id}/undo")  # back to original
        # Undo the undo — should go back to Edit 1
        r = client.post(f"/api/cas/workspace/{ws_id}/undo")
        assert r.status_code == 200
        assert "Edit 1" in r.json()["content"]
