"""CAS session manager — chat sessions with workspace lifecycle."""

from __future__ import annotations

import logging
import re
import uuid
from dataclasses import dataclass, field
from datetime import datetime, timezone
from typing import Any

from cas.audit import get_cas_auditor
from cas.conductor import Conductor
from cas.contracts import AgentContract, load_contract_from_config
from cas.llm import generate_chat_reply, generate_workspace_content, generate_workspace_edit
from cas.store import CASStore
from cas.workspaces import Workspace, WorkspaceManager

logger = logging.getLogger(__name__)


# ── Intent detection ────────────────────────────────────────────────

_DOC_NOUNS = (
    r"document|doc|proposal|report|letter|memo|essay|article|plan|outline|"
    r"resume|cv|email|brief|spec|story|blog|post|summary|agenda|budget|"
    r"invoice|contract|pitch|bio|profile|note|notes|"
    r"template|form|guide|handbook|manual|policy|procedure|playbook|"
    r"readme|changelog|roadmap|brief|deck|overview|draft|ticket|issue"
)

_CODE_NOUNS = (
    r"script|program|function|class|module|snippet|code|file|"
    r"api|endpoint|query|schema|migration|test|dockerfile|config"
)

_LIST_NOUNS = r"list|checklist|todo|to-do|table|inventory|index|glossary|outline"

_CREATE_PATTERNS: list[tuple[re.Pattern[str], str]] = [
    (re.compile(rf"\b(?:write|draft|create|make|start|begin|compose)\b.*\b(?:{_CODE_NOUNS})\b", re.IGNORECASE), "code"),
    (re.compile(rf"\b(?:{_CODE_NOUNS})\b.*\b(?:write|draft|create|make|start|begin|compose)\b", re.IGNORECASE), "code"),
    (re.compile(rf"\b(?:write|draft|create|make|start|begin|compose)\b.*\b(?:{_LIST_NOUNS})\b", re.IGNORECASE), "list"),
    (re.compile(rf"\b(?:{_LIST_NOUNS})\b.*\b(?:write|draft|create|make|start|begin|compose)\b", re.IGNORECASE), "list"),
    (re.compile(rf"\b(?:write|draft|create|make|start|begin|compose)\b.*\b(?:{_DOC_NOUNS})\b", re.IGNORECASE), "document"),
    (re.compile(rf"\b(?:{_DOC_NOUNS})\b.*\b(?:write|draft|create|make|start|begin|compose)\b", re.IGNORECASE), "document"),
    (re.compile(r"\bnew\s+(?:document|doc|note|proposal|report|resume|email|brief)\b", re.IGNORECASE), "document"),
    (re.compile(r"\bnew\s+(?:script|program|code|function|module)\b", re.IGNORECASE), "code"),
    (re.compile(r"\bnew\s+(?:list|checklist|todo)\b", re.IGNORECASE), "list"),
    (re.compile(r"\bi\s+need\s+to\s+(?:write|draft)\b", re.IGNORECASE), "document"),
]

_EDIT_PATTERNS: list[re.Pattern[str]] = [
    re.compile(r"\b(?:edit|update|change|modify|revise|rewrite|append|insert|remove|delete|expand|shorten|rename)\b", re.IGNORECASE),
    re.compile(r"\badd\b.{0,60}\b(?:section|paragraph|part|chapter|bullet|point|entry|item|row|function|method|class)\b", re.IGNORECASE),
    re.compile(r"\b(?:fix|improve|clean\s+up|polish|proofread|refactor|optimise|optimize)\b", re.IGNORECASE),
]

# Phrases where the user signals they will edit manually — must NOT trigger
# an LLM edit call. Checked before _EDIT_PATTERNS in detect_intent().
# Note: no \b anchors here — simpler patterns, less corruption risk.
_SELF_EDIT_PATTERNS: list[re.Pattern[str]] = [
    re.compile(r"edit\s+(it\s+)?(directly|myself|yourself|manually|now)", re.IGNORECASE),
    re.compile(r"i'?ll\s+(edit|do|fix|change|update|write)\s*(it|that|this)?", re.IGNORECASE),
    re.compile(r"let\s+me\s+(edit|do|fix|change|update|write)", re.IGNORECASE),
    re.compile(r"(i'll|i will|i can)\s+(take it from here|handle it)", re.IGNORECASE),
    re.compile(r"just\s+(edit|open|show)\s+(it|the editor)", re.IGNORECASE),
    re.compile(r"i('ll| will| can)\s+do\s+(it|that)\s*(myself|manually)?", re.IGNORECASE),
]

