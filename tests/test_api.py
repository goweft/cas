"""Tests for CAS API routes.

Uses FastAPI's TestClient to verify all CAS endpoints:
POST /message, GET/PUT/DELETE /workspace, render, export, profile, styles.
"""

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from cas.api import create_router
from cas.shell import Shell


@pytest.fixture
def client():
    """Fresh Shell and TestClient for each test."""
    shell = Shell()
    app = FastAPI()
    app.include_router(create_router(shell))
    return TestClient(app)


@pytest.fixture
def client_with_workspace(client):
    """Client that already has one active workspace."""
    r = client.post("/api/cas/message", json={"message": "write a project proposal"})
    ws = r.json()["workspace"]
    sid = r.json()["session_id"]
    return client, sid, ws


# ── POST /api/cas/message ────────────────────────────────────────────


class TestSendMessage:
    def test_creates_session_when_none_provided(self, client):
        r = client.post("/api/cas/message", json={"message": "hello"})
        assert r.status_code == 200
        data = r.json()
        assert "session_id" in data
        assert "chat_reply" in data
        assert data["intent"] == "chat"

    def test_reuses_existing_session(self, client):
        r1 = client.post("/api/cas/message", json={"message": "hello"})
        sid = r1.json()["session_id"]
        r2 = client.post("/api/cas/message", json={"session_id": sid, "message": "hello again"})
        assert r2.status_code == 200
        assert r2.json()["session_id"] == sid

    def test_invalid_session_returns_404(self, client):
        r = client.post("/api/cas/message", json={"session_id": "nonexistent", "message": "hello"})
        assert r.status_code == 404

    def test_post_session_creates_new_session(self, client):
        r = client.post("/api/cas/session")
        assert r.status_code == 200
        data = r.json()
        assert "session_id" in data
        assert len(data["session_id"]) == 12
        assert data["messages"] == []

    def test_post_session_each_call_returns_unique_id(self, client):
        r1 = client.post("/api/cas/session")
        r2 = client.post("/api/cas/session")
        assert r1.json()["session_id"] != r2.json()["session_id"]

    def test_post_session_id_usable_in_messages(self, client):
        sid = client.post("/api/cas/session").json()["session_id"]
        r = client.post("/api/cas/message", json={"session_id": sid, "message": "hello"})
        assert r.status_code == 200
        assert r.json()["session_id"] == sid

    def test_create_workspace_via_message(self, client):
        r = client.post("/api/cas/message", json={"message": "write a project proposal"})
        assert r.status_code == 200
        data = r.json()
        assert data["intent"] == "create_workspace"
        assert data["workspace"] is not None
        assert data["workspace"]["type"] == "document"
        assert data["workspace"]["is_active"] is True

    def test_edit_workspace_via_message(self, client):
        r1 = client.post("/api/cas/message", json={"message": "write a proposal"})
        sid = r1.json()["session_id"]
        ws_id = r1.json()["workspace"]["id"]
        r2 = client.post("/api/cas/message", json={"session_id": sid, "message": "add a section for budget"})
        assert r2.status_code == 200
        assert r2.json()["intent"] == "edit_workspace"
        assert r2.json()["workspace"]["id"] == ws_id

    def test_close_workspace_via_message(self, client):
        r1 = client.post("/api/cas/message", json={"message": "write a proposal"})
        sid = r1.json()["session_id"]
        r2 = client.post("/api/cas/message", json={"session_id": sid, "message": "close the document"})
        assert r2.status_code == 200
        assert r2.json()["intent"] == "close_workspace"
        assert r2.json()["workspace"]["is_active"] is False


# ── GET /api/cas/workspaces ──────────────────────────────────────────


class TestListWorkspaces:
    def test_empty_list(self, client):
        r = client.get("/api/cas/workspaces")
        assert r.status_code == 200
        assert r.json() == []

    def test_lists_active_workspaces(self, client):
        client.post("/api/cas/message", json={"message": "write a proposal"})
        client.post("/api/cas/message", json={"message": "draft a report"})
        r = client.get("/api/cas/workspaces")
        assert r.status_code == 200
        assert len(r.json()) == 2

    def test_excludes_closed_workspaces(self, client):
        r1 = client.post("/api/cas/message", json={"message": "write a proposal"})
        sid = r1.json()["session_id"]
        client.post("/api/cas/message", json={"session_id": sid, "message": "close the document"})
        r = client.get("/api/cas/workspaces")
        assert len(r.json()) == 0


# ── GET /api/cas/workspace/{id} ─────────────────────────────────────


