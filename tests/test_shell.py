"""Tests for CAS shell — session manager and intent detection.

Verifies deterministic intent detection, session lifecycle,
workspace creation/edit/close through chat messages, conversation
history tracking, and response structure.
"""

import pytest

from cas.memory_store import InMemoryStore
from cas.shell import (
    Intent,
    Message,
    Session,
    Shell,
    ShellResponse,
    detect_intent,
)
from cas.workspaces import WorkspaceNotFound


# ── Intent detection ─────────────────────────────────────────────────


class TestIntentDetection:
    # -- Create intents --

    @pytest.mark.parametrize("message", [
        "write a project proposal",
        "draft a report on Q1 results",
        "create a document about testing",
        "I need to write a memo",
        "start a new essay",
        "compose a letter to the team",
        "begin drafting a plan",
        "Write a proposal for the new system",
        "new document",
        "new note",
        "create work ticket template",
        "create a template for onboarding",
        "write a guide for new users",
        "create a form for expenses",
        "draft a ticket",
    ])
    def test_detects_create_intent(self, message):
        intent = detect_intent(message)
        assert intent.kind == "create_workspace", f"Expected create for: {message}"

    @pytest.mark.parametrize("message", [
        "write a project proposal",
        "draft a report on Q1 results",
        "create a document about testing",
    ])
    def test_create_intent_has_title_hint(self, message):
        intent = detect_intent(message)
        assert intent.title_hint != ""

    def test_title_hint_extracts_subject(self):
        intent = detect_intent("write a project proposal")
        assert "Project Proposal" in intent.title_hint

    def test_title_hint_caps_length(self):
        intent = detect_intent(
            "write a very long document title that goes on and on and on forever"
        )
        words = intent.title_hint.split()
        assert len(words) <= 6

    # -- Edit intents --

    @pytest.mark.parametrize("message", [
        "edit the introduction",
        "update the summary section",
        "change the title",
        "modify the conclusion",
        "revise paragraph two",
        "add a section for budget",
        "append a references list",
        "insert a table of contents",
    ])
    def test_detects_edit_intent(self, message):
        intent = detect_intent(message)
        assert intent.kind == "edit_workspace", f"Expected edit for: {message}"

    # -- Close intents --

    @pytest.mark.parametrize("message", [
        "close the workspace",
        "done with the document",
        "finish the editor",
        "dismiss the workspace",
        "discard the document",
        "close the doc",
    ])
    def test_detects_close_intent(self, message):
        intent = detect_intent(message)
        assert intent.kind == "close_workspace", f"Expected close for: {message}"

    # -- Chat fallback --

    @pytest.mark.parametrize("message", [
        "hello",
        "what time is it",
        "how does this work",
        "tell me about CAS",
        "thanks",
    ])
    def test_detects_chat_intent(self, message):
        intent = detect_intent(message)
        assert intent.kind == "chat", f"Expected chat for: {message}"

    # -- Priority: close > edit > create > chat --

    def test_close_takes_priority_over_edit(self):
        # "close" + "edit" → close wins
        intent = detect_intent("close the document, don't edit it")
        assert intent.kind == "close_workspace"

    def test_close_takes_priority_over_create(self):
        intent = detect_intent("close the document, don't create a new one")
        assert intent.kind == "close_workspace"

    # -- Self-edit exclusions: user edits manually, no LLM call --

    @pytest.mark.parametrize("message", [
        "edit directly",
        "i'll edit it myself",
        "i'll do it",
        "let me edit",
        "let me fix it",
        "i'll take it from here",
        "just edit it",
        "i'll handle it",
        "i can do it myself",
    ])
    def test_self_edit_routes_to_chat(self, message):
        intent = detect_intent(message)
        assert intent.kind == "chat", (
            f"Expected chat (user editing manually) for: {message!r}, "
            f"got {intent.kind!r}"
        )


# ── Session lifecycle ────────────────────────────────────────────────