_CLOSE_PATTERNS: list[re.Pattern[str]] = [
    re.compile(r"\b(?:close|done|finish|discard|dismiss)\b.*\b(?:workspace|document|doc|editor|file)\b", re.IGNORECASE),
    re.compile(r"\b(?:workspace|document|doc|editor|file)\b.*\b(?:close|done|finish|discard|dismiss)\b", re.IGNORECASE),
]


@dataclass(frozen=True)
class Intent:
    kind: str
    title_hint: str = ""
    ws_type: str = "document"


def detect_intent(message: str) -> Intent:
    for pattern in _CLOSE_PATTERNS:
        if pattern.search(message):
            return Intent(kind="close_workspace")
    # Self-edit exclusions: user will edit manually — route to chat, not LLM
    for pattern in _SELF_EDIT_PATTERNS:
        if pattern.search(message):
            return Intent(kind="chat")
    for pattern in _EDIT_PATTERNS:
        if pattern.search(message):
            return Intent(kind="edit_workspace")
    for pattern, ws_type in _CREATE_PATTERNS:
        if pattern.search(message):
            return Intent(
                kind="create_workspace",
                title_hint=_extract_title_hint(message),
                ws_type=ws_type,
            )
    return Intent(kind="chat")


def _extract_title_hint(message: str) -> str:
    m = re.search(
        r"\b(?:write|draft|create|make|start|begin|compose)\s+(?:me\s+)?(?:a|an|the|my|our)?\s*(.+)",
        message, re.IGNORECASE,
    )
    if m:
        return " ".join(m.group(1).strip().rstrip(".!?").split()[:6]).title()
    return " ".join(message.split()[:6]).title()


# ── Response / history / session ────────────────────────────────────

@dataclass
class ShellResponse:
    chat_reply: str
    workspace: Workspace | None = None
    intent: Intent = field(default_factory=lambda: Intent(kind="chat"))

    def to_dict(self) -> dict[str, Any]:
        return {
            "chat_reply": self.chat_reply,
            "workspace": self.workspace.to_dict() if self.workspace else None,
            "intent": self.intent.kind,
        }


@dataclass
class Message:
    role: str
    text: str
    timestamp: datetime


@dataclass
class Session:
    id: str
    created_at: datetime
    history: list[Message] = field(default_factory=list)

    def add_message(self, role: str, text: str) -> Message:
        msg = Message(role=role, text=text, timestamp=datetime.now(timezone.utc))
        self.history.append(msg)
        return msg


# ── Shell ───────────────────────────────────────────────────────────

_DEFAULT_CONTRACT_CONFIG: dict[str, Any] = {
    "allowed_workspace_types": ["document", "code", "list"],
    "max_workspace_size_kb": 512,
    "network_access": False,
}


