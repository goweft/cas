"""CAS Conductor — behavioral learning and user context.

Observes shell interactions over time and builds a persistent user profile
that is fed back into LLM system prompts, progressively calibrating CAS to
a specific user's patterns without requiring explicit configuration.

Profile is stored as JSON at ~/.cas/profile.json. Single-user design.
All observation methods are fail-safe — a corrupt or missing profile never
breaks the shell; the conductor simply returns no context.

Public API:
    conductor = Conductor()
    conductor.observe(intent, message, workspace=None, ws_type=None)
    conductor.user_context()  -> str for LLM system prompts
    conductor.profile_summary() -> dict for UI display
"""

from __future__ import annotations

import json
import logging
import re
from collections import Counter
from datetime import datetime, timezone
from pathlib import Path
from typing import Any

logger = logging.getLogger(__name__)

_PROFILE_PATH = Path.home() / ".cas" / "profile.json"
_MAX_PHRASES      = 30
_MAX_CONTEXT_TYPES = 4
_MAX_CONTEXT_VERBS = 3

# Edit verbs — broader pattern, matches anywhere in message
_EDIT_VERB_RE = re.compile(
    r"\b(add|append|insert|revise|rewrite|remove|delete|update|change|modify|"
    r"expand|shorten|summarise|summarize|fix|improve|rename|refactor|"
    r"clean|polish|proofread|reorganize|restructure)\b",
    re.IGNORECASE,
)

# Document content-type nouns (for extracting topic from message text)
_DOC_TYPE_RE = re.compile(
    r"\b(resume|cv|letter|proposal|report|memo|essay|article|plan|outline|"
    r"note|brief|spec|story|blog|post|summary|agenda|budget|invoice|"
    r"contract|script|pitch|bio|profile|email|list|document|doc|"
    r"function|script|program|class|module|test|query|schema)\b",
    re.IGNORECASE,
)

# Minimum signal before generating any context
_MIN_WORKSPACES = 1   # lower threshold — one workspace is enough to start
_MIN_MESSAGES   = 2


