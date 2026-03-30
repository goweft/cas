"""CAS persistence layer — SQLite-backed store for sessions and workspaces.

All state that was previously in-memory (sessions, messages, workspaces)
is now persisted to ~/.cas/cas.db via the standard library sqlite3 module.
No external dependencies required.

Public API:
    store = CASStore()
    store.save_session(session)
    store.save_message(session_id, message)
    store.save_workspace(workspace, session_id)
    store.update_workspace(workspace)          # saves history snapshot first
    store.close_workspace(workspace_id, closed_at)
    store.load_sessions() -> dict
    store.load_workspaces(contract_factory) -> dict
    store.load_history(workspace_id) -> list[dict]   # [{version, title, content, saved_at}]
    store.get_version(workspace_id, version) -> dict | None
"""

from __future__ import annotations

import logging
import sqlite3
from datetime import datetime, timezone
from pathlib import Path
from typing import TYPE_CHECKING, Any, Callable

if TYPE_CHECKING:
    from cas.shell import Message, Session
    from cas.workspaces import Workspace
    from cas.contracts import AgentContract

logger = logging.getLogger(__name__)

_DB_PATH = Path.home() / ".cas" / "cas.db"

_SCHEMA = """
PRAGMA journal_mode=WAL;
PRAGMA foreign_keys=ON;

CREATE TABLE IF NOT EXISTS sessions (
    id          TEXT PRIMARY KEY,
    created_at  TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS messages (
    id          INTEGER PRIMARY KEY AUTOINCREMENT,
    session_id  TEXT NOT NULL REFERENCES sessions(id),
    role        TEXT NOT NULL,
    text        TEXT NOT NULL,
    timestamp   TEXT NOT NULL
);

CREATE TABLE IF NOT EXISTS workspaces (
    id          TEXT PRIMARY KEY,
    session_id  TEXT,  -- informational, no FK constraint
    type        TEXT NOT NULL DEFAULT 'document',
    title       TEXT NOT NULL,
    content     TEXT NOT NULL DEFAULT '',
    created_at  TEXT NOT NULL,
    closed_at   TEXT
);

CREATE TABLE IF NOT EXISTS workspace_history (
    id           INTEGER PRIMARY KEY AUTOINCREMENT,
    workspace_id TEXT NOT NULL,
    version      INTEGER NOT NULL,
    title        TEXT NOT NULL,
    content      TEXT NOT NULL,
    saved_at     TEXT NOT NULL,
    UNIQUE (workspace_id, version)
);

CREATE INDEX IF NOT EXISTS idx_messages_session   ON messages(session_id);
CREATE INDEX IF NOT EXISTS idx_workspaces_session ON workspaces(session_id);
CREATE INDEX IF NOT EXISTS idx_history_workspace  ON workspace_history(workspace_id, version);
"""

# Maximum number of history versions retained per workspace
MAX_HISTORY_VERSIONS = 50


def _fmt_dt(dt: datetime | None) -> str | None:
    return dt.isoformat() if dt else None


def _parse_dt(s: str | None) -> datetime | None:
    if not s:
        return None
    try:
        return datetime.fromisoformat(s)
    except ValueError:
        return None