class TestSessionLifecycle:
    def test_create_session(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        assert len(session.id) == 12
        assert session.created_at is not None
        assert session.history == []

    def test_create_multiple_sessions(self):
        shell = Shell(store=InMemoryStore())
        s1 = shell.create_session()
        s2 = shell.create_session()
        assert s1.id != s2.id

    def test_get_session(self):
        shell = Shell(store=InMemoryStore())
        s = shell.create_session()
        fetched = shell.get_session(s.id)
        assert fetched is s

    def test_get_nonexistent_session_raises(self):
        shell = Shell(store=InMemoryStore())
        with pytest.raises(KeyError, match="No session"):
            shell.get_session("nonexistent")


# ── Conversation history ─────────────────────────────────────────────


class TestConversationHistory:
    def test_messages_recorded(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        shell.process_message(session.id, "hello")

        assert len(session.history) == 2  # user + shell
        assert session.history[0].role == "user"
        assert session.history[0].text == "hello"
        assert session.history[1].role == "shell"

    def test_multiple_messages_accumulate(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        shell.process_message(session.id, "hello")
        shell.process_message(session.id, "how are you")

        assert len(session.history) == 4  # 2 user + 2 shell

    def test_message_has_timestamp(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        shell.process_message(session.id, "hello")

        assert session.history[0].timestamp is not None
        assert session.history[0].timestamp.tzinfo is not None


# ── Workspace creation via chat ──────────────────────────────────────


class TestWorkspaceCreationViaChat:
    def test_create_workspace_from_message(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        response = shell.process_message(session.id, "write a project proposal")

        assert response.workspace is not None
        assert response.workspace.is_active
        assert response.workspace.type == "document"
        assert response.intent.kind == "create_workspace"
        assert "workspace" in response.chat_reply.lower()

    def test_workspace_title_from_message(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        response = shell.process_message(session.id, "draft a budget report")

        assert response.workspace is not None
        assert response.workspace.title != ""

    def test_workspace_has_initial_content(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        response = shell.process_message(session.id, "write a project proposal")

        assert response.workspace.content.startswith("#")

    def test_workspace_registered_in_manager(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        response = shell.process_message(session.id, "create a document about testing")

        active = shell.workspaces.list_active()
        assert len(active) == 1
        assert active[0].id == response.workspace.id

    def test_workspace_bound_to_contract(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        response = shell.process_message(session.id, "write a memo")

        ws = response.workspace
        assert ws.contract is not None
        assert ws.contract.agent_name == "cas-workspace"

    def test_multiple_creates_make_multiple_workspaces(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        shell.process_message(session.id, "write a proposal")
        shell.process_message(session.id, "draft a report")

        assert len(shell.workspaces.list_active()) == 2


# ── Workspace editing via chat ───────────────────────────────────────


class TestWorkspaceEditViaChat:
    def test_edit_updates_most_recent_workspace(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        shell.process_message(session.id, "write a proposal")
        response = shell.process_message(session.id, "add a section for budget")

        assert response.intent.kind == "edit_workspace"
        assert response.workspace is not None
        assert "budget" in response.workspace.content
        assert "Updated workspace" in response.chat_reply

    def test_edit_with_no_workspace_returns_message(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        response = shell.process_message(session.id, "edit the introduction")

        assert response.workspace is None
        assert "No active workspace" in response.chat_reply

    def test_edit_targets_latest_active(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        r1 = shell.process_message(session.id, "write a first doc")
        r2 = shell.process_message(session.id, "write a second doc")
        response = shell.process_message(session.id, "update the summary")

        # Should edit the second (most recent) workspace
        assert response.workspace.id == r2.workspace.id


# ── Workspace close via chat ─────────────────────────────────────────


class TestWorkspaceCloseViaChat:
    def test_close_most_recent_workspace(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        shell.process_message(session.id, "write a proposal")
        response = shell.process_message(session.id, "close the document")

        assert response.intent.kind == "close_workspace"
        assert response.workspace is not None
        assert not response.workspace.is_active
        assert "Closed workspace" in response.chat_reply

    def test_close_with_no_workspace_returns_message(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        response = shell.process_message(session.id, "close the workspace")

        assert response.workspace is None
        assert "No active workspace" in response.chat_reply

    def test_close_reduces_active_count(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        shell.process_message(session.id, "write a doc")
        shell.process_message(session.id, "draft a report")
        assert len(shell.workspaces.list_active()) == 2

        shell.process_message(session.id, "close the document")
        assert len(shell.workspaces.list_active()) == 1


# ── Chat fallback ───────────────────────────────────────────────────


class TestChatFallback:
    def test_non_workspace_message_returns_chat(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        response = shell.process_message(session.id, "hello there")

        assert response.workspace is None
        assert response.intent.kind == "chat"
        assert response.chat_reply != ""


# ── Response serialization ───────────────────────────────────────────


class TestResponseSerialization:
    def test_response_to_dict_with_workspace(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        response = shell.process_message(session.id, "write a memo")
        d = response.to_dict()

        assert "chat_reply" in d
        assert "workspace" in d
        assert d["workspace"] is not None
        assert d["intent"] == "create_workspace"

    def test_response_to_dict_without_workspace(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        response = shell.process_message(session.id, "hello")
        d = response.to_dict()

        assert d["workspace"] is None
        assert d["intent"] == "chat"


# ── Custom contract config ───────────────────────────────────────────


class TestCustomContractConfig:
    def test_shell_with_custom_contract(self):
        config = {
            "allowed_workspace_types": ["document"],
            "max_workspace_size_kb": 1,  # very small
            "network_access": False,
        }
        shell = Shell(contract_config=config)
        session = shell.create_session()
        response = shell.process_message(session.id, "write a short note")

        assert response.workspace is not None
        # The contract should be applied
        assert response.workspace.contract.agent_name == "cas-workspace"


# ── Session isolation ────────────────────────────────────────────────


class TestSessionIsolation:
    def test_sessions_have_independent_history(self):
        shell = Shell(store=InMemoryStore())
        s1 = shell.create_session()
        s2 = shell.create_session()

        shell.process_message(s1.id, "hello from session 1")
        shell.process_message(s2.id, "hello from session 2")

        assert len(s1.history) == 2
        assert len(s2.history) == 2
        assert "session 1" in s1.history[0].text
        assert "session 2" in s2.history[0].text

    def test_sessions_share_workspace_manager(self):
        """All sessions in a shell share the same WorkspaceManager."""
        shell = Shell(store=InMemoryStore())
        s1 = shell.create_session()
        s2 = shell.create_session()

        shell.process_message(s1.id, "write a doc")
        # s2 can see workspaces created by s1
        assert len(shell.workspaces.list_active()) == 1
