"""CAS LLM bridge — multi-provider, synchronous and streaming.

Providers
---------
Set CAS_PROVIDER environment variable to select a backend:

    CAS_PROVIDER=ollama      (default) — local Ollama inference
    CAS_PROVIDER=anthropic   — Anthropic API (requires ANTHROPIC_API_KEY)

Model routing
-------------
Each workspace type maps to a model appropriate for that provider:

    Ollama:
        document / list / chat  → qwen3.5:9b
        code                    → qwen2.5-coder:7b

    Anthropic:
        document / list / chat  → claude-sonnet-4-6
        code                    → claude-haiku-4-5-20251001

Override any model via CAS_MODEL_* environment variables:
    CAS_MODEL_DOCUMENT, CAS_MODEL_LIST, CAS_MODEL_CODE, CAS_MODEL_CHAT

Public non-streaming functions:
    generate_workspace_content(title, user_message, ws_type, user_context) -> str
    generate_workspace_edit(title, current, edit_request, ws_type, user_context) -> str
    generate_chat_reply(message, history, user_context) -> str

Streaming builder functions:
    build_workspace_messages(title, user_message, ws_type, user_context) -> list
    build_edit_messages(title, current, edit_request, ws_type, user_context) -> list
    build_chat_messages(message, history, user_context) -> list
    stream_chat(messages, model, temperature) -> Iterator[str]
"""

from __future__ import annotations

import json
import logging
import os
import re
from typing import Any, Iterator

import httpx

logger = logging.getLogger(__name__)

# ── Provider selection ───────────────────────────────────────────────

CAS_PROVIDER = os.environ.get("CAS_PROVIDER", "ollama").lower()

# ── Model routing ────────────────────────────────────────────────────

_DEFAULT_MODELS: dict[str, dict[str, str]] = {
    "ollama": {
        "document": "qwen3.5:9b",
        "list":     "qwen3.5:9b",
        "code":     "qwen2.5-coder:7b",
        "chat":     "qwen3.5:9b",
    },
    "anthropic": {
        "document": "claude-sonnet-4-6",
        "list":     "claude-sonnet-4-6",
        "code":     "claude-haiku-4-5-20251001",
        "chat":     "claude-sonnet-4-6",
    },
}

_MODEL_OVERRIDES = {
    k: os.environ[f"CAS_MODEL_{k.upper()}"]
    for k in ("document", "list", "code", "chat")
    if f"CAS_MODEL_{k.upper()}" in os.environ
}


def model_for(ws_type: str) -> str:
    if ws_type in _MODEL_OVERRIDES:
        return _MODEL_OVERRIDES[ws_type]
    provider_models = _DEFAULT_MODELS.get(CAS_PROVIDER, _DEFAULT_MODELS["ollama"])
    return provider_models.get(ws_type, provider_models["document"])


# ── Ollama internals ─────────────────────────────────────────────────

_OLLAMA_BASE    = os.environ.get("OLLAMA_BASE_URL", "http://localhost:11434")
_TIMEOUT        = 60.0
_STREAM_TIMEOUT = 120.0


def _strip_think(text: str) -> str:
    """Remove <think>...</think> blocks emitted by qwen models."""
    return re.sub(r"<think>.*?</think>", "", text, flags=re.DOTALL).strip()


def _chat_ollama(messages: list[dict], model: str, temperature: float) -> str:
    payload: dict[str, Any] = {
        "model": model, "messages": messages,
        "stream": False, "options": {"temperature": temperature},
    }
    try:
        resp = httpx.post(f"{_OLLAMA_BASE}/api/chat", json=payload, timeout=_TIMEOUT)
        resp.raise_for_status()
        return _strip_think(resp.json()["message"]["content"])
    except httpx.TimeoutException:
        logger.warning("Ollama request timed out (model=%s)", model)
        return ""
    except httpx.HTTPError as exc:
        logger.warning("Ollama HTTP error: %s (model=%s)", exc, model)
        return ""
    except Exception as exc:
        logger.exception("Unexpected Ollama error: %s", exc)
        return ""


