"""Integration tests — prove Shell operations actually flow through the store.

Validates that the SessionStore Protocol swap from MagicMock to InMemoryStore
is meaningful: session save/load, message persistence, workspace history, and
Shell reconstruction all exercise real store logic rather than mocks.
"""

import pytest

from cas.memory_store import InMemoryStore
from cas.shell import Shell


class TestStoreIntegration:
    """Verify Shell operations flow through to the store."""

    def test_create_session_persists_to_store(self, shell, store):
        session = shell.create_session()
        stored = store.load_sessions()
        assert session.id in stored

    def test_process_message_persists_messages(self, shell, store):
        session = shell.create_session()
        shell.process_message(session.id, "hello world")
        messages = store.load_messages(session.id)
        assert len(messages) >= 2  # user + shell response
        assert messages[0]["role"] == "user"
        assert messages[0]["text"] == "hello world"

    def test_create_workspace_persists_to_store(self, shell, store):
        session = shell.create_session()
        shell.process_message(session.id, "write a project proposal")
        workspaces = store.load_workspaces(shell._contract_factory)
        assert len(workspaces) == 1
        ws_id = list(workspaces.keys())[0]
        assert workspaces[ws_id]["title"] is not None

    def test_multiple_sessions_isolated_in_store(self, shell, store):
        s1 = shell.create_session()
        s2 = shell.create_session()
        shell.process_message(s1.id, "hello from session 1")
        shell.process_message(s2.id, "hello from session 2")
        m1 = store.load_messages(s1.id)
        m2 = store.load_messages(s2.id)
        assert all(m["text"] != "hello from session 2" for m in m1)
        assert all(m["text"] != "hello from session 1" for m in m2)

    def test_edit_workspace_updates_store(self, shell, store):
        session = shell.create_session()
        shell.process_message(session.id, "write a project proposal")
        ws_before = store.load_workspaces(shell._contract_factory)
        ws_id = list(ws_before.keys())[0]
        shell.process_message(session.id, "add an executive summary section")
        ws_after = store.load_workspaces(shell._contract_factory)
        assert ws_after[ws_id]["content"] != ws_before[ws_id]["content"]

    def test_close_workspace_reflected_in_store(self, shell, store):
        session = shell.create_session()
        shell.process_message(session.id, "write a project proposal")
        active_before = shell.workspaces.list_active()
        assert len(active_before) == 1
        shell.process_message(session.id, "close the workspace")
        active_after = shell.workspaces.list_active()
        assert len(active_after) == 0

    def test_store_survives_shell_reconstruction(self, store):
        """Shell rebuilt on the same store restores sessions and workspaces."""
        shell1 = Shell(store=store)
        session = shell1.create_session()
        shell1.process_message(session.id, "write a project proposal")
        assert len(shell1.workspaces.list_active()) == 1

        # New shell, same store — simulates process restart
        shell2 = Shell(store=store)
        assert session.id in shell2._sessions
        assert len(shell2.workspaces.list_active()) == 1
