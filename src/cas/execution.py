"""CAS execution context — where file and command operations happen.

The ExecutionContext abstraction decouples CAS from the assumption that
workspaces live on the same machine as the server. LocalExecutionContext
wraps pathlib for same-machine use. Future implementations (SSH, container)
swap in via configuration, not code changes.

All paths are validated against the session's scope before any I/O.
Path traversal outside the workspace root raises ContractViolation.
"""

from __future__ import annotations

import fnmatch
import logging
import subprocess
from pathlib import Path
from typing import Any

from cas.contracts import ContractViolation
from cas.protocols import ExecResult, ExecutionContext, FileEntry, SessionScope

logger = logging.getLogger(__name__)

_DEFAULT_ROOT = Path.home() / ".cas" / "workspaces"


class ExecutionContextError(Exception):
    """Base error for execution context operations."""
    pass


class LocalExecutionContext:
    """Filesystem operations on the local machine via pathlib.

    This is the current (and default) execution context. All workspace
    file operations go through here rather than calling pathlib directly,
    so the calling code doesn't need to change when the backend switches
    to SSH or containers.

    Scope enforcement:
    - All paths are resolved relative to workspace_root
    - Traversal outside the root raises ContractViolation
    - Operations not in allowed_operations raise ContractViolation
    - Files exceeding max_file_size are rejected on write
    - Paths matching excluded_patterns are blocked
    """

    def __init__(self, scope: SessionScope | None = None) -> None:
        if scope is None:
            scope = SessionScope.permissive(str(_DEFAULT_ROOT))
        self._scope = scope
        self._root = Path(scope.workspace_root).resolve()
        self._root.mkdir(parents=True, exist_ok=True)

    @property
    def scope(self) -> SessionScope:
        return self._scope

    # ── Path validation ──────────────────────────────────────────

    def _resolve(self, path: str) -> Path:
        """Resolve a relative path against the workspace root.

        Raises ContractViolation if the resolved path escapes the root
        or matches an excluded pattern.
        """
        resolved = (self._root / path).resolve()

        # Traversal check
        try:
            resolved.relative_to(self._root)
        except ValueError:
            raise ContractViolation(
                "execution_context", "precondition", "path_scope",
                f"Path '{path}' resolves outside workspace root",
            )

        # Excluded pattern check
        rel_str = str(resolved.relative_to(self._root))
        for pattern in self._scope.excluded_patterns:
            if fnmatch.fnmatch(rel_str, pattern) or fnmatch.fnmatch(resolved.name, pattern):
                raise ContractViolation(
                    "execution_context", "precondition", "excluded_pattern",
                    f"Path '{path}' matches excluded pattern '{pattern}'",
                )

        return resolved

    def _require_op(self, op: str) -> None:
        """Raise ContractViolation if the operation is not allowed."""
        if op not in self._scope.allowed_operations:
            raise ContractViolation(
                "execution_context", "precondition", "allowed_operations",
                f"Operation '{op}' not permitted in this scope",
            )

    # ── ExecutionContext implementation ───────────────────────────

    def read_file(self, path: str) -> str:
        self._require_op("read")
        resolved = self._resolve(path)
        if not resolved.is_file():
            raise ExecutionContextError(f"File not found: {path}")
        return resolved.read_text(encoding="utf-8")

    def write_file(self, path: str, content: str) -> None:
        self._require_op("write")
        resolved = self._resolve(path)

        # Size check
        size = len(content.encode("utf-8"))
        if size > self._scope.max_file_size:
            raise ContractViolation(
                "execution_context", "precondition", "max_file_size",
                f"Content size {size} exceeds limit {self._scope.max_file_size}",
            )

        # Ensure parent directory exists
        resolved.parent.mkdir(parents=True, exist_ok=True)
        resolved.write_text(content, encoding="utf-8")
        logger.debug("Wrote %d bytes to %s", size, path)

    def list_dir(self, path: str = ".") -> list[FileEntry]:
        self._require_op("read")
        resolved = self._resolve(path)
        if not resolved.is_dir():
            raise ExecutionContextError(f"Not a directory: {path}")
        entries = []
        for child in sorted(resolved.iterdir()):
            # Skip hidden files
            if child.name.startswith("."):
                continue
            entries.append(FileEntry(
                name=child.name,
                is_dir=child.is_dir(),
                size=child.stat().st_size if child.is_file() else 0,
            ))
        return entries

    def exists(self, path: str) -> bool:
        self._require_op("read")
        try:
            resolved = self._resolve(path)
            return resolved.exists()
        except ContractViolation:
            return False

    def delete(self, path: str) -> None:
        self._require_op("delete")
        resolved = self._resolve(path)
        if not resolved.exists():
            raise ExecutionContextError(f"Path not found: {path}")
        if resolved.is_dir():
            # Only delete empty directories
            if any(resolved.iterdir()):
                raise ExecutionContextError(f"Directory not empty: {path}")
            resolved.rmdir()
        else:
            resolved.unlink()
        logger.debug("Deleted %s", path)

    def mkdir(self, path: str) -> None:
        self._require_op("write")
        resolved = self._resolve(path)
        resolved.mkdir(parents=True, exist_ok=True)

    def execute(self, command: str, args: list[str] | None = None) -> ExecResult:
        self._require_op("execute")
        cmd = [command] + (args or [])
        try:
            result = subprocess.run(
                cmd,
                cwd=str(self._root),
                capture_output=True,
                text=True,
                timeout=30,
            )
            return ExecResult(
                returncode=result.returncode,
                stdout=result.stdout,
                stderr=result.stderr,
            )
        except subprocess.TimeoutExpired:
            return ExecResult(returncode=-1, stdout="", stderr="Command timed out (30s)")
        except FileNotFoundError:
            return ExecResult(returncode=-1, stdout="", stderr=f"Command not found: {command}")
        except Exception as exc:
            return ExecResult(returncode=-1, stdout="", stderr=str(exc))

    def __repr__(self) -> str:
        return f"LocalExecutionContext(root={self._root})"
