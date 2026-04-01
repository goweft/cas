"""Tests for CAS execution context — scope enforcement and file operations."""

import pytest

from cas.contracts import ContractViolation
from cas.execution import ExecutionContextError, LocalExecutionContext
from cas.protocols import ExecResult, FileEntry, SessionScope


@pytest.fixture
def workspace_root(tmp_path):
    """Create a temporary workspace root."""
    root = tmp_path / "workspaces"
    root.mkdir()
    return root


@pytest.fixture
def ctx(workspace_root):
    """LocalExecutionContext with permissive scope."""
    scope = SessionScope.permissive(str(workspace_root))
    return LocalExecutionContext(scope)


@pytest.fixture
def restricted_ctx(workspace_root):
    """LocalExecutionContext with restrictive scope (read-only)."""
    scope = SessionScope.restrictive(str(workspace_root))
    return LocalExecutionContext(scope)


# ── Basic file operations ────────────────────────────────────────────


class TestReadWrite:
    def test_write_and_read(self, ctx):
        ctx.write_file("hello.txt", "world")
        assert ctx.read_file("hello.txt") == "world"

    def test_write_creates_parent_dirs(self, ctx):
        ctx.write_file("sub/deep/file.txt", "nested")
        assert ctx.read_file("sub/deep/file.txt") == "nested"

    def test_read_nonexistent_raises(self, ctx):
        with pytest.raises(ExecutionContextError, match="File not found"):
            ctx.read_file("nope.txt")

    def test_overwrite(self, ctx):
        ctx.write_file("f.txt", "v1")
        ctx.write_file("f.txt", "v2")
        assert ctx.read_file("f.txt") == "v2"

    def test_write_empty_file(self, ctx):
        ctx.write_file("empty.txt", "")
        assert ctx.read_file("empty.txt") == ""


class TestListDir:
    def test_list_empty(self, ctx):
        assert ctx.list_dir() == []

    def test_list_files(self, ctx):
        ctx.write_file("a.txt", "aaa")
        ctx.write_file("b.txt", "bbb")
        entries = ctx.list_dir()
        names = [e.name for e in entries]
        assert names == ["a.txt", "b.txt"]
        assert all(not e.is_dir for e in entries)

    def test_list_with_subdirs(self, ctx):
        ctx.mkdir("subdir")
        ctx.write_file("file.txt", "x")
        entries = ctx.list_dir()
        names = {e.name: e.is_dir for e in entries}
        assert names == {"file.txt": False, "subdir": True}

    def test_list_skips_hidden(self, ctx, workspace_root):
        (workspace_root / ".hidden").write_text("secret")
        ctx.write_file("visible.txt", "ok")
        entries = ctx.list_dir()
        assert len(entries) == 1
        assert entries[0].name == "visible.txt"

    def test_list_nonexistent_raises(self, ctx):
        with pytest.raises(ExecutionContextError, match="Not a directory"):
            ctx.list_dir("nope")


class TestExists:
    def test_exists_true(self, ctx):
        ctx.write_file("here.txt", "yes")
        assert ctx.exists("here.txt") is True

    def test_exists_false(self, ctx):
        assert ctx.exists("nope.txt") is False

    def test_exists_dir(self, ctx):
        ctx.mkdir("mydir")
        assert ctx.exists("mydir") is True


class TestDelete:
    def test_delete_file(self, ctx):
        ctx.write_file("gone.txt", "bye")
        ctx.delete("gone.txt")
        assert ctx.exists("gone.txt") is False

    def test_delete_empty_dir(self, ctx):
        ctx.mkdir("emptydir")
        ctx.delete("emptydir")
        assert ctx.exists("emptydir") is False

    def test_delete_nonempty_dir_raises(self, ctx):
        ctx.write_file("dir/file.txt", "x")
        with pytest.raises(ExecutionContextError, match="not empty"):
            ctx.delete("dir")

    def test_delete_nonexistent_raises(self, ctx):
        with pytest.raises(ExecutionContextError, match="not found"):
            ctx.delete("nope.txt")


class TestMkdir:
    def test_mkdir(self, ctx):
        ctx.mkdir("newdir")
        assert ctx.exists("newdir") is True

    def test_mkdir_nested(self, ctx):
        ctx.mkdir("a/b/c")
        assert ctx.exists("a/b/c") is True

    def test_mkdir_idempotent(self, ctx):
        ctx.mkdir("mydir")
        ctx.mkdir("mydir")  # no error
        assert ctx.exists("mydir") is True


class TestExecute:
    def test_echo(self, ctx):
        result = ctx.execute("echo", ["hello"])
        assert result.ok
        assert result.stdout.strip() == "hello"

    def test_command_not_found(self, ctx):
        result = ctx.execute("nonexistent_command_xyz")
        assert not result.ok
        assert "not found" in result.stderr.lower()

    def test_exit_code(self, ctx):
        result = ctx.execute("false")
        assert not result.ok
        assert result.returncode != 0


