"""Tests for CAS deterministic contract enforcement.

Verifies that AgentContract blocks violations deterministically —
preconditions, postconditions, invariants, immutability, and fail-closed
behavior on exceptions.
"""

import pytest

from cas.contracts import (
    AgentContract,
    ContractRule,
    ContractViolation,
    load_contract_from_config,
    make_allowed_types_rule,
    make_cannot_modify_rule,
    make_filesystem_scope_rule,
    make_max_size_rule,
    make_network_access_rule,
)


# ── Helpers ──────────────────────────────────────────────────────────


def _always_pass(action: dict) -> bool:
    return True


def _always_fail(action: dict) -> bool:
    return False


def _raise_error(action: dict) -> bool:
    raise RuntimeError("unexpected error in check")


def _post_pass(action: dict, result: dict) -> bool:
    return True


def _post_fail(action: dict, result: dict) -> bool:
    return False


def _inv_pass() -> bool:
    return True


def _inv_fail() -> bool:
    return False


# ── Basic precondition enforcement ───────────────────────────────────


class TestPreconditions:
    def test_passes_when_all_satisfied(self, tmp_path):
        contract = AgentContract(
            agent_name="test",
            preconditions=(
                ContractRule("r1", _always_pass),
                ContractRule("r2", _always_pass),
            ),
        )
        # Should not raise
        contract.check_preconditions({"action": "write"})

    def test_fails_on_first_violation(self, tmp_path):
        contract = AgentContract(
            agent_name="test",
            preconditions=(
                ContractRule("pass", _always_pass),
                ContractRule("block", _always_fail, "must not happen"),
            ),
        )
        with pytest.raises(ContractViolation) as exc_info:
            contract.check_preconditions({"action": "write"})
        assert exc_info.value.phase == "precondition"
        assert exc_info.value.rule == "block"
        assert "test" in exc_info.value.agent_name

    def test_empty_preconditions_pass(self):
        contract = AgentContract(agent_name="test")
        contract.check_preconditions({"anything": True})

    def test_exception_in_check_is_fail_closed(self):
        contract = AgentContract(
            agent_name="test",
            preconditions=(ContractRule("boom", _raise_error),),
        )
        with pytest.raises(ContractViolation) as exc_info:
            contract.check_preconditions({"action": "write"})
        assert "fail-closed" in exc_info.value.detail
        assert exc_info.value.phase == "precondition"


# ── Postcondition enforcement ────────────────────────────────────────


class TestPostconditions:
    def test_passes_when_all_satisfied(self):
        contract = AgentContract(
            agent_name="test",
            postconditions=(ContractRule("ok", _post_pass),),
        )
        contract.check_postconditions({"action": "gen"}, {"content": "hi"})

    def test_fails_on_violation(self):
        contract = AgentContract(
            agent_name="test",
            postconditions=(ContractRule("nope", _post_fail, "bad output"),),
        )
        with pytest.raises(ContractViolation) as exc_info:
            contract.check_postconditions({"action": "gen"}, {"content": "x"})
        assert exc_info.value.phase == "postcondition"

    def test_exception_in_post_check_is_fail_closed(self):
        def bad_post(action, result):
            raise ValueError("oops")

        contract = AgentContract(
            agent_name="test",
            postconditions=(ContractRule("bad", bad_post),),
        )
        with pytest.raises(ContractViolation) as exc_info:
            contract.check_postconditions({}, {})
        assert "fail-closed" in exc_info.value.detail


# ── Invariant enforcement ────────────────────────────────────────────


class TestInvariants:
    def test_passes_when_all_hold(self):
        contract = AgentContract(
            agent_name="test",
            invariants=(ContractRule("ok", _inv_pass),),
        )
        contract.check_invariants()

    def test_fails_on_violation(self):
        contract = AgentContract(
            agent_name="test",
            invariants=(ContractRule("broken", _inv_fail, "invariant broken"),),
        )
        with pytest.raises(ContractViolation) as exc_info:
            contract.check_invariants()
        assert exc_info.value.phase == "invariant"
        assert exc_info.value.rule == "broken"

    def test_exception_in_invariant_is_fail_closed(self):
        def bad_inv():
            raise RuntimeError("disk error")

        contract = AgentContract(
            agent_name="test",
            invariants=(ContractRule("bad", bad_inv),),
        )
        with pytest.raises(ContractViolation):
            contract.check_invariants()