class Shell:
    """CAS session manager with full persistence via CASStore."""

    def __init__(
        self,
        contract_config: dict[str, Any] | None = None,
        conductor: Conductor | None = None,
        store: CASStore | None = None,
    ) -> None:
        self._contract_config = contract_config or _DEFAULT_CONTRACT_CONFIG
        self._conductor = conductor if conductor is not None else Conductor()
        self._auditor = get_cas_auditor()
        self._store = store if store is not None else CASStore()
        self.workspaces = WorkspaceManager(store=self._store)
        self._sessions: dict[str, Session] = {}
        self._restore()

    def _contract_factory(self) -> AgentContract:
        return load_contract_from_config("cas-workspace", self._contract_config)

    def _restore(self) -> None:
        ws_data = self._store.load_workspaces(self._contract_factory)
        self.workspaces.restore(ws_data)
        session_rows = self._store.load_sessions()
        for sid, row in session_rows.items():
            session = Session(id=sid, created_at=datetime.fromisoformat(row["created_at"]))
            for msg_row in self._store.load_messages(sid):
                msg = Message(
                    role=msg_row["role"], text=msg_row["text"],
                    timestamp=datetime.fromisoformat(msg_row["timestamp"]),
                )
                session.history.append(msg)
            self._sessions[sid] = session
        n_ws, n_sess = len(ws_data), len(session_rows)
        if n_ws or n_sess:
            logger.info("Restored %d sessions, %d workspaces from store", n_sess, n_ws)

    def create_session(self) -> Session:
        session = Session(id=uuid.uuid4().hex[:12], created_at=datetime.now(timezone.utc))
        self._sessions[session.id] = session
        self._store.save_session(session)
        self._conductor.observe_session_start()
        self._auditor.log_session_create(session.id)
        logger.info("Created session %s", session.id)
        return session

    def get_session(self, session_id: str) -> Session:
        try:
            return self._sessions[session_id]
        except KeyError:
            raise KeyError(f"No session with id '{session_id}'")

    def process_message(self, session_id: str, message: str) -> ShellResponse:
        session = self.get_session(session_id)
        session.add_message("user", message)
        self._store.save_message(session_id, session.history[-1])

        intent = detect_intent(message)

        if intent.kind == "create_workspace":
            response = self._handle_create(intent, message, session_id)
        elif intent.kind == "edit_workspace":
            response = self._handle_edit(message, session_id, session=session)
        elif intent.kind == "close_workspace":
            response = self._handle_close(session_id)
        else:
            response = ShellResponse(
                chat_reply=self._chat_reply(message, session=session),
                intent=intent,
            )

        session.add_message("shell", response.chat_reply)
        self._store.save_message(session_id, session.history[-1])
        ws_title = response.workspace.title if response.workspace else None
        ws_type = response.workspace.type if response.workspace else None
        self._conductor.observe(intent.kind, message, workspace_title=ws_title, ws_type=ws_type)
        return response

    def _handle_create(self, intent: Intent, message: str, session_id: str) -> ShellResponse:
        title = intent.title_hint or "Untitled"
        ws_type = intent.ws_type
        contract = self._contract_factory()
        user_context = self._conductor.user_context()
        content = generate_workspace_content(title, message, ws_type=ws_type, user_context=user_context)
        ws = self.workspaces.create(title, content, contract, workspace_type=ws_type, session_id=session_id)
        self._auditor.log_workspace_create(ws, session_id)
        return ShellResponse(
            chat_reply=f'Created {ws_type} workspace "{ws.title}". You can edit it directly or ask me to make changes.',
            workspace=ws, intent=intent,
        )

    def _handle_edit(self, message: str, session_id: str, session: Session | None = None) -> ShellResponse:
        active = self.workspaces.list_active()
        if not active:
            return ShellResponse(
                chat_reply="No active workspace to edit. Ask me to create one first.",
                intent=Intent(kind="edit_workspace"),
            )
        ws = active[-1]
        user_context = self._conductor.user_context()
        new_content = generate_workspace_edit(
            ws.title, ws.content, message, ws_type=ws.type, user_context=user_context
        )
        if not new_content:
            new_content = ws.content + f"\n\n{message}\n"
        self.workspaces.update(ws.id, content=new_content)
        ws = self.workspaces.get(ws.id)
        self._auditor.log_workspace_update(ws, session_id, message)
        return ShellResponse(
            chat_reply=f'Updated workspace "{ws.title}".',
            workspace=ws, intent=Intent(kind="edit_workspace"),
        )

    def _handle_close(self, session_id: str) -> ShellResponse:
        active = self.workspaces.list_active()
        if not active:
            return ShellResponse(
                chat_reply="No active workspace to close.",
                intent=Intent(kind="close_workspace"),
            )
        ws = active[-1]
        self.workspaces.close(ws.id)
        self._auditor.log_workspace_close(ws, session_id)
        return ShellResponse(
            chat_reply=f'Closed workspace "{ws.title}".',
            workspace=ws, intent=Intent(kind="close_workspace"),
        )

    def _chat_reply(self, message: str, session: Session | None = None) -> str:
        history = None
        if session is not None and session.history:
            history = [
                {"role": "user" if m.role == "user" else "assistant", "content": m.text}
                for m in session.history[-6:]
            ]
        return generate_chat_reply(message, history, user_context=self._conductor.user_context())
