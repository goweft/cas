"""CAS protocol definitions — formal interfaces for swappable backends.

These Protocols define the contracts that backends must implement.
SessionStore formalises what CASStore already does.
ExecutionContext is new infrastructure for location-independent file ops.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from datetime import datetime
from pathlib import PurePosixPath
from typing import Any, Protocol, runtime_checkable


# ── Data types shared across protocols ──────────────────────────────


@dataclass(frozen=True)
class FileEntry:
    """A single entry in a directory listing."""
    name: str
    is_dir: bool
    size: int = 0  # bytes, 0 for directories


@dataclass(frozen=True)
class ExecResult:
    """Result of a command execution."""
    returncode: int
    stdout: str
    stderr: str

    @property
    def ok(self) -> bool:
        return self.returncode == 0


@dataclass
class SessionScope:
    """Defines the boundaries of what a session's execution context can do.

    Local sessions default to permissive. Remote sessions default to
    restrictive. The contract layer enforces whichever scope is bound.
    """
    workspace_root: str
    allowed_operations: set[str] = field(
        default_factory=lambda: {"read", "write", "delete", "execute"}
    )
    max_file_size: int = 10 * 1024 * 1024  # 10 MB default
    excluded_patterns: list[str] = field(default_factory=list)

    @classmethod
    def permissive(cls, workspace_root: str) -> SessionScope:
        """Full local access — the default for same-machine sessions."""
        return cls(workspace_root=workspace_root)

    @classmethod
    def restrictive(cls, workspace_root: str) -> SessionScope:
        """Locked-down access — the default for remote sessions."""
        return cls(
            workspace_root=workspace_root,
            allowed_operations={"read"},
            max_file_size=1 * 1024 * 1024,
            excluded_patterns=["*.env", ".ssh/*", "*.key", "*.pem"],
        )


# ── ExecutionContext protocol ───────────────────────────────────────


@runtime_checkable
class ExecutionContext(Protocol):
    """Abstraction over where file/command operations actually happen.

    Implementations: LocalExecutionContext (pathlib), SSHExecutionContext
    (future), ContainerExecutionContext (future).

    All paths are relative to the session's workspace root.
    """

    @property
    def scope(self) -> SessionScope: ...

    def read_file(self, path: str) -> str:
        """Read a text file. Path is relative to workspace root."""
        ...

    def write_file(self, path: str, content: str) -> None:
        """Write a text file. Path is relative to workspace root."""
        ...

    def list_dir(self, path: str = ".") -> list[FileEntry]:
        """List directory contents. Path is relative to workspace root."""
        ...

    def exists(self, path: str) -> bool:
        """Check if a path exists relative to workspace root."""
        ...

    def delete(self, path: str) -> None:
        """Delete a file. Path is relative to workspace root."""
        ...

    def mkdir(self, path: str) -> None:
        """Create a directory (and parents). Relative to workspace root."""
        ...

    def execute(self, command: str, args: list[str] | None = None) -> ExecResult:
        """Run a command within the workspace root."""
        ...


# ── SessionStore protocol ──────────────────────────────────────────


@runtime_checkable
class SessionStore(Protocol):
    """Persistence interface for CAS sessions, messages, and workspaces.

    CASStore (SQLite) already implements this. Defined as a Protocol so
    alternative backends (PostgreSQL, remote, in-memory for tests) can
    be swapped in without changing calling code.
    """

    # Sessions
    def save_session(self, session: Any) -> None: ...
    def load_sessions(self) -> dict[str, Any]: ...

    # Messages
    def save_message(self, session_id: str, message: Any) -> None: ...
    def load_messages(self, session_id: str) -> list[dict]: ...

    # Workspaces
    def save_workspace(self, workspace: Any, session_id: str) -> None: ...
    def update_workspace(self, workspace: Any) -> None: ...
    def close_workspace(self, workspace_id: str, closed_at: datetime) -> None: ...
    def load_workspaces(self, contract_factory: Any) -> dict[str, Any]: ...

    # History
    def load_history(self, workspace_id: str) -> list[dict]: ...
    def get_version(self, workspace_id: str, version: int) -> dict | None: ...
    def apply_version(self, workspace_id: str, version: int) -> bool: ...
    def undo(self, workspace_id: str) -> dict | None: ...

    def close(self) -> None: ...
