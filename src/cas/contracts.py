"""Deterministic agent contract enforcement — Design by Contract for AI agents.

Based on Meyer's Design by Contract (1986). Wraps probabilistic agents in
hard, deterministic boundaries that cannot be bypassed at runtime.

Contracts are loaded from signed YAML agent configs and compiled into
Python callables. The agent cannot modify its own contract. Enforcement
is pure Python — no LLM in the loop. Fail-closed on any violation.

Frameworks: OWASP Agentic #3 (Excessive Agency), NIST AI RMF GV-1.3,
MAESTRO Integrity layer
"""

from __future__ import annotations

import fnmatch
import logging
import re
from dataclasses import dataclass, field
from pathlib import Path
from typing import Any, Callable

from heddle.security.audit import get_audit_logger

logger = logging.getLogger(__name__)


class ContractViolation(Exception):
    """Raised when an agent violates its contract. Non-negotiable."""

    def __init__(self, agent_name: str, phase: str, rule: str, detail: str):
        self.agent_name = agent_name
        self.phase = phase  # "precondition", "postcondition", "invariant"
        self.rule = rule
        self.detail = detail
        super().__init__(
            f"Contract violation [{agent_name}] {phase}: {rule} — {detail}"
        )


# ── Contract rule types ──────────────────────────────────────────────


@dataclass(frozen=True)
class ContractRule:
    """A single named, deterministic check."""

    name: str
    check: Callable[..., bool]
    description: str = ""

    def __call__(self, *args: Any, **kwargs: Any) -> bool:
        return self.check(*args, **kwargs)


# ── The contract itself ──────────────────────────────────────────────


@dataclass
class AgentContract:
    """Immutable enforcement layer wrapping an agent.

    Preconditions:  checked before every action  — signature: (action: dict) -> bool
    Postconditions: checked after every action    — signature: (action: dict, result: dict) -> bool
    Invariants:     checked continuously          — signature: () -> bool

    Loaded from signed YAML. Frozen after construction.
    Agent CANNOT modify its own contract.
    """

    agent_name: str
    preconditions: tuple[ContractRule, ...] = ()
    postconditions: tuple[ContractRule, ...] = ()
    invariants: tuple[ContractRule, ...] = ()
    _frozen: bool = field(default=False, repr=False)

    def freeze(self) -> AgentContract:
        """Make the contract immutable. Called after loading from config."""
        object.__setattr__(self, "_frozen", True)
        return self

    def __setattr__(self, name: str, value: Any) -> None:
        if getattr(self, "_frozen", False) and name != "_frozen":
            raise ContractViolation(
                getattr(self, "agent_name", "?"),
                "invariant",
                "immutability",
                f"Cannot modify frozen contract attribute '{name}'",
            )
        object.__setattr__(self, name, value)

    def check_preconditions(self, action: dict[str, Any]) -> None:
        """All preconditions must pass before action. Fail-closed."""
        audit = get_audit_logger()
        for rule in self.preconditions:
            try:
                result = rule(action)
            except Exception as exc:
                # Exception in check = fail-closed
                audit.log_trust_violation(
                    self.agent_name, 0,
                    action="contract_precondition_error",
                    detail=f"Rule '{rule.name}' raised: {exc}",
                )
                raise ContractViolation(
                    self.agent_name, "precondition", rule.name,
                    f"Check raised exception (fail-closed): {exc}",
                ) from exc
            if not result:
                audit.log_trust_violation(
                    self.agent_name, 0,
                    action="contract_precondition_failed",
                    detail=f"Rule '{rule.name}': {rule.description}",
                )
                raise ContractViolation(
                    self.agent_name, "precondition", rule.name,
                    rule.description or "Precondition failed",
                )

    def check_postconditions(
        self, action: dict[str, Any], result: dict[str, Any]
    ) -> None:
        """All postconditions must hold after action. Fail-closed."""
        audit = get_audit_logger()
        for rule in self.postconditions:
            try:
                ok = rule(action, result)
            except Exception as exc:
                audit.log_trust_violation(
                    self.agent_name, 0,
                    action="contract_postcondition_error",
                    detail=f"Rule '{rule.name}' raised: {exc}",
                )
                raise ContractViolation(
                    self.agent_name, "postcondition", rule.name,
                    f"Check raised exception (fail-closed): {exc}",
                ) from exc
            if not ok:
                audit.log_trust_violation(
                    self.agent_name, 0,
                    action="contract_postcondition_failed",
                    detail=f"Rule '{rule.name}': {rule.description}",
                )
                raise ContractViolation(
                    self.agent_name, "postcondition", rule.name,
                    rule.description or "Postcondition failed",
                )

    def check_invariants(self) -> None:
        """Invariants must always hold. Violation = quarantine."""
        audit = get_audit_logger()
        for rule in self.invariants:
            try:
                ok = rule()
            except Exception as exc:
                audit.log_trust_violation(
                    self.agent_name, 0,
                    action="contract_invariant_error",
                    detail=f"Rule '{rule.name}' raised: {exc}",
                )
                raise ContractViolation(
                    self.agent_name, "invariant", rule.name,
                    f"Check raised exception (fail-closed): {exc}",
                ) from exc
            if not ok:
                audit.log_trust_violation(
                    self.agent_name, 0,
                    action="contract_invariant_failed",
                    detail=f"Rule '{rule.name}': {rule.description}",
                )
                raise ContractViolation(
                    self.agent_name, "invariant", rule.name,
                    rule.description or "Invariant violated",
                )


