"""CAS audit integration — workspace actions into Heddle's audit log.

Wraps heddle.security.audit.AuditLogger with CAS-specific helpers.
All workspace lifecycle events (create, update, close, export) are
recorded as tamper-evident, hash-chained entries alongside Heddle's
own audit trail.

If Heddle's audit module is unavailable (e.g. running CAS standalone),
all methods are no-ops — the shell never fails due to audit errors.

Public API:
    from cas.audit import get_cas_auditor
    auditor = get_cas_auditor()
    auditor.log_workspace_create(ws, session_id)
    auditor.log_workspace_update(ws, session_id, edit_request)
    auditor.log_workspace_close(ws, session_id)
    auditor.log_workspace_export(ws, session_id, fmt)
"""

from __future__ import annotations

import logging
from typing import TYPE_CHECKING, Any

if TYPE_CHECKING:
    from cas.workspaces import Workspace

logger = logging.getLogger(__name__)

# ── Attempt to import Heddle's audit logger ─────────────────────────

try:
    from heddle.security.audit import get_audit_logger as _get_heddle_audit
    _HEDDLE_AVAILABLE = True
except ImportError:
    _HEDDLE_AVAILABLE = False
    logger.debug("heddle.security.audit not available — CAS audit is a no-op")


class CasAuditor:
    """Thin CAS wrapper around Heddle's AuditLogger.

    Records workspace lifecycle events using log_tool_call() and
    log_agent_lifecycle(). Falls back gracefully if Heddle is absent.
    """

    def __init__(self) -> None:
        self._heddle = _get_heddle_audit() if _HEDDLE_AVAILABLE else None

    def _log(self, event: str, agent: str, params: dict[str, Any], status: str = "success") -> None:
        if self._heddle is None:
            return
        try:
            self._heddle.log_tool_call(
                agent_name=agent,
                tool_name=event,
                parameters=params,
                result_status=status,
            )
        except Exception as exc:
            logger.warning("CAS audit write failed: %s", exc)

    def log_workspace_create(self, ws: "Workspace", session_id: str) -> None:
        self._log("workspace_create", "cas-workspace", {
            "workspace_id": ws.id,
            "title": ws.title,
            "type": ws.type,
            "session_id": session_id,
            "content_bytes": len(ws.content.encode()),
        })

    def log_workspace_update(self, ws: "Workspace", session_id: str, edit_request: str) -> None:
        self._log("workspace_update", "cas-workspace", {
            "workspace_id": ws.id,
            "title": ws.title,
            "session_id": session_id,
            "edit_request": edit_request[:200],  # truncate for log hygiene
            "content_bytes": len(ws.content.encode()),
        })

    def log_workspace_close(self, ws: "Workspace", session_id: str) -> None:
        self._log("workspace_close", "cas-workspace", {
            "workspace_id": ws.id,
            "title": ws.title,
            "session_id": session_id,
        })

    def log_workspace_export(self, ws: "Workspace", session_id: str, fmt: str) -> None:
        self._log("workspace_export", "cas-workspace", {
            "workspace_id": ws.id,
            "title": ws.title,
            "session_id": session_id,
            "format": fmt,
            "content_bytes": len(ws.content.encode()),
        })

    def log_session_create(self, session_id: str) -> None:
        if self._heddle is None:
            return
        try:
            self._heddle.log_agent_lifecycle(
                agent_name="cas-shell",
                action="session_create",
                detail=f"session={session_id}",
            )
        except Exception as exc:
            logger.warning("CAS audit session write failed: %s", exc)


# ── Singleton ───────────────────────────────────────────────────────

_auditor: CasAuditor | None = None


def get_cas_auditor() -> CasAuditor:
    """Return the singleton CasAuditor instance."""
    global _auditor
    if _auditor is None:
        _auditor = CasAuditor()
    return _auditor
