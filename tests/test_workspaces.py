"""Tests for CAS workspace lifecycle.

Verifies create, update, close, list operations, contract enforcement
on workspace mutations, and error handling for invalid states.
"""

import pytest

from cas.contracts import (
    AgentContract,
    ContractRule,
    ContractViolation,
    make_allowed_types_rule,
    make_max_size_rule,
)
from cas.workspaces import (
    Workspace,
    WorkspaceClosed,
    WorkspaceError,
    WorkspaceManager,
    WorkspaceNotFound,
)


# ── Helpers ──────────────────────────────────────────────────────────


def _make_contract(
    agent_name: str = "test-agent",
    preconditions: tuple = (),
    postconditions: tuple = (),
    invariants: tuple = (),
    freeze: bool = True,
) -> AgentContract:
    c = AgentContract(
        agent_name=agent_name,
        preconditions=preconditions,
        postconditions=postconditions,
        invariants=invariants,
    )
    if freeze:
        c.freeze()
    return c


def _permissive_contract(agent_name: str = "test-agent") -> AgentContract:
    """A contract with no rules — everything is allowed."""
    return _make_contract(agent_name=agent_name)


def _blocking_precondition() -> ContractRule:
    return ContractRule(
        name="always_block",
        check=lambda action: False,
        description="Always blocks",
    )


def _blocking_postcondition() -> ContractRule:
    return ContractRule(
        name="always_block_post",
        check=lambda action, result: False,
        description="Always blocks postcondition",
    )


# ── Creation ─────────────────────────────────────────────────────────


class TestWorkspaceCreation:
    def test_create_document_workspace(self):
        mgr = WorkspaceManager()
        ws = mgr.create("My Doc", "# Hello", _permissive_contract())

        assert ws.type == "document"
        assert ws.title == "My Doc"
        assert ws.content == "# Hello"
        assert ws.is_active
        assert ws.closed_at is None
        assert len(ws.id) == 12

    def test_create_assigns_unique_ids(self):
        mgr = WorkspaceManager()
        contract = _permissive_contract()
        ws1 = mgr.create("Doc 1", "", contract)
        ws2 = mgr.create("Doc 2", "", contract)
        assert ws1.id != ws2.id

    def test_create_sets_created_at(self):
        mgr = WorkspaceManager()
        ws = mgr.create("Doc", "", _permissive_contract())
        assert ws.created_at is not None
        assert ws.created_at.tzinfo is not None  # UTC-aware

    def test_create_unknown_type_raises(self):
        mgr = WorkspaceManager()
        with pytest.raises(WorkspaceError, match="Unknown workspace type"):
            mgr.create("Doc", "", _permissive_contract(), workspace_type="spreadsheet")

    def test_create_empty_content(self):
        mgr = WorkspaceManager()
        ws = mgr.create("Empty", "", _permissive_contract())
        assert ws.content == ""

    def test_create_with_markdown_content(self):
        mgr = WorkspaceManager()
        md = "# Title\n\n- item 1\n- item 2\n\n```python\nprint('hello')\n```"
        ws = mgr.create("Markdown Doc", md, _permissive_contract())
        assert ws.content == md


# ── Contract enforcement on creation ─────────────────────────────────


class TestCreationContractEnforcement:
    def test_precondition_blocks_creation(self):
        mgr = WorkspaceManager()
        contract = _make_contract(
            preconditions=(_blocking_precondition(),),
        )
        with pytest.raises(ContractViolation, match="precondition"):
            mgr.create("Doc", "content", contract)

        # Workspace must not exist
        assert mgr.list_all() == []

    def test_postcondition_blocks_creation(self):
        mgr = WorkspaceManager()
        contract = _make_contract(
            postconditions=(_blocking_postcondition(),),
        )
        with pytest.raises(ContractViolation, match="postcondition"):
            mgr.create("Doc", "content", contract)

        assert mgr.list_all() == []

    def test_invariant_blocks_creation(self):
        mgr = WorkspaceManager()
        contract = _make_contract(
            invariants=(ContractRule(name="bad_inv", check=lambda: False),),
        )
        with pytest.raises(ContractViolation, match="invariant"):
            mgr.create("Doc", "content", contract)

        assert mgr.list_all() == []

    def test_allowed_types_rule_permits_document(self):
        mgr = WorkspaceManager()
        contract = _make_contract(
            preconditions=(make_allowed_types_rule(["document"]),),
        )
        ws = mgr.create("Doc", "ok", contract)
        assert ws.type == "document"

    def test_allowed_types_rule_blocks_disallowed(self):
        """Even if 'document' is a valid CAS type, a contract can forbid it."""
        mgr = WorkspaceManager()
        contract = _make_contract(
            preconditions=(make_allowed_types_rule(["terminal"]),),
        )
        with pytest.raises(ContractViolation, match="allowed_workspace_types"):
            mgr.create("Doc", "content", contract)

    def test_max_size_postcondition(self):
        mgr = WorkspaceManager()
        contract = _make_contract(
            postconditions=(make_max_size_rule(1),),  # 1KB limit
        )
        # Small content passes
        ws = mgr.create("Small", "short", contract)
        assert ws.content == "short"

        # Large content blocked
        big = "x" * 2048
        with pytest.raises(ContractViolation, match="max_workspace_size"):
            mgr.create("Big", big, contract)