class TestGetWorkspace:
    def test_get_existing_workspace(self, client):
        r1 = client.post("/api/cas/message", json={"message": "write a proposal"})
        ws_id = r1.json()["workspace"]["id"]
        r = client.get(f"/api/cas/workspace/{ws_id}")
        assert r.status_code == 200
        assert r.json()["id"] == ws_id

    def test_get_nonexistent_returns_404(self, client):
        r = client.get("/api/cas/workspace/nonexistent")
        assert r.status_code == 404

    def test_get_closed_workspace_still_works(self, client):
        r1 = client.post("/api/cas/message", json={"message": "write a doc"})
        sid = r1.json()["session_id"]
        ws_id = r1.json()["workspace"]["id"]
        client.post("/api/cas/message", json={"session_id": sid, "message": "close the document"})
        r = client.get(f"/api/cas/workspace/{ws_id}")
        assert r.status_code == 200
        assert r.json()["is_active"] is False


# ── PUT /api/cas/workspace/{id} ─────────────────────────────────────


class TestUpdateWorkspace:
    def test_update_content(self, client):
        r1 = client.post("/api/cas/message", json={"message": "write a proposal"})
        ws_id = r1.json()["workspace"]["id"]
        r = client.put(f"/api/cas/workspace/{ws_id}", json={"content": "# Updated\n\nNew content."})
        assert r.status_code == 200
        assert r.json()["content"] == "# Updated\n\nNew content."

    def test_update_title(self, client):
        r1 = client.post("/api/cas/message", json={"message": "write a proposal"})
        ws_id = r1.json()["workspace"]["id"]
        r = client.put(f"/api/cas/workspace/{ws_id}", json={"title": "New Title"})
        assert r.status_code == 200
        assert r.json()["title"] == "New Title"

    def test_update_nonexistent_returns_404(self, client):
        r = client.put("/api/cas/workspace/nonexistent", json={"content": "x"})
        assert r.status_code == 404

    def test_update_closed_returns_409(self, client):
        r1 = client.post("/api/cas/message", json={"message": "write a doc"})
        sid = r1.json()["session_id"]
        ws_id = r1.json()["workspace"]["id"]
        client.post("/api/cas/message", json={"session_id": sid, "message": "close the document"})
        r = client.put(f"/api/cas/workspace/{ws_id}", json={"content": "nope"})
        assert r.status_code == 409


# ── DELETE /api/cas/workspace/{id} ───────────────────────────────────


class TestCloseWorkspace:
    def test_close_workspace(self, client):
        r1 = client.post("/api/cas/message", json={"message": "write a proposal"})
        ws_id = r1.json()["workspace"]["id"]
        r = client.delete(f"/api/cas/workspace/{ws_id}")
        assert r.status_code == 200
        assert r.json()["is_active"] is False

    def test_close_idempotent(self, client):
        r1 = client.post("/api/cas/message", json={"message": "write a proposal"})
        ws_id = r1.json()["workspace"]["id"]
        client.delete(f"/api/cas/workspace/{ws_id}")
        r = client.delete(f"/api/cas/workspace/{ws_id}")
        assert r.status_code == 200
        assert r.json()["is_active"] is False

    def test_close_nonexistent_returns_404(self, client):
        r = client.delete("/api/cas/workspace/nonexistent")
        assert r.status_code == 404


# ── GET /api/cas/workspace/{id}/render ──────────────────────────────


class TestRenderWorkspace:
    def test_render_returns_html(self, client):
        r1 = client.post("/api/cas/message", json={"message": "write a proposal"})
        ws_id = r1.json()["workspace"]["id"]
        r = client.get(f"/api/cas/workspace/{ws_id}/render")
        assert r.status_code == 200
        assert "text/html" in r.headers["content-type"]
        assert "cas-doc" in r.text

    def test_render_contains_heading(self, client):
        r1 = client.post("/api/cas/message", json={"message": "write a proposal"})
        ws_id = r1.json()["workspace"]["id"]
        # Put known content
        client.put(f"/api/cas/workspace/{ws_id}", json={"content": "# My Heading\n\nBody text."})
        r = client.get(f"/api/cas/workspace/{ws_id}/render")
        assert "<h1>" in r.text
        assert "My Heading" in r.text

    def test_render_nonexistent_returns_404(self, client):
        r = client.get("/api/cas/workspace/nonexistent/render")
        assert r.status_code == 404


# ── GET /api/cas/workspace/{id}/export/{fmt} ────────────────────────