class Conductor:
    """Observes CAS interactions and builds a persistent user profile."""

    def __init__(self, profile_path: Path = _PROFILE_PATH) -> None:
        self._path = profile_path
        self._profile: dict[str, Any] = self._load()

    # ── Persistence ─────────────────────────────────────────────

    def _load(self) -> dict[str, Any]:
        try:
            if self._path.exists():
                return self._defaults(json.loads(self._path.read_text()))
        except Exception as exc:
            logger.warning("Conductor: could not load profile: %s", exc)
        return self._defaults({})

    def _save(self) -> None:
        try:
            self._path.parent.mkdir(parents=True, exist_ok=True)
            self._path.write_text(json.dumps(self._profile, indent=2))
        except Exception as exc:
            logger.warning("Conductor: could not save profile: %s", exc)

    @staticmethod
    def _defaults(data: dict[str, Any]) -> dict[str, Any]:
        data.setdefault("doc_types", {})       # content-type nouns from messages
        data.setdefault("ws_types", {})        # actual workspace types (document/code/list)
        data.setdefault("edit_verbs", {})      # edit verb counts
        data.setdefault("phrases", [])         # recent create-intent messages
        data.setdefault("session_count", 0)
        data.setdefault("message_count", 0)
        data.setdefault("workspace_count", 0)  # actual workspaces created (not word count)
        data.setdefault("edit_count", 0)       # total edit operations
        data.setdefault("last_seen", None)
        return data

    # ── Observation ──────────────────────────────────────────────

    def observe(
        self,
        intent_kind: str,
        user_message: str,
        workspace_title: str | None = None,
        ws_type: str | None = None,
    ) -> None:
        """Record one shell interaction."""
        try:
            self._profile["message_count"] += 1
            self._profile["last_seen"] = datetime.now(timezone.utc).isoformat()

            if intent_kind == "create_workspace":
                self._observe_create(user_message, workspace_title, ws_type)
            elif intent_kind == "edit_workspace":
                self._observe_edit(user_message)

        except Exception as exc:
            logger.warning("Conductor.observe failed: %s", exc)
        finally:
            self._save()

    def observe_session_start(self) -> None:
        try:
            self._profile["session_count"] += 1
            self._save()
        except Exception as exc:
            logger.warning("Conductor.observe_session_start failed: %s", exc)

    def _observe_create(self, message: str, title: str | None, ws_type: str | None) -> None:
        # Increment actual workspace count once per creation
        self._profile["workspace_count"] += 1

        # Track actual workspace type (document/code/list)
        if ws_type:
            wt = self._profile["ws_types"]
            wt[ws_type] = wt.get(ws_type, 0) + 1

        # Extract topic nouns — deduplicated per message to avoid double-counting
        # "write a brief project plan" should count as one observation of 'plan',
        # not two (brief + plan both increment separately)
        types_found = list(dict.fromkeys(          # preserve order, deduplicate
            m.lower() for m in _DOC_TYPE_RE.findall(message)
        ))
        # Prefer the last/most-specific noun (usually the actual type)
        if types_found:
            primary = types_found[-1]
            doc_types = self._profile["doc_types"]
            doc_types[primary] = doc_types.get(primary, 0) + 1

        # Also record from title if it adds a different type
        if title:
            title_types = list(dict.fromkeys(m.lower() for m in _DOC_TYPE_RE.findall(title)))
            if title_types and title_types[-1] != (types_found[-1] if types_found else None):
                t = title_types[-1]
                doc_types = self._profile["doc_types"]
                doc_types[t] = doc_types.get(t, 0) + 1

        # Phrase ring buffer
        phrases = self._profile["phrases"]
        phrases.append(message.strip())
        if len(phrases) > _MAX_PHRASES:
            self._profile["phrases"] = phrases[-_MAX_PHRASES:]

    def _observe_edit(self, message: str) -> None:
        self._profile["edit_count"] = self._profile.get("edit_count", 0) + 1
        verbs_found = list(dict.fromkeys(m.lower() for m in _EDIT_VERB_RE.findall(message)))
        edit_verbs = self._profile["edit_verbs"]
        for v in verbs_found:
            edit_verbs[v] = edit_verbs.get(v, 0) + 1

    # ── Context generation ───────────────────────────────────────

    def user_context(self) -> str:
        """Return a natural-language user context string for LLM prompts.

        Returns empty string if insufficient signal.
        """
        try:
            return self._build_context()
        except Exception as exc:
            logger.warning("Conductor.user_context failed: %s", exc)
            return ""

    def _build_context(self) -> str:
        p = self._profile
        parts: list[str] = []

        # Lower threshold — start adapting after first workspace + a couple messages
        if p["workspace_count"] < _MIN_WORKSPACES and p["message_count"] < _MIN_MESSAGES:
            return ""

        # Workspace type preferences (document/code/list)
        ws_types = Counter(p.get("ws_types", {})).most_common(3)
        doc_types = Counter(p["doc_types"]).most_common(_MAX_CONTEXT_TYPES)

        if ws_types:
            dominant_ws_type = ws_types[0][0]
            if len(ws_types) == 1:
                parts.append(f"This user primarily creates {dominant_ws_type} workspaces.")
            else:
                type_list = ", ".join(f"{t} ({c})" for t, c in ws_types)
                parts.append(f"This user creates: {type_list} workspaces.")

        # Content topic preferences (proposal, resume, etc.)
        if doc_types:
            top = [t for t, _ in doc_types[:3]]
            if len(top) == 1:
                parts.append(f"Their documents are primarily {top[0]}s.")
            else:
                parts.append(
                    f"Their documents are typically {', '.join(top[:-1])} and {top[-1]}s."
                )

        # Edit style
        edit_verbs = Counter(p["edit_verbs"]).most_common(_MAX_CONTEXT_VERBS)
        edit_count = p.get("edit_count", 0)
        if edit_verbs and edit_count > 0:
            top_verbs = [v for v, _ in edit_verbs]
            if "rewrite" in top_verbs or "revise" in top_verbs:
                parts.append("They prefer full rewrites over incremental edits.")
            elif "shorten" in top_verbs or "summarize" in top_verbs or "summarise" in top_verbs:
                parts.append("They frequently ask to shorten or condense content.")
            elif "add" in top_verbs or "append" in top_verbs or "insert" in top_verbs:
                parts.append("They prefer adding sections over rewriting existing content.")
            elif "fix" in top_verbs or "improve" in top_verbs or "proofread" in top_verbs:
                parts.append("They frequently ask for quality improvements and fixes.")

        # Return user signal
        if p["session_count"] > 1:
            ws_str = f"{p['workspace_count']} workspace{'s' if p['workspace_count'] != 1 else ''}"
            parts.append(f"Returning user: {p['session_count']} sessions, {ws_str} created.")

        return " ".join(parts)

    # ── Introspection ─────────────────────────────────────────────

    def profile_summary(self) -> dict[str, Any]:
        """Return profile plus the current generated context string."""
        summary = dict(self._profile)
        summary["context"] = self.user_context()
        summary["has_context"] = bool(summary["context"])
        return summary

    def reset(self) -> None:
        self._profile = self._defaults({})
        self._save()