def _stream_ollama(
    messages: list[dict], model: str, temperature: float
) -> Iterator[str]:
    payload: dict[str, Any] = {
        "model": model, "messages": messages,
        "stream": True, "options": {"temperature": temperature},
    }
    try:
        with httpx.stream("POST", f"{_OLLAMA_BASE}/api/chat",
                          json=payload, timeout=_STREAM_TIMEOUT) as resp:
            resp.raise_for_status()
            in_think = False
            for line in resp.iter_lines():
                if not line:
                    continue
                try:
                    chunk = json.loads(line)
                except json.JSONDecodeError:
                    continue
                token = chunk.get("message", {}).get("content", "")
                if token:
                    # Suppress think-phase tokens inline
                    if "<think>" in token:
                        in_think = True
                    if not in_think:
                        yield token
                    if "</think>" in token:
                        in_think = False
                if chunk.get("done"):
                    break
    except httpx.TimeoutException:
        logger.warning("Ollama stream timed out (model=%s)", model)
    except httpx.HTTPError as exc:
        logger.warning("Ollama stream HTTP error: %s (model=%s)", exc, model)
    except Exception as exc:
        logger.exception("Unexpected Ollama stream error: %s", exc)


# ── Anthropic internals ──────────────────────────────────────────────

_ANTHROPIC_BASE = "https://api.anthropic.com"
_ANTHROPIC_VERSION = "2023-06-01"


def _anthropic_headers() -> dict[str, str]:
    api_key = os.environ.get("ANTHROPIC_API_KEY", "")
    if not api_key:
        raise RuntimeError(
            "ANTHROPIC_API_KEY is not set. "
            "Export it before starting CAS: export ANTHROPIC_API_KEY=sk-ant-..."
        )
    return {
        "x-api-key": api_key,
        "anthropic-version": _ANTHROPIC_VERSION,
        "content-type": "application/json",
    }


def _split_system(messages: list[dict]) -> tuple[str, list[dict]]:
    """Separate a leading system message from the rest (Anthropic API format)."""
    if messages and messages[0]["role"] == "system":
        return messages[0]["content"], messages[1:]
    return "", messages


def _chat_anthropic(messages: list[dict], model: str, temperature: float) -> str:
    system, user_messages = _split_system(messages)
    payload: dict[str, Any] = {
        "model": model,
        "max_tokens": 4096,
        "temperature": temperature,
        "messages": user_messages,
    }
    if system:
        payload["system"] = system
    try:
        resp = httpx.post(
            f"{_ANTHROPIC_BASE}/v1/messages",
            headers=_anthropic_headers(),
            json=payload,
            timeout=_TIMEOUT,
        )
        resp.raise_for_status()
        data = resp.json()
        text_blocks = [b["text"] for b in data.get("content", []) if b.get("type") == "text"]
        return "".join(text_blocks).strip()
    except httpx.TimeoutException:
        logger.warning("Anthropic request timed out (model=%s)", model)
        return ""
    except httpx.HTTPStatusError as exc:
        logger.warning("Anthropic HTTP error %s: %s (model=%s)",
                       exc.response.status_code, exc.response.text, model)
        return ""
    except RuntimeError as exc:
        logger.error("Anthropic config error: %s", exc)
        raise
    except Exception as exc:
        logger.exception("Unexpected Anthropic error: %s", exc)
        return ""


