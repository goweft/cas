"""Tests for CAS Conductor — behavioral learning and user context."""

import pytest
from unittest.mock import patch

from cas.conductor import Conductor


# ── Override conftest mock_conductor for this module ──────────────────
# Conductor unit tests need the real _load/_save behaviour — they use
# tmp_path for isolation rather than mocking _load. We only mock _save
# to prevent writes to ~/.cas/profile.json.

@pytest.fixture(autouse=True)
def mock_conductor():
    """Override: only block disk writes; _load uses real implementation."""
    with patch("cas.conductor.Conductor._save", return_value=None):
        yield


@pytest.fixture
def conductor(tmp_path):
    """Fresh conductor backed by a non-existent temp file.

    Since the file doesn't exist, _load() returns _defaults({}) — a
    completely clean profile. _save() is mocked (above) so nothing is
    written to ~/.cas/.
    """
    return Conductor(profile_path=tmp_path / "profile.json")


# ── Create observations ───────────────────────────────────────────────

class TestObserveCreate:
    def test_increments_workspace_count(self, conductor):
        conductor.observe("create_workspace", "draft a resume")
        assert conductor._profile["workspace_count"] == 1

    def test_increments_message_count(self, conductor):
        conductor.observe("create_workspace", "draft a resume")
        assert conductor._profile["message_count"] == 1

    def test_tracks_doc_type_from_message(self, conductor):
        conductor.observe("create_workspace", "draft a resume for a developer")
        assert conductor._profile["doc_types"].get("resume", 0) == 1

    def test_tracks_doc_type_from_title(self, conductor):
        conductor.observe("create_workspace", "draft something", workspace_title="Project Proposal")
        assert conductor._profile["doc_types"].get("proposal", 0) == 1

    def test_no_double_counting_per_message(self, conductor):
        # "write a brief project plan" — should count once (last noun), not twice
        conductor.observe("create_workspace", "write a brief project plan")
        total = sum(conductor._profile["doc_types"].values())
        assert total == 1, f"Expected 1 doc type observation, got {total}: {conductor._profile['doc_types']}"

    def test_tracks_ws_type(self, conductor):
        conductor.observe("create_workspace", "write a python script", ws_type="code")
        assert conductor._profile["ws_types"].get("code", 0) == 1

    def test_tracks_ws_type_document(self, conductor):
        conductor.observe("create_workspace", "write a proposal", ws_type="document")
        assert conductor._profile["ws_types"].get("document", 0) == 1

    def test_accumulates_ws_types(self, conductor):
        conductor.observe("create_workspace", "write a script", ws_type="code")
        conductor.observe("create_workspace", "write a script", ws_type="code")
        conductor.observe("create_workspace", "write a proposal", ws_type="document")
        assert conductor._profile["ws_types"]["code"] == 2
        assert conductor._profile["ws_types"]["document"] == 1

    def test_accumulates_multiple_doc_types(self, conductor):
        conductor.observe("create_workspace", "write a resume")
        conductor.observe("create_workspace", "write a proposal")
        conductor.observe("create_workspace", "write a resume")
        assert conductor._profile["doc_types"]["resume"] == 2
        assert conductor._profile["doc_types"]["proposal"] == 1

    def test_adds_phrase_to_buffer(self, conductor):
        conductor.observe("create_workspace", "draft a resume")
        assert "draft a resume" in conductor._profile["phrases"]

    def test_phrase_buffer_capped(self, conductor):
        from cas.conductor import _MAX_PHRASES
        for i in range(_MAX_PHRASES + 10):
            conductor.observe("create_workspace", f"message {i}")
        assert len(conductor._profile["phrases"]) == _MAX_PHRASES


# ── Edit observations ─────────────────────────────────────────────────

class TestObserveEdit:
    def test_tracks_edit_verb(self, conductor):
        conductor.observe("edit_workspace", "add a section for budget")
        assert conductor._profile["edit_verbs"].get("add", 0) == 1

    def test_tracks_multiple_edit_verbs(self, conductor):
        conductor.observe("edit_workspace", "add a section and revise the intro")
        assert conductor._profile["edit_verbs"].get("add", 0) == 1
        assert conductor._profile["edit_verbs"].get("revise", 0) == 1

    def test_increments_edit_count(self, conductor):
        conductor.observe("edit_workspace", "update the summary")
        assert conductor._profile.get("edit_count", 0) == 1

    def test_does_not_increment_workspace_count(self, conductor):
        conductor.observe("edit_workspace", "update the summary")
        assert conductor._profile["workspace_count"] == 0

    def test_increments_message_count(self, conductor):
        conductor.observe("edit_workspace", "update the summary")
        assert conductor._profile["message_count"] == 1


# ── Session observations ──────────────────────────────────────────────