class CASStore:
    """SQLite-backed persistence for CAS sessions and workspaces."""

    def __init__(self, db_path: Path = _DB_PATH) -> None:
        self._path = db_path
        self._path.parent.mkdir(parents=True, exist_ok=True)
        self._conn = sqlite3.connect(str(self._path), check_same_thread=False)
        self._conn.row_factory = sqlite3.Row
        self._migrate()
        logger.info("CASStore opened: %s", self._path)

    def _migrate(self) -> None:
        try:
            self._conn.executescript(_SCHEMA)
            self._conn.commit()
        except Exception as exc:
            logger.error("CASStore migration failed: %s", exc)

    def _exec(self, sql: str, params: tuple = ()) -> None:
        try:
            self._conn.execute(sql, params)
            self._conn.commit()
        except Exception as exc:
            logger.error("CASStore write error: %s | sql=%s", exc, sql[:80])

    # ── Sessions ─────────────────────────────────────────────────────

    def save_session(self, session: "Session") -> None:
        self._exec(
            "INSERT OR IGNORE INTO sessions (id, created_at) VALUES (?, ?)",
            (session.id, _fmt_dt(session.created_at)),
        )

    def load_sessions(self) -> dict[str, Any]:
        try:
            rows = self._conn.execute("SELECT * FROM sessions").fetchall()
            return {r["id"]: dict(r) for r in rows}
        except Exception as exc:
            logger.error("CASStore load_sessions failed: %s", exc)
            return {}

    # ── Messages ─────────────────────────────────────────────────────

    def save_message(self, session_id: str, message: "Message") -> None:
        self._exec(
            "INSERT INTO messages (session_id, role, text, timestamp) VALUES (?, ?, ?, ?)",
            (session_id, message.role, message.text, _fmt_dt(message.timestamp)),
        )

    def load_messages(self, session_id: str) -> list[dict]:
        try:
            rows = self._conn.execute(
                "SELECT role, text, timestamp FROM messages WHERE session_id=? ORDER BY id",
                (session_id,),
            ).fetchall()
            return [dict(r) for r in rows]
        except Exception as exc:
            logger.error("CASStore load_messages failed: %s", exc)
            return []

    # ── Workspaces ────────────────────────────────────────────────────

    def save_workspace(self, workspace: "Workspace", session_id: str) -> None:
        self._exec(
            """INSERT OR REPLACE INTO workspaces
               (id, session_id, type, title, content, created_at, closed_at)
               VALUES (?, ?, ?, ?, ?, ?, ?)""",
            (
                workspace.id, session_id, workspace.type,
                workspace.title, workspace.content,
                _fmt_dt(workspace.created_at), _fmt_dt(workspace.closed_at),
            ),
        )

    def update_workspace(self, workspace: "Workspace") -> None:
        """Update workspace state, saving a history snapshot first."""
        self._snapshot(workspace.id)
        self._exec(
            "UPDATE workspaces SET title=?, content=?, closed_at=? WHERE id=?",
            (workspace.title, workspace.content, _fmt_dt(workspace.closed_at), workspace.id),
        )
        self._prune_history(workspace.id)

    def close_workspace(self, workspace_id: str, closed_at: datetime) -> None:
        self._exec(
            "UPDATE workspaces SET closed_at=? WHERE id=?",
            (_fmt_dt(closed_at), workspace_id),
        )

    def load_workspaces(self, contract_factory: Callable[[], "AgentContract"]) -> dict[str, Any]:
        """Return active workspace rows with reconstructed contracts."""
        try:
            rows = self._conn.execute(
                "SELECT * FROM workspaces WHERE closed_at IS NULL ORDER BY created_at"
            ).fetchall()
            result = {}
            for r in rows:
                result[r["id"]] = {
                    "id": r["id"],
                    "session_id": r["session_id"],
                    "type": r["type"],
                    "title": r["title"],
                    "content": r["content"],
                    "created_at": _parse_dt(r["created_at"]),
                    "closed_at": None,
                    "contract": contract_factory(),
                }
            return result
        except Exception as exc:
            logger.error("CASStore load_workspaces failed: %s", exc)
            return {}

    # ── History ───────────────────────────────────────────────────────

    def _next_version(self, workspace_id: str) -> int:
        """Return the next version number for a workspace."""
        row = self._conn.execute(
            "SELECT COALESCE(MAX(version), 0) + 1 FROM workspace_history WHERE workspace_id=?",
            (workspace_id,),
        ).fetchone()
        return row[0] if row else 1

    def _snapshot(self, workspace_id: str) -> None:
        """Save current workspace state as a history version."""
        try:
            current = self._conn.execute(
                "SELECT title, content FROM workspaces WHERE id=?",
                (workspace_id,),
            ).fetchone()
            if not current:
                return
            version = self._next_version(workspace_id)
            self._conn.execute(
                """INSERT OR IGNORE INTO workspace_history
                   (workspace_id, version, title, content, saved_at)
                   VALUES (?, ?, ?, ?, ?)""",
                (workspace_id, version, current["title"], current["content"],
                 datetime.now(timezone.utc).isoformat()),
            )
            self._conn.commit()
        except Exception as exc:
            logger.error("CASStore snapshot failed: %s", exc)

    def _prune_history(self, workspace_id: str) -> None:
        """Keep only the most recent MAX_HISTORY_VERSIONS versions."""
        try:
            self._conn.execute(
                """DELETE FROM workspace_history
                   WHERE workspace_id=? AND version <= (
                       SELECT MAX(version) - ? FROM workspace_history WHERE workspace_id=?
                   )""",
                (workspace_id, MAX_HISTORY_VERSIONS, workspace_id),
            )
            self._conn.commit()
        except Exception as exc:
            logger.error("CASStore prune failed: %s", exc)

    def load_history(self, workspace_id: str) -> list[dict]:
        """Return history versions newest-first."""
        try:
            rows = self._conn.execute(
                """SELECT version, title, content, saved_at
                   FROM workspace_history
                   WHERE workspace_id=?
                   ORDER BY version DESC""",
                (workspace_id,),
            ).fetchall()
            return [dict(r) for r in rows]
        except Exception as exc:
            logger.error("CASStore load_history failed: %s", exc)
            return []

    def get_version(self, workspace_id: str, version: int) -> dict | None:
        """Return a specific history version, or None if not found."""
        try:
            row = self._conn.execute(
                "SELECT version, title, content, saved_at FROM workspace_history WHERE workspace_id=? AND version=?",
                (workspace_id, version),
            ).fetchone()
            return dict(row) if row else None
        except Exception as exc:
            logger.error("CASStore get_version failed: %s", exc)
            return None

    def apply_version(self, workspace_id: str, version: int) -> bool:
        """Restore a workspace to a historical version. Snapshots current first.

        Returns True on success, False if the version doesn't exist.
        """
        try:
            target = self.get_version(workspace_id, version)
            if not target:
                return False
            self._snapshot(workspace_id)
            self._exec(
                "UPDATE workspaces SET title=?, content=? WHERE id=?",
                (target["title"], target["content"], workspace_id),
            )
            self._prune_history(workspace_id)
            return True
        except Exception as exc:
            logger.error("CASStore apply_version failed: %s", exc)
            return False

    def undo(self, workspace_id: str) -> dict | None:
        """Restore the most recent history version.

        Snapshots the current state first (so undo is itself undoable).
        Returns the restored version dict, or None if no history exists.
        """
        history = self.load_history(workspace_id)
        if not history:
            return None
        latest = history[0]  # newest-first
        success = self.apply_version(workspace_id, latest["version"])
        if not success:
            return None
        # Return the current workspace state after undo
        try:
            row = self._conn.execute(
                "SELECT title, content FROM workspaces WHERE id=?",
                (workspace_id,),
            ).fetchone()
            return dict(row) if row else None
        except Exception:
            return None

    def close(self) -> None:
        try:
            self._conn.close()
        except Exception:
            pass
