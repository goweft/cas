"""Tests for execution context wiring into Session and Shell."""

from unittest.mock import MagicMock, patch

import pytest

from cas.protocols import ExecutionContext, SessionScope
from cas.shell import Session, Shell
from cas.memory_store import InMemoryStore


class TestSessionExecutionContext:
    def test_session_has_execution_context_field(self):
        """Session dataclass accepts an execution_context."""
        from datetime import datetime, timezone
        mock_ctx = MagicMock(spec=ExecutionContext)
        session = Session(
            id="test",
            created_at=datetime.now(timezone.utc),
            execution_context=mock_ctx,
        )
        assert session.execution_context is mock_ctx

    def test_session_defaults_to_none(self):
        from datetime import datetime, timezone
        session = Session(id="test", created_at=datetime.now(timezone.utc))
        assert session.execution_context is None


class TestShellExecutionContextWiring:
    def test_shell_creates_default_context(self):
        shell = Shell(store=InMemoryStore())
        assert shell._default_ctx is not None

    def test_shell_accepts_custom_context(self):
        mock_ctx = MagicMock(spec=ExecutionContext)
        shell = Shell(execution_context=mock_ctx)
        assert shell._default_ctx is mock_ctx

    def test_create_session_binds_default_context(self):
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        assert session.execution_context is shell._default_ctx

    def test_create_session_binds_custom_context(self):
        shell = Shell(store=InMemoryStore())
        custom_ctx = MagicMock(spec=ExecutionContext)
        session = shell.create_session(execution_context=custom_ctx)
        assert session.execution_context is custom_ctx

    def test_process_message_preserves_context(self):
        """After processing a message, the session still has its context."""
        shell = Shell(store=InMemoryStore())
        session = shell.create_session()
        shell.process_message(session.id, "hello")
        assert shell.get_session(session.id).execution_context is shell._default_ctx

    def test_different_sessions_can_have_different_contexts(self):
        shell = Shell(store=InMemoryStore())
        ctx_a = MagicMock(spec=ExecutionContext)
        ctx_b = MagicMock(spec=ExecutionContext)
        session_a = shell.create_session(execution_context=ctx_a)
        session_b = shell.create_session(execution_context=ctx_b)
        assert session_a.execution_context is ctx_a
        assert session_b.execution_context is ctx_b
        assert session_a.execution_context is not session_b.execution_context