def _stream_anthropic(
    messages: list[dict], model: str, temperature: float
) -> Iterator[str]:
    system, user_messages = _split_system(messages)
    payload: dict[str, Any] = {
        "model": model,
        "max_tokens": 4096,
        "temperature": temperature,
        "stream": True,
        "messages": user_messages,
    }
    if system:
        payload["system"] = system
    try:
        with httpx.stream(
            "POST", f"{_ANTHROPIC_BASE}/v1/messages",
            headers=_anthropic_headers(),
            json=payload,
            timeout=_STREAM_TIMEOUT,
        ) as resp:
            resp.raise_for_status()
            for line in resp.iter_lines():
                if not line or not line.startswith("data:"):
                    continue
                data_str = line[len("data:"):].strip()
                if data_str == "[DONE]":
                    break
                try:
                    event = json.loads(data_str)
                except json.JSONDecodeError:
                    continue
                if event.get("type") == "content_block_delta":
                    delta = event.get("delta", {})
                    if delta.get("type") == "text_delta":
                        token = delta.get("text", "")
                        if token:
                            yield token
    except httpx.TimeoutException:
        logger.warning("Anthropic stream timed out (model=%s)", model)
    except httpx.HTTPStatusError as exc:
        logger.warning("Anthropic stream HTTP error %s (model=%s)",
                       exc.response.status_code, model)
    except RuntimeError as exc:
        logger.error("Anthropic config error: %s", exc)
        raise
    except Exception as exc:
        logger.exception("Unexpected Anthropic stream error: %s", exc)


# ── Provider dispatch ────────────────────────────────────────────────

def _chat(messages: list[dict], model: str, temperature: float = 0.7) -> str:
    if CAS_PROVIDER == "anthropic":
        return _chat_anthropic(messages, model, temperature)
    return _chat_ollama(messages, model, temperature)


def stream_chat(
    messages: list[dict],
    model: str = "",
    temperature: float = 0.7,
) -> Iterator[str]:
    if not model:
        model = model_for("document")
    if CAS_PROVIDER == "anthropic":
        return _stream_anthropic(messages, model, temperature)
    return _stream_ollama(messages, model, temperature)


# ── Type-aware system prompts ────────────────────────────────────────

_WORKSPACE_SYSTEM = {
    "document": (
        "You are a document drafting assistant. "
        "Produce a well-structured markdown document with appropriate headings, "
        "sections, and placeholder text. "
        "Output only the document content — no preamble, no explanation, no code fences."
    ),
    "code": (
        "You are a coding assistant. "
        "Produce clean, well-commented code that fulfils the request. "
        "Output ONLY the raw code — no markdown fences, no explanation before or after. "
        "Start directly with the code. Add inline comments where useful."
    ),
    "list": (
        "You are a list-making assistant. "
        "Produce a clean, structured markdown list. "
        "Use a top-level heading, then bullet points or numbered items as appropriate. "
        "Output only the list content — no preamble, no explanation."
    ),
}

_EDIT_SYSTEM = {
    "document": (
        "You are a precise document editor. "
        "Apply the requested change to the document and return the complete updated content in markdown. "
        "Preserve all existing sections not affected by the change. "
        "Output only the updated document — no commentary."
    ),
    "code": (
        "You are a precise code editor. "
        "Apply the requested change to the code and return the complete updated code. "
        "Preserve all existing logic not affected by the change. "
        "Output only the updated code — no markdown fences, no explanation."
    ),
    "list": (
        "You are a precise list editor. "
        "Apply the requested change to the list and return the complete updated list in markdown. "
        "Preserve all existing items not affected by the change. "
        "Output only the updated list — no commentary."
    ),
}

_CHAT_SYSTEM = """You are CAS — a Conversational Agent Shell.

CAS lets users create and edit workspaces (documents, code files, lists) through conversation. Workspace creation and editing is handled automatically by the routing layer — you never create or modify workspaces yourself.

YOUR ROLE IN THIS CONTEXT:
This message reached you because it is a plain conversational message, not a workspace operation. Your job is to respond helpfully and briefly.

HARD RULES:
- Never simulate workspace creation. Do not say things like "workspace created", "I've saved that", "here's your document" — none of that is real when you say it. Only the routing layer creates workspaces.
- Never ask follow-up questions about workspace names, types, or where to save things. CAS handles that automatically.
- Never offer numbered options like "1. create workspace  2. view workspaces". That is not how CAS works.
- If the user seems to want a workspace, tell them to ask directly. Example: "To create a document, just say: write a [type of document]."
- Keep responses short. This is a shell, not a chatbot.
- Do not use markdown formatting in chat replies — plain text only.
- Do not ask clarifying questions unless absolutely necessary.
- If you genuinely cannot help with something, say so in one sentence.

WHAT YOU CAN DO:
- Answer questions about how CAS works
- Help the user phrase their request so the routing layer picks it up correctly
- Have brief factual conversations
- Explain what workspace types are available: document, code, list"""


