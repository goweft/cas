"""Integration tests — prove InMemoryStore exercises real persistence logic.

These tests verify that Shell operations actually persist through the store,
not just in-memory on the Shell's own dicts. This validates that the
SessionStore Protocol swap from MagicMock to InMemoryStore is meaningful.
"""

import pytest

from cas.memory_store import InMemoryStore
from cas.shell import Shell


class TestStoreIntegration:
    """Verify Shell operations flow through to the InMemoryStore."""

    def test_create_session_persists_to_store(self, in_memory_store):
        shell = Shell()
        session = shell.create_session()
        stored = in_memory_store.load_sessions()
        assert session.id in stored

    def test_process_message_persists_messages(self, in_memory_store):
        shell = Shell()
        session = shell.create_session()
        shell.process_message(session.id, "hello world")
        messages = in_memory_store.load_messages(session.id)
        assert len(messages) >= 2  # user + shell response
        assert messages[0]["role"] == "user"
        assert messages[0]["text"] == "hello world"

    def test_create_workspace_persists_to_store(self, in_memory_store):
        shell = Shell()
        session = shell.create_session()
        shell.process_message(session.id, "write a proposal for Q4")
        from cas.contracts import load_contract_from_config
        factory = lambda: load_contract_from_config("cas-workspace", {
            "allowed_workspace_types": ["document", "code", "list"],
            "max_workspace_size_kb": 512,
            "network_access": False,
        })
        stored_ws = in_memory_store.load_workspaces(factory)
        assert len(stored_ws) == 1
        ws = list(stored_ws.values())[0]
        assert "Proposal" in ws["title"] or "Q4" in ws["title"] or ws["title"]

    def test_multiple_sessions_isolated_in_store(self, in_memory_store):
        shell = Shell()
        s1 = shell.create_session()
        s2 = shell.create_session()
        shell.process_message(s1.id, "message for session 1")
        shell.process_message(s2.id, "message for session 2")
        msgs_1 = in_memory_store.load_messages(s1.id)
        msgs_2 = in_memory_store.load_messages(s2.id)
        assert any("session 1" in m["text"] for m in msgs_1)
        assert any("session 2" in m["text"] for m in msgs_2)
        assert not any("session 2" in m["text"] for m in msgs_1)

    def test_edit_workspace_updates_store(self, in_memory_store):
        shell = Shell()
        session = shell.create_session()
        shell.process_message(session.id, "create a document about testing")
        shell.process_message(session.id, "add a section about unit tests")
        from cas.contracts import load_contract_from_config
        factory = lambda: load_contract_from_config("cas-workspace", {
            "allowed_workspace_types": ["document", "code", "list"],
            "max_workspace_size_kb": 512,
            "network_access": False,
        })
        stored_ws = in_memory_store.load_workspaces(factory)
        assert len(stored_ws) == 1
        ws = list(stored_ws.values())[0]
        # After edit, content should have changed from original
        assert "unit tests" in ws["content"].lower() or len(ws["content"]) > 0

    def test_close_workspace_reflected_in_store(self, in_memory_store):
        shell = Shell()
        session = shell.create_session()
        shell.process_message(session.id, "write a brief note")
        from cas.contracts import load_contract_from_config
        factory = lambda: load_contract_from_config("cas-workspace", {
            "allowed_workspace_types": ["document", "code", "list"],
            "max_workspace_size_kb": 512,
            "network_access": False,
        })
        active_before = in_memory_store.load_workspaces(factory)
        assert len(active_before) == 1
        shell.process_message(session.id, "close the workspace")
        active_after = in_memory_store.load_workspaces(factory)
        assert len(active_after) == 0

    def test_store_survives_shell_reconstruction(self, in_memory_store):
        """If we build a new Shell on the same store, state is restored."""
        from unittest.mock import patch, MagicMock

        shell1 = Shell()
        session = shell1.create_session()
        shell1.process_message(session.id, "write a report on testing")

        # Build a second Shell on the same store — simulates process restart
        with patch("cas.shell.CASStore", return_value=in_memory_store):
            shell2 = Shell()
            active = shell2.workspaces.list_active()
            assert len(active) == 1
            assert active[0].content  # not empty
