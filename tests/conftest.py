"""CAS test configuration.

All fixtures are autouse so every test gets:
  - LLM calls stubbed (no Ollama/Anthropic calls)
  - Conductor persistence disabled (no ~/.cas/profile.json)
  - Auditor mocked (no Heddle dependency)
  - InMemoryStore instead of CASStore (no ~/.cas/cas.db)
  - LocalExecutionContext mocked (no filesystem side effects)
"""
import sys
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

from cas.memory_store import InMemoryStore
from cas.shell import Shell


def _stub_workspace_content(title, user_message, ws_type="document", user_context=""):
    return f"# {title}\n\n{user_message}\n"

def _stub_workspace_edit(title, current, edit_request, ws_type="document", user_context=""):
    return f"{current}\n\n<!-- edit: {edit_request} -->\n"

def _stub_chat_reply(message, history=None, user_context=""):
    return "I can help you create and edit documents."

def _stub_stream_chat(messages, model="", temperature=0.7):
    for token in ["Hello", ".", " How", " can", " I", " help", "?"]:
        yield token


_CONDUCTOR_DEFAULTS = {
    "doc_types": {},
    "ws_types": {},
    "edit_verbs": {},
    "phrases": [],
    "session_count": 0,
    "message_count": 0,
    "workspace_count": 0,
    "edit_count": 0,
    "last_seen": None,
}


@pytest.fixture(autouse=True)
def mock_llm():
    with patch("cas.shell.generate_workspace_content", side_effect=_stub_workspace_content), \
         patch("cas.shell.generate_workspace_edit",    side_effect=_stub_workspace_edit), \
         patch("cas.shell.generate_chat_reply",        side_effect=_stub_chat_reply), \
         patch("cas.api.stream_chat",                  side_effect=_stub_stream_chat):
        yield


@pytest.fixture(autouse=True)
def mock_conductor():
    with patch("cas.conductor.Conductor._save", return_value=None), \
         patch("cas.conductor.Conductor._load", side_effect=lambda: dict(_CONDUCTOR_DEFAULTS)):
        yield


@pytest.fixture(autouse=True)
def mock_auditor():
    mock = MagicMock()
    with patch("cas.shell.get_cas_auditor", return_value=mock), \
         patch("cas.api.get_cas_auditor",   return_value=mock):
        yield mock


@pytest.fixture(autouse=True)
def mock_execution_context():
    """Prevent LocalExecutionContext from touching the filesystem."""
    mock_ctx = MagicMock()
    mock_ctx.scope = MagicMock()
    with patch("cas.shell.LocalExecutionContext", return_value=mock_ctx):
        yield mock_ctx


@pytest.fixture
def store():
    """A clean InMemoryStore — no SQLite, no ~/.cas/cas.db."""
    return InMemoryStore()


@pytest.fixture
def shell(store):
    """A Shell wired to an isolated InMemoryStore.

    This is the canonical way to get a Shell in tests. Every test
    that uses this fixture starts with a clean, empty store — no
    persisted state from previous runs or other tests.
    """
    return Shell(store=store)