def _ws_system(ws_type: str, user_context: str = "") -> str:
    base = _WORKSPACE_SYSTEM.get(ws_type, _WORKSPACE_SYSTEM["document"])
    return base + (f"\n\nUser context: {user_context}" if user_context else "")


def _edit_system(ws_type: str, user_context: str = "") -> str:
    base = _EDIT_SYSTEM.get(ws_type, _EDIT_SYSTEM["document"])
    return base + (f"\n\nUser context: {user_context}" if user_context else "")


def _chat_system(user_context: str = "") -> str:
    return _CHAT_SYSTEM + (f"\n\nUser context: {user_context}" if user_context else "")


# ── Public non-streaming API ─────────────────────────────────────────

def generate_workspace_content(
    title: str,
    user_message: str,
    ws_type: str = "document",
    user_context: str = "",
) -> str:
    model = model_for(ws_type)
    logger.info("generate_workspace_content: type=%s model=%s provider=%s",
                ws_type, model, CAS_PROVIDER)
    messages = [
        {"role": "system", "content": _ws_system(ws_type, user_context)},
        {"role": "user",   "content": f"Title: {title}\nRequest: {user_message}"},
    ]
    result = _chat(messages, model=model, temperature=0.6)
    if not result:
        return f"# {title}\n\n" if ws_type != "code" else ""
    if ws_type != "code" and not result.lstrip().startswith("#"):
        result = f"# {title}\n\n{result}"
    return result


def generate_workspace_edit(
    title: str,
    current_content: str,
    edit_request: str,
    ws_type: str = "document",
    user_context: str = "",
) -> str:
    model = model_for(ws_type)
    logger.info("generate_workspace_edit: type=%s model=%s provider=%s",
                ws_type, model, CAS_PROVIDER)
    messages = [
        {"role": "system", "content": _edit_system(ws_type, user_context)},
        {"role": "user",   "content": (
            f"Title: {title}\n\nCurrent content:\n{current_content}\n\n"
            f"Change request: {edit_request}"
        )},
    ]
    return _chat(messages, model=model, temperature=0.3)


def generate_chat_reply(
    message: str,
    history: list[dict[str, str]] | None = None,
    user_context: str = "",
) -> str:
    model = model_for("chat")
    messages: list[dict[str, str]] = [
        {"role": "system", "content": _chat_system(user_context)}
    ]
    if history:
        messages.extend(history[-6:])
    messages.append({"role": "user", "content": message})
    result = _chat(messages, model=model, temperature=0.7)
    return result or 'To create a workspace, say: "write a [document type]".'


# ── Message builders for streaming ───────────────────────────────────

def build_workspace_messages(
    title: str, user_message: str,
    ws_type: str = "document", user_context: str = "",
) -> list[dict[str, str]]:
    return [
        {"role": "system", "content": _ws_system(ws_type, user_context)},
        {"role": "user",   "content": f"Title: {title}\nRequest: {user_message}"},
    ]


def build_edit_messages(
    title: str, current_content: str, edit_request: str,
    ws_type: str = "document", user_context: str = "",
) -> list[dict[str, str]]:
    return [
        {"role": "system", "content": _edit_system(ws_type, user_context)},
        {"role": "user",   "content": (
            f"Title: {title}\n\nCurrent content:\n{current_content}\n\n"
            f"Change request: {edit_request}"
        )},
    ]


def build_chat_messages(
    message: str,
    history: list[dict[str, str]] | None = None,
    user_context: str = "",
) -> list[dict[str, str]]:
    messages: list[dict[str, str]] = [
        {"role": "system", "content": _chat_system(user_context)}
    ]
    if history:
        messages.extend(history[-6:])
    messages.append({"role": "user", "content": message})
    return messages