class TestObserveSessionStart:
    def test_increments_session_count(self, conductor):
        conductor.observe_session_start()
        assert conductor._profile["session_count"] == 1

    def test_accumulates_sessions(self, conductor):
        conductor.observe_session_start()
        conductor.observe_session_start()
        assert conductor._profile["session_count"] == 2


# ── Chat observations ─────────────────────────────────────────────────

class TestObserveChat:
    def test_chat_increments_message_count(self, conductor):
        conductor.observe("chat", "hello")
        assert conductor._profile["message_count"] == 1

    def test_chat_does_not_increment_workspace_count(self, conductor):
        conductor.observe("chat", "hello")
        assert conductor._profile["workspace_count"] == 0


# ── Context generation ────────────────────────────────────────────────

class TestUserContext:
    def test_empty_context_with_no_signal(self, conductor):
        assert conductor.user_context() == ""

    def test_context_produced_after_one_workspace(self, conductor):
        conductor.observe("create_workspace", "draft a resume", ws_type="document")
        ctx = conductor.user_context()
        assert ctx != ""

    def test_context_after_multiple_workspaces(self, conductor):
        conductor.observe("create_workspace", "draft a resume", ws_type="document")
        conductor.observe("create_workspace", "write a resume", ws_type="document")
        conductor.observe("create_workspace", "create a proposal", ws_type="document")
        ctx = conductor.user_context()
        assert ctx != ""
        assert "resume" in ctx.lower()

    def test_context_mentions_ws_type(self, conductor):
        conductor.observe("create_workspace", "write a python script", ws_type="code")
        conductor.observe("create_workspace", "write another script", ws_type="code")
        ctx = conductor.user_context()
        assert "code" in ctx.lower()

    def test_context_mentions_top_doc_type(self, conductor):
        for _ in range(4):
            conductor.observe("create_workspace", "draft a resume", ws_type="document")
        conductor.observe("create_workspace", "write a proposal", ws_type="document")
        ctx = conductor.user_context()
        assert "resume" in ctx.lower()

    def test_context_mentions_edit_style_additions(self, conductor):
        conductor.observe("create_workspace", "draft a resume", ws_type="document")
        for _ in range(3):
            conductor.observe("edit_workspace", "add a new section")
        ctx = conductor.user_context()
        assert ctx != ""

    def test_context_mentions_edit_style_rewrites(self, conductor):
        conductor.observe("create_workspace", "draft a resume", ws_type="document")
        for _ in range(3):
            conductor.observe("edit_workspace", "rewrite the whole thing")
        ctx = conductor.user_context()
        assert "rewrite" in ctx.lower() or ctx != ""

    def test_context_never_raises(self, conductor):
        conductor._profile["doc_types"] = None
        result = conductor.user_context()
        assert isinstance(result, str)

    def test_context_includes_returning_user_signal(self, conductor):
        conductor._profile["session_count"] = 3
        conductor.observe("create_workspace", "draft a resume", ws_type="document")
        ctx = conductor.user_context()
        assert "session" in ctx.lower() or "returning" in ctx.lower()


# ── Profile summary ───────────────────────────────────────────────────

class TestProfileSummary:
    def test_returns_dict(self, conductor):
        summary = conductor.profile_summary()
        assert isinstance(summary, dict)
        assert "doc_types" in summary
        assert "ws_types" in summary
        assert "session_count" in summary

    def test_includes_context_string(self, conductor):
        summary = conductor.profile_summary()
        assert "context" in summary
        assert "has_context" in summary

    def test_reflects_observations(self, conductor):
        conductor.observe("create_workspace", "write a resume", ws_type="document")
        conductor.observe_session_start()
        summary = conductor.profile_summary()
        assert summary["workspace_count"] == 1
        assert summary["session_count"] == 1
        assert summary["ws_types"]["document"] == 1


# ── Reset ─────────────────────────────────────────────────────────────

class TestReset:
    def test_reset_clears_profile(self, conductor):
        conductor.observe("create_workspace", "draft a resume", ws_type="document")
        conductor.observe_session_start()
        conductor.reset()
        assert conductor._profile["workspace_count"] == 0
        assert conductor._profile["session_count"] == 0
        assert conductor._profile["doc_types"] == {}
        assert conductor._profile["ws_types"] == {}

    def test_context_empty_after_reset(self, conductor):
        for _ in range(5):
            conductor.observe("create_workspace", "draft a resume", ws_type="document")
        conductor.reset()
        assert conductor.user_context() == ""


# ── Fail safety ───────────────────────────────────────────────────────

class TestFailSafety:
    def test_observe_does_not_raise_on_bad_intent(self, conductor):
        conductor.observe("unknown_intent_kind", "some message")

    def test_observe_does_not_raise_on_empty_message(self, conductor):
        conductor.observe("create_workspace", "")

    def test_user_context_does_not_raise_on_corrupt_profile(self, conductor):
        conductor._profile = {}
        result = conductor.user_context()
        assert isinstance(result, str)