# ── Get ──────────────────────────────────────────────────────────────


class TestWorkspaceGet:
    def test_get_existing(self):
        mgr = WorkspaceManager()
        ws = mgr.create("Doc", "content", _permissive_contract())
        fetched = mgr.get(ws.id)
        assert fetched is ws

    def test_get_nonexistent_raises(self):
        mgr = WorkspaceManager()
        with pytest.raises(WorkspaceNotFound):
            mgr.get("nonexistent")

    def test_get_closed_workspace_still_works(self):
        mgr = WorkspaceManager()
        ws = mgr.create("Doc", "content", _permissive_contract())
        mgr.close(ws.id)
        fetched = mgr.get(ws.id)
        assert fetched is ws
        assert not fetched.is_active


# ── Update ───────────────────────────────────────────────────────────


class TestWorkspaceUpdate:
    def test_update_content(self):
        mgr = WorkspaceManager()
        ws = mgr.create("Doc", "v1", _permissive_contract())
        updated = mgr.update(ws.id, content="v2")
        assert updated.content == "v2"
        assert updated.title == "Doc"  # unchanged

    def test_update_title(self):
        mgr = WorkspaceManager()
        ws = mgr.create("Old Title", "content", _permissive_contract())
        updated = mgr.update(ws.id, title="New Title")
        assert updated.title == "New Title"
        assert updated.content == "content"  # unchanged

    def test_update_both(self):
        mgr = WorkspaceManager()
        ws = mgr.create("T1", "C1", _permissive_contract())
        updated = mgr.update(ws.id, title="T2", content="C2")
        assert updated.title == "T2"
        assert updated.content == "C2"

    def test_update_no_changes(self):
        mgr = WorkspaceManager()
        ws = mgr.create("Doc", "content", _permissive_contract())
        updated = mgr.update(ws.id)
        assert updated.title == "Doc"
        assert updated.content == "content"

    def test_update_nonexistent_raises(self):
        mgr = WorkspaceManager()
        with pytest.raises(WorkspaceNotFound):
            mgr.update("nonexistent", content="x")

    def test_update_closed_raises(self):
        mgr = WorkspaceManager()
        ws = mgr.create("Doc", "content", _permissive_contract())
        mgr.close(ws.id)
        with pytest.raises(WorkspaceClosed):
            mgr.update(ws.id, content="new")


# ── Contract enforcement on update ───────────────────────────────────


class TestUpdateContractEnforcement:
    def test_precondition_blocks_update(self):
        mgr = WorkspaceManager()
        # Create with permissive, then swap contract to restrictive
        ws = mgr.create("Doc", "v1", _permissive_contract())
        ws.contract = _make_contract(
            preconditions=(_blocking_precondition(),),
        )
        with pytest.raises(ContractViolation, match="precondition"):
            mgr.update(ws.id, content="v2")

        # Content must not have changed
        assert ws.content == "v1"

    def test_postcondition_blocks_update(self):
        mgr = WorkspaceManager()
        contract = _make_contract(
            postconditions=(make_max_size_rule(1),),  # 1KB
        )
        ws = mgr.create("Doc", "small", contract)
        with pytest.raises(ContractViolation, match="max_workspace_size"):
            mgr.update(ws.id, content="x" * 2048)

        # Content must not have changed
        assert ws.content == "small"

    def test_invariant_blocks_update(self):
        mgr = WorkspaceManager()
        ws = mgr.create("Doc", "v1", _permissive_contract())
        ws.contract = _make_contract(
            invariants=(ContractRule(name="broken", check=lambda: False),),
        )
        with pytest.raises(ContractViolation, match="invariant"):
            mgr.update(ws.id, content="v2")

        assert ws.content == "v1"


