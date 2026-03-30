"""Tests for multiple workspace types — detection, rendering, API."""

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from cas.shell import Shell, detect_intent
from cas.renderer import render, render_with_styles
from cas.api import create_router


@pytest.fixture
def client():
    shell = Shell()
    app = FastAPI()
    app.include_router(create_router(shell))
    return TestClient(app)


# ── Intent type detection ─────────────────────────────────────────────

class TestWorkspaceTypeDetection:

    @pytest.mark.parametrize("message", [
        "write a project proposal",
        "draft a resume for a developer",
        "create a memo about the meeting",
        "write a letter to the client",
        "draft a blog post about Python",
    ])
    def test_detects_document_type(self, message):
        intent = detect_intent(message)
        assert intent.kind == "create_workspace"
        assert intent.ws_type == "document", f"Expected document for: {message!r}"

    @pytest.mark.parametrize("message", [
        "write a python script to parse csv files",
        "create a function to sort a list",
        "draft a bash script for deployment",
        "write a test for the login endpoint",
        "create a dockerfile for my app",
    ])
    def test_detects_code_type(self, message):
        intent = detect_intent(message)
        assert intent.kind == "create_workspace"
        assert intent.ws_type == "code", f"Expected code for: {message!r}"

    @pytest.mark.parametrize("message", [
        "create a todo list for the sprint",
        "write a checklist for deployment",
        "make a list of requirements",
        "draft a shopping list",
        "create a new list of tasks",
    ])
    def test_detects_list_type(self, message):
        intent = detect_intent(message)
        assert intent.kind == "create_workspace"
        assert intent.ws_type == "list", f"Expected list for: {message!r}"

    def test_default_type_is_document(self):
        # A generic create that doesn't match code/list should default to document
        intent = detect_intent("write a proposal")
        assert intent.ws_type == "document"

    def test_intent_has_ws_type_field(self):
        intent = detect_intent("write a memo")
        assert hasattr(intent, "ws_type")
        assert intent.ws_type in ("document", "code", "list")


# ── Renderer ─────────────────────────────────────────────────────────

class TestTypeAwareRenderer:

    def test_document_renders_as_cas_doc(self):
        html = render("# Title\n\nBody.", ws_type="document")
        assert 'class="cas-doc"' in html
        assert "<h1>" in html

    def test_code_renders_as_cas_code(self):
        html = render("print('hello')", ws_type="code")
        assert 'class="cas-code"' in html
        assert "<pre>" in html
        # Should NOT do markdown processing — raw content
        assert "print" in html

    def test_code_escapes_html(self):
        html = render("<script>alert('xss')</script>", ws_type="code")
        assert "<script>" not in html
        assert "&lt;script&gt;" in html

    def test_list_renders_as_cas_list(self):
        html = render("# My List\n\n- item one\n- item two\n", ws_type="list")
        assert 'class="cas-list"' in html
        assert "<li>" in html

    def test_default_type_renders_document(self):
        html = render("# Title\n\nBody.")
        assert 'class="cas-doc"' in html

    def test_render_with_styles_embeds_css(self):
        html = render_with_styles("# Title", ws_type="document")
        assert "<style>" in html
        assert "cas-doc" in html

    def test_render_with_styles_code(self):
        html = render_with_styles("x = 1", ws_type="code")
        assert "<style>" in html
        assert "cas-code" in html

    def test_workspace_css_contains_all_classes(self):
        from cas.renderer import WORKSPACE_CSS
        assert "cas-doc" in WORKSPACE_CSS
        assert "cas-code" in WORKSPACE_CSS
        assert "cas-list" in WORKSPACE_CSS


# ── Shell type routing ────────────────────────────────────────────────

class TestShellTypeRouting:

    def test_code_workspace_created_with_correct_type(self):
        shell = Shell()
        session = shell.create_session()
        resp = shell.process_message(session.id, "write a python script to sort a list")
        assert resp.workspace is not None
        assert resp.workspace.type == "code"

    def test_list_workspace_created_with_correct_type(self):
        shell = Shell()
        session = shell.create_session()
        resp = shell.process_message(session.id, "create a todo list for the project")
        assert resp.workspace is not None
        assert resp.workspace.type == "list"

    def test_document_workspace_default_type(self):
        shell = Shell()
        session = shell.create_session()
        resp = shell.process_message(session.id, "write a project proposal")
        assert resp.workspace.type == "document"

    def test_reply_includes_type_name(self):
        shell = Shell()
        session = shell.create_session()
        resp = shell.process_message(session.id, "write a python script to parse csv")
        assert "code" in resp.chat_reply.lower()


# ── API type routing ──────────────────────────────────────────────────

class TestAPITypeRouting:

    def test_api_creates_code_workspace(self, client):
        r = client.post("/api/cas/message", json={"message": "write a python script to sort"})
        assert r.status_code == 200
        assert r.json()["workspace"]["type"] == "code"

    def test_api_creates_list_workspace(self, client):
        r = client.post("/api/cas/message", json={"message": "create a todo list for sprint"})
        assert r.status_code == 200
        assert r.json()["workspace"]["type"] == "list"

    def test_render_uses_type(self, client):
        # Create a code workspace, render it, check it uses code class
        r = client.post("/api/cas/message", json={"message": "write a python script to sort"})
        ws_id = r.json()["workspace"]["id"]
        # Inject known code content
        client.put(f"/api/cas/workspace/{ws_id}", json={"content": "print('hello')"})
        r = client.get(f"/api/cas/workspace/{ws_id}/render")
        assert "cas-code" in r.text

    def test_export_html_uses_type_styles(self, client):
        r = client.post("/api/cas/message", json={"message": "write a python script to sort"})
        ws_id = r.json()["workspace"]["id"]
        r = client.get(f"/api/cas/workspace/{ws_id}/export/html")
        assert "cas-code" in r.text

    def test_workspaces_list_includes_type(self, client):
        client.post("/api/cas/message", json={"message": "write a proposal"})
        client.post("/api/cas/message", json={"message": "write a python script to parse"})
        r = client.get("/api/cas/workspaces")
        types = {ws["type"] for ws in r.json()}
        assert "document" in types
        assert "code" in types