class TestExportWorkspace:
    def _make_ws(self, client):
        r = client.post("/api/cas/message", json={"message": "write a proposal"})
        ws_id = r.json()["workspace"]["id"]
        client.put(f"/api/cas/workspace/{ws_id}", json={
            "content": "# My Proposal\n\n**Bold text** and *italic*.\n\n## Section\n\nContent here.\n"
        })
        return ws_id

    def test_export_markdown(self, client):
        ws_id = self._make_ws(client)
        r = client.get(f"/api/cas/workspace/{ws_id}/export/md")
        assert r.status_code == 200
        assert "text/markdown" in r.headers["content-type"]
        assert 'attachment' in r.headers["content-disposition"]
        assert ".md" in r.headers["content-disposition"]
        assert "# My Proposal" in r.text

    def test_export_html(self, client):
        ws_id = self._make_ws(client)
        r = client.get(f"/api/cas/workspace/{ws_id}/export/html")
        assert r.status_code == 200
        assert "text/html" in r.headers["content-type"]
        assert ".html" in r.headers["content-disposition"]
        assert "<style>" in r.text
        assert "<h1>" in r.text

    def test_export_txt(self, client):
        ws_id = self._make_ws(client)
        r = client.get(f"/api/cas/workspace/{ws_id}/export/txt")
        assert r.status_code == 200
        assert "text/plain" in r.headers["content-type"]
        assert ".txt" in r.headers["content-disposition"]
        assert "# " not in r.text        # headings stripped
        assert "My Proposal" in r.text   # but text preserved
        assert "**" not in r.text        # bold markers stripped

    def test_export_invalid_format_returns_400(self, client):
        r1 = client.post("/api/cas/message", json={"message": "write a proposal"})
        ws_id = r1.json()["workspace"]["id"]
        r = client.get(f"/api/cas/workspace/{ws_id}/export/pdf")
        assert r.status_code == 400

    def test_export_nonexistent_returns_404(self, client):
        r = client.get("/api/cas/workspace/nonexistent/export/md")
        assert r.status_code == 404

    def test_export_filename_derived_from_title(self, client):
        ws_id = self._make_ws(client)
        r = client.get(f"/api/cas/workspace/{ws_id}/export/md")
        assert "proposal" in r.headers["content-disposition"]


# ── GET /api/cas/styles/workspace.css ───────────────────────────────


class TestWorkspaceCSS:
    def test_returns_css(self, client):
        r = client.get("/api/cas/styles/workspace.css")
        assert r.status_code == 200
        assert "text/css" in r.headers["content-type"]
        assert "cas-doc" in r.text
        assert "font-family" in r.text


# ── GET/DELETE /api/cas/profile ─────────────────────────────────────


class TestProfile:
    def test_profile_returns_dict(self, client):
        r = client.get("/api/cas/profile")
        assert r.status_code == 200
        data = r.json()
        assert "doc_types" in data
        assert "edit_verbs" in data
        assert "session_count" in data
        assert "workspace_count" in data

    def test_profile_tracks_creates(self, client):
        client.post("/api/cas/message", json={"message": "write a resume"})
        client.post("/api/cas/message", json={"message": "draft a proposal"})
        r = client.get("/api/cas/profile")
        assert r.json()["workspace_count"] == 2

    def test_profile_reset(self, client):
        client.post("/api/cas/message", json={"message": "write a resume"})
        r = client.delete("/api/cas/profile")
        assert r.status_code == 200
        assert r.json()["reset"] is True
        profile = client.get("/api/cas/profile").json()
        assert profile["workspace_count"] == 0


# ── Static UI ────────────────────────────────────────────────────────


class TestStaticUI:
    def test_serves_index_html(self, client):
        r = client.get("/api/cas/")
        assert r.status_code == 200
        assert "text/html" in r.headers["content-type"]
        assert "CAS" in r.text


# ── Full workflow ────────────────────────────────────────────────────


class TestFullWorkflow:
    def test_create_edit_export_close(self, client):
        # Create
        r = client.post("/api/cas/message", json={"message": "write a project proposal"})
        sid = r.json()["session_id"]
        ws_id = r.json()["workspace"]["id"]

        # List — 1 active
        assert len(client.get("/api/cas/workspaces").json()) == 1

        # Update via REST
        client.put(f"/api/cas/workspace/{ws_id}", json={"content": "# Proposal\n\n## Goals\n\nShip it."})
        assert "Ship it." in client.get(f"/api/cas/workspace/{ws_id}").json()["content"]

        # Export all formats
        assert client.get(f"/api/cas/workspace/{ws_id}/export/md").status_code == 200
        assert client.get(f"/api/cas/workspace/{ws_id}/export/html").status_code == 200
        assert client.get(f"/api/cas/workspace/{ws_id}/export/txt").status_code == 200

        # Render
        assert client.get(f"/api/cas/workspace/{ws_id}/render").status_code == 200

        # Close
        r = client.delete(f"/api/cas/workspace/{ws_id}")
        assert r.json()["is_active"] is False

        # List — 0 active
        assert client.get("/api/cas/workspaces").json() == []