# ── Built-in rule factories (compiled from YAML contract stanzas) ────


def make_filesystem_scope_rule(scope: str) -> ContractRule:
    """Action must target paths within the allowed scope."""
    scope_path = Path(scope).resolve()

    def check(action: dict[str, Any]) -> bool:
        target = action.get("path") or action.get("target", "")
        if not target:
            return True  # No path in action — not a filesystem op
        resolved = Path(target).resolve()
        return str(resolved).startswith(str(scope_path))

    return ContractRule(
        name="filesystem_scope",
        check=check,
        description=f"Path must be within {scope}",
    )


def make_network_access_rule(allowed: bool) -> ContractRule:
    """Block or allow network access."""
    def check(action: dict[str, Any]) -> bool:
        if allowed:
            return True
        has_network = action.get("network", False) or action.get("url")
        return not has_network

    return ContractRule(
        name="network_access",
        check=check,
        description=f"Network access: {'allowed' if allowed else 'denied'}",
    )


def make_max_size_rule(max_kb: int) -> ContractRule:
    """Output size must not exceed the limit."""
    def check(action: dict[str, Any], result: dict[str, Any]) -> bool:
        content = result.get("content", "")
        size_kb = len(content.encode("utf-8")) / 1024 if isinstance(content, str) else 0
        return size_kb <= max_kb

    return ContractRule(
        name="max_workspace_size",
        check=check,
        description=f"Output must not exceed {max_kb}KB",
    )


def make_allowed_types_rule(allowed: list[str]) -> ContractRule:
    """Workspace type must be in the allowed list."""
    allowed_set = frozenset(allowed)

    def check(action: dict[str, Any]) -> bool:
        ws_type = action.get("workspace_type", "")
        if not ws_type:
            return True  # Not a workspace action
        return ws_type in allowed_set

    return ContractRule(
        name="allowed_workspace_types",
        check=check,
        description=f"Workspace type must be one of: {sorted(allowed_set)}",
    )


def make_cannot_modify_rule(patterns: list[str]) -> ContractRule:
    """Action must not target paths matching forbidden patterns."""
    def check(action: dict[str, Any]) -> bool:
        target = action.get("path") or action.get("target", "")
        if not target:
            return True
        for pattern in patterns:
            if fnmatch.fnmatch(target, pattern):
                return False
        return True

    return ContractRule(
        name="cannot_modify",
        check=check,
        description=f"Must not modify paths matching: {patterns}",
    )


# ── Contract loader from agent YAML config ───────────────────────────


def load_contract_from_config(
    agent_name: str, contract_data: dict[str, Any]
) -> AgentContract:
    """Build an AgentContract from the 'contract' stanza in agent YAML.

    Expected keys (all optional):
        filesystem_scope: str
        network_access: bool
        max_workspace_size_kb: int
        allowed_workspace_types: list[str]
        cannot_modify: list[str]
        on_violation: str  (informational — enforcement is always fail-closed)
    """
    preconditions: list[ContractRule] = []
    postconditions: list[ContractRule] = []

    if "filesystem_scope" in contract_data:
        preconditions.append(
            make_filesystem_scope_rule(contract_data["filesystem_scope"])
        )

    if "network_access" in contract_data:
        preconditions.append(
            make_network_access_rule(contract_data["network_access"])
        )

    if "allowed_workspace_types" in contract_data:
        preconditions.append(
            make_allowed_types_rule(contract_data["allowed_workspace_types"])
        )

    if "cannot_modify" in contract_data:
        preconditions.append(
            make_cannot_modify_rule(contract_data["cannot_modify"])
        )

    if "max_workspace_size_kb" in contract_data:
        postconditions.append(
            make_max_size_rule(contract_data["max_workspace_size_kb"])
        )

    contract = AgentContract(
        agent_name=agent_name,
        preconditions=tuple(preconditions),
        postconditions=tuple(postconditions),
        invariants=(),
    )
    contract.freeze()

    logger.info(
        "Loaded contract for %s: %d pre, %d post, %d inv",
        agent_name,
        len(preconditions),
        len(postconditions),
        0,
    )
    return contract