# ── Contract immutability ────────────────────────────────────────────


class TestImmutability:
    def test_frozen_contract_rejects_mutation(self):
        contract = AgentContract(agent_name="test")
        contract.freeze()

        with pytest.raises(ContractViolation) as exc_info:
            contract.preconditions = ()
        assert exc_info.value.phase == "invariant"
        assert "immutability" in exc_info.value.rule

    def test_frozen_contract_rejects_new_attributes(self):
        contract = AgentContract(agent_name="test")
        contract.freeze()

        with pytest.raises(ContractViolation):
            contract.agent_name = "hacked"

    def test_unfrozen_contract_allows_mutation(self):
        contract = AgentContract(agent_name="test")
        contract.preconditions = (ContractRule("x", _always_pass),)
        assert len(contract.preconditions) == 1


# ── Built-in rule: filesystem_scope ──────────────────────────────────


class TestFilesystemScope:
    def test_allows_path_within_scope(self, tmp_path):
        scope_dir = tmp_path / "workspace"
        scope_dir.mkdir()
        rule = make_filesystem_scope_rule(str(scope_dir))
        assert rule({"path": str(scope_dir / "doc.html")})

    def test_blocks_path_outside_scope(self, tmp_path):
        scope_dir = tmp_path / "workspace"
        scope_dir.mkdir()
        rule = make_filesystem_scope_rule(str(scope_dir))
        assert not rule({"path": "/etc/passwd"})

    def test_blocks_traversal_attack(self, tmp_path):
        scope_dir = tmp_path / "workspace"
        scope_dir.mkdir()
        rule = make_filesystem_scope_rule(str(scope_dir))
        # ../.. traversal should be caught after resolve()
        assert not rule({"path": str(scope_dir / ".." / ".." / "etc" / "passwd")})

    def test_no_path_in_action_passes(self):
        rule = make_filesystem_scope_rule("/tmp/safe")
        assert rule({"action": "chat"})


# ── Built-in rule: network_access ────────────────────────────────────


class TestNetworkAccess:
    def test_denied_blocks_url(self):
        rule = make_network_access_rule(False)
        assert not rule({"url": "http://evil.com"})

    def test_denied_blocks_network_flag(self):
        rule = make_network_access_rule(False)
        assert not rule({"network": True})

    def test_denied_allows_no_network(self):
        rule = make_network_access_rule(False)
        assert rule({"action": "local_write"})

    def test_allowed_passes_everything(self):
        rule = make_network_access_rule(True)
        assert rule({"url": "http://example.com"})


# ── Built-in rule: max_workspace_size ────────────────────────────────


class TestMaxSize:
    def test_under_limit_passes(self):
        rule = make_max_size_rule(1)  # 1KB
        assert rule({}, {"content": "x" * 500})

    def test_over_limit_fails(self):
        rule = make_max_size_rule(1)  # 1KB
        assert not rule({}, {"content": "x" * 2000})

    def test_empty_content_passes(self):
        rule = make_max_size_rule(1)
        assert rule({}, {"content": ""})

    def test_missing_content_passes(self):
        rule = make_max_size_rule(1)
        assert rule({}, {})


# ── Built-in rule: allowed_workspace_types ───────────────────────────


class TestAllowedTypes:
    def test_allowed_type_passes(self):
        rule = make_allowed_types_rule(["document"])
        assert rule({"workspace_type": "document"})

    def test_disallowed_type_fails(self):
        rule = make_allowed_types_rule(["document"])
        assert not rule({"workspace_type": "terminal"})

    def test_no_type_in_action_passes(self):
        rule = make_allowed_types_rule(["document"])
        assert rule({"action": "chat"})


# ── Built-in rule: cannot_modify ─────────────────────────────────────