# ── Scope enforcement ─────────────────────────────────────────────


class TestPathTraversal:
    def test_traversal_blocked(self, ctx):
        with pytest.raises(ContractViolation, match="outside workspace root"):
            ctx.read_file("../../etc/passwd")

    def test_traversal_write_blocked(self, ctx):
        with pytest.raises(ContractViolation, match="outside workspace root"):
            ctx.write_file("../escape.txt", "nope")

    def test_absolute_path_outside_root_blocked(self, ctx):
        with pytest.raises(ContractViolation, match="outside workspace root"):
            ctx.read_file("/etc/passwd")


class TestExcludedPatterns:
    def test_env_file_blocked(self, workspace_root):
        scope = SessionScope(
            workspace_root=str(workspace_root),
            excluded_patterns=["*.env"],
        )
        ctx = LocalExecutionContext(scope)
        with pytest.raises(ContractViolation, match="excluded pattern"):
            ctx.write_file(".env", "SECRET=bad")

    def test_ssh_dir_blocked(self, workspace_root):
        scope = SessionScope(
            workspace_root=str(workspace_root),
            excluded_patterns=[".ssh/*"],
        )
        ctx = LocalExecutionContext(scope)
        with pytest.raises(ContractViolation, match="excluded pattern"):
            ctx.write_file(".ssh/id_rsa", "private key")

    def test_non_excluded_allowed(self, workspace_root):
        scope = SessionScope(
            workspace_root=str(workspace_root),
            excluded_patterns=["*.env"],
        )
        ctx = LocalExecutionContext(scope)
        ctx.write_file("config.yaml", "ok: true")
        assert ctx.read_file("config.yaml") == "ok: true"


class TestAllowedOperations:
    def test_read_only_blocks_write(self, restricted_ctx):
        with pytest.raises(ContractViolation, match="not permitted"):
            restricted_ctx.write_file("nope.txt", "blocked")

    def test_read_only_blocks_delete(self, restricted_ctx):
        with pytest.raises(ContractViolation, match="not permitted"):
            restricted_ctx.delete("nope.txt")

    def test_read_only_blocks_execute(self, restricted_ctx):
        with pytest.raises(ContractViolation, match="not permitted"):
            restricted_ctx.execute("echo", ["hi"])

    def test_read_only_allows_read(self, restricted_ctx, workspace_root):
        (workspace_root / "readable.txt").write_text("ok")
        assert restricted_ctx.read_file("readable.txt") == "ok"

    def test_read_only_allows_exists(self, restricted_ctx, workspace_root):
        (workspace_root / "check.txt").write_text("ok")
        assert restricted_ctx.exists("check.txt") is True

    def test_read_only_allows_list(self, restricted_ctx, workspace_root):
        (workspace_root / "file.txt").write_text("ok")
        entries = restricted_ctx.list_dir()
        assert len(entries) == 1


class TestMaxFileSize:
    def test_oversized_write_blocked(self, workspace_root):
        scope = SessionScope(
            workspace_root=str(workspace_root),
            max_file_size=100,  # 100 bytes
        )
        ctx = LocalExecutionContext(scope)
        with pytest.raises(ContractViolation, match="exceeds limit"):
            ctx.write_file("big.txt", "x" * 200)

    def test_within_limit_allowed(self, workspace_root):
        scope = SessionScope(
            workspace_root=str(workspace_root),
            max_file_size=100,
        )
        ctx = LocalExecutionContext(scope)
        ctx.write_file("small.txt", "x" * 50)
        assert ctx.read_file("small.txt") == "x" * 50


# ── SessionScope factories ───────────────────────────────────────


class TestSessionScope:
    def test_permissive_defaults(self):
        scope = SessionScope.permissive("/tmp/ws")
        assert "read" in scope.allowed_operations
        assert "write" in scope.allowed_operations
        assert "execute" in scope.allowed_operations
        assert "delete" in scope.allowed_operations
        assert scope.excluded_patterns == []

    def test_restrictive_defaults(self):
        scope = SessionScope.restrictive("/tmp/ws")
        assert scope.allowed_operations == {"read"}
        assert "*.env" in scope.excluded_patterns
        assert ".ssh/*" in scope.excluded_patterns
        assert scope.max_file_size == 1 * 1024 * 1024


# ── Protocol conformance ─────────────────────────────────────────


class TestProtocolConformance:
    def test_local_context_is_execution_context(self, ctx):
        from cas.protocols import ExecutionContext
        assert isinstance(ctx, ExecutionContext)
