"""CAS test configuration."""
import sys
from pathlib import Path
from unittest.mock import MagicMock, patch

import pytest

heddle_src = Path(__file__).parent.parent.parent / "loom" / "src"
if heddle_src.exists() and str(heddle_src) not in sys.path:
    sys.path.insert(0, str(heddle_src))

from cas.memory_store import InMemoryStore


def _stub_workspace_content(title, user_message, ws_type="document", user_context=""):
    return f"# {title}\n\n{user_message}\n"

def _stub_workspace_edit(title, current, edit_request, ws_type="document", user_context=""):
    return f"{current}\n\n<!-- edit: {edit_request} -->\n"

def _stub_chat_reply(message, history=None, user_context=""):
    return "I can help you create and edit documents."

def _stub_stream_chat(messages, model="", temperature=0.7):
    for token in ["Hello", ".", " How", " can", " I", " help", "?"]:
        yield token


# Fresh conductor defaults — kept in sync with Conductor._defaults()
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
def in_memory_store():
    """Replace CASStore with InMemoryStore so tests exercise real store
    logic without touching ~/.cas/cas.db.

    Unlike the previous MagicMock approach, this means session save/load,
    message persistence, and workspace history all work for real inside
    tests — the only thing bypassed is SQLite.
    """
    store = InMemoryStore()
    with patch("cas.shell.CASStore", return_value=store):
        yield store


@pytest.fixture(autouse=True)
def mock_execution_context():
    """Replace LocalExecutionContext so tests don't create ~/.cas/workspaces/."""
    mock_ctx = MagicMock()
    mock_ctx.scope = MagicMock()
    with patch("cas.shell.LocalExecutionContext", return_value=mock_ctx):
        yield mock_ctx