class TestCannotModify:
    def test_forbidden_pattern_blocks(self):
        rule = make_cannot_modify_rule(["/home/*", "heddle/security/*"])
        assert not rule({"path": "/home/user"})
        assert not rule({"path": "heddle/security/trust.py"})

    def test_allowed_path_passes(self):
        rule = make_cannot_modify_rule(["/home/*"])
        assert rule({"path": "/tmp/workspace/doc.html"})

    def test_no_path_passes(self):
        rule = make_cannot_modify_rule(["/home/*"])
        assert rule({"action": "chat"})

    def test_contracts_file_protected(self):
        rule = make_cannot_modify_rule(["heddle/cas/contracts.py"])
        assert not rule({"path": "heddle/cas/contracts.py"})


# ── load_contract_from_config ────────────────────────────────────────


class TestLoadFromConfig:
    def test_loads_full_contract(self, tmp_path):
        config = {
            "filesystem_scope": str(tmp_path),
            "network_access": False,
            "max_workspace_size_kb": 512,
            "allowed_workspace_types": ["document"],
            "cannot_modify": ["/home/*", "heddle/security/*"],
            "on_violation": "quarantine",
        }
        contract = load_contract_from_config("ws-gen", config)

        assert contract.agent_name == "ws-gen"
        assert len(contract.preconditions) == 4  # scope, network, types, cannot_modify
        assert len(contract.postconditions) == 1  # max_size

    def test_contract_is_frozen(self):
        contract = load_contract_from_config("test", {"network_access": False})
        with pytest.raises(ContractViolation):
            contract.preconditions = ()

    def test_empty_config_produces_empty_contract(self):
        contract = load_contract_from_config("test", {})
        assert len(contract.preconditions) == 0
        assert len(contract.postconditions) == 0

    def test_loaded_contract_enforces_filesystem_scope(self, tmp_path):
        scope = tmp_path / "ws"
        scope.mkdir()
        contract = load_contract_from_config("test", {
            "filesystem_scope": str(scope),
        })
        # Within scope — passes
        contract.check_preconditions({"path": str(scope / "f.txt")})

        # Outside scope — blocked
        with pytest.raises(ContractViolation):
            contract.check_preconditions({"path": "/etc/shadow"})

    def test_loaded_contract_enforces_network_block(self):
        contract = load_contract_from_config("test", {"network_access": False})
        with pytest.raises(ContractViolation):
            contract.check_preconditions({"url": "http://exfil.bad"})

    def test_loaded_contract_enforces_max_size(self):
        contract = load_contract_from_config("test", {"max_workspace_size_kb": 1})
        with pytest.raises(ContractViolation):
            contract.check_postconditions({}, {"content": "x" * 2000})

    def test_loaded_contract_enforces_workspace_type(self):
        contract = load_contract_from_config("test", {
            "allowed_workspace_types": ["document"],
        })
        with pytest.raises(ContractViolation):
            contract.check_preconditions({"workspace_type": "terminal"})

    def test_loaded_contract_enforces_cannot_modify(self):
        contract = load_contract_from_config("test", {
            "cannot_modify": ["heddle/cas/contracts.py"],
        })
        with pytest.raises(ContractViolation):
            contract.check_preconditions({"path": "heddle/cas/contracts.py"})


# ── Audit integration ────────────────────────────────────────────────


class TestAuditIntegration:
    def test_violation_is_audited(self, tmp_path, monkeypatch):
        """Contract violations produce audit log entries."""
        from heddle.security import audit as audit_mod

        test_logger = audit_mod.AuditLogger(log_dir=tmp_path / "audit")
        monkeypatch.setattr(audit_mod, "_global_audit", test_logger)

        contract = AgentContract(
            agent_name="audited-agent",
            preconditions=(ContractRule("block", _always_fail, "blocked"),),
        )
        with pytest.raises(ContractViolation):
            contract.check_preconditions({"action": "bad"})

        entries = test_logger.recent(10)
        assert len(entries) == 1
        assert entries[0]["event"] == "trust_violation"
        assert entries[0]["agent"] == "audited-agent"
        assert "precondition" in entries[0]["action"]