# ── Close ────────────────────────────────────────────────────────────


class TestWorkspaceClose:
    def test_close_workspace(self):
        mgr = WorkspaceManager()
        ws = mgr.create("Doc", "content", _permissive_contract())
        closed = mgr.close(ws.id)
        assert not closed.is_active
        assert closed.closed_at is not None

    def test_close_idempotent(self):
        mgr = WorkspaceManager()
        ws = mgr.create("Doc", "content", _permissive_contract())
        first_close = mgr.close(ws.id)
        first_time = first_close.closed_at
        second_close = mgr.close(ws.id)
        assert second_close.closed_at == first_time  # same timestamp

    def test_close_nonexistent_raises(self):
        mgr = WorkspaceManager()
        with pytest.raises(WorkspaceNotFound):
            mgr.close("nonexistent")


# ── List ─────────────────────────────────────────────────────────────


class TestWorkspaceList:
    def test_list_active_empty(self):
        mgr = WorkspaceManager()
        assert mgr.list_active() == []

    def test_list_active_returns_only_active(self):
        mgr = WorkspaceManager()
        contract = _permissive_contract()
        ws1 = mgr.create("A", "", contract)
        ws2 = mgr.create("B", "", contract)
        ws3 = mgr.create("C", "", contract)
        mgr.close(ws2.id)

        active = mgr.list_active()
        assert len(active) == 2
        assert active[0].id == ws1.id
        assert active[1].id == ws3.id

    def test_list_all_includes_closed(self):
        mgr = WorkspaceManager()
        contract = _permissive_contract()
        ws1 = mgr.create("A", "", contract)
        ws2 = mgr.create("B", "", contract)
        mgr.close(ws1.id)

        all_ws = mgr.list_all()
        assert len(all_ws) == 2

    def test_list_active_ordered_by_creation(self):
        mgr = WorkspaceManager()
        contract = _permissive_contract()
        ws1 = mgr.create("First", "", contract)
        ws2 = mgr.create("Second", "", contract)
        ws3 = mgr.create("Third", "", contract)

        active = mgr.list_active()
        assert [ws.title for ws in active] == ["First", "Second", "Third"]


# ── Serialization ────────────────────────────────────────────────────


class TestWorkspaceSerialization:
    def test_to_dict_active(self):
        mgr = WorkspaceManager()
        ws = mgr.create("Doc", "# Hello", _permissive_contract())
        d = ws.to_dict()

        assert d["id"] == ws.id
        assert d["type"] == "document"
        assert d["title"] == "Doc"
        assert d["content"] == "# Hello"
        assert d["is_active"] is True
        assert d["closed_at"] is None
        assert "created_at" in d

    def test_to_dict_closed(self):
        mgr = WorkspaceManager()
        ws = mgr.create("Doc", "content", _permissive_contract())
        mgr.close(ws.id)
        d = ws.to_dict()

        assert d["is_active"] is False
        assert d["closed_at"] is not None


# ── Edge cases ───────────────────────────────────────────────────────


class TestEdgeCases:
    def test_workspace_bound_to_specific_contract(self):
        """Each workspace carries its own contract, not a shared global."""
        mgr = WorkspaceManager()
        c1 = _permissive_contract(agent_name="agent-a")
        c2 = _permissive_contract(agent_name="agent-b")
        ws1 = mgr.create("Doc A", "", c1)
        ws2 = mgr.create("Doc B", "", c2)

        assert ws1.contract.agent_name == "agent-a"
        assert ws2.contract.agent_name == "agent-b"

    def test_multiple_managers_independent(self):
        mgr1 = WorkspaceManager()
        mgr2 = WorkspaceManager()
        ws = mgr1.create("Doc", "", _permissive_contract())

        with pytest.raises(WorkspaceNotFound):
            mgr2.get(ws.id)
