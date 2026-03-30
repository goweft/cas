#!/usr/bin/env python3 -c pass
"""CAS renderer — converts workspace content to styled HTML.

Public functions:
    render(markdown_text, ws_type="document") -> str
        Type-aware render dispatcher. Returns a <div class="cas-doc"> fragment.

    render_with_styles(markdown_text, ws_type="document") -> str
        Same but with embedded CSS — for standalone HTML export.

    WORKSPACE_CSS   — the CSS string to inject once into the page
"""

from __future__ import annotations

import html as html_module
import re
import textwrap

import markdown

_EXTENSIONS = ["fenced_code", "tables", "nl2br", "attr_list"]
_MD = markdown.Markdown(extensions=_EXTENSIONS)


# ── Shared CSS ───────────────────────────────────────────────────────

WORKSPACE_CSS = textwrap.dedent("""
    .cas-doc {
        font-family: Georgia, "Times New Roman", serif;
        font-size: 14px;
        line-height: 1.75;
        color: #c8c8c8;
        max-width: 720px;
        padding: 24px 32px;
    }

    .cas-doc h1 {
        font-size: 1.6em; font-weight: 700;
        border-bottom: 1px solid #2a2a2a; padding-bottom: 6px;
        margin-bottom: 16px; color: #e8e8e8;
        font-family: "SF Mono", "Cascadia Code", "Fira Code", monospace;
        letter-spacing: -0.02em;
    }
    .cas-doc h2 {
        font-size: 1.15em; font-weight: 700; margin-top: 24px;
        margin-bottom: 8px; color: #ddd;
        font-family: "SF Mono", "Cascadia Code", "Fira Code", monospace;
    }
    .cas-doc h3 {
        font-size: 1em; font-weight: 700; margin-top: 16px;
        margin-bottom: 6px; color: #bbb;
        font-family: "SF Mono", "Cascadia Code", "Fira Code", monospace;
    }
    .cas-doc p  { margin: 0 0 12px 0; }
    .cas-doc ul, .cas-doc ol { margin: 0 0 12px 1.5em; padding: 0; }
    .cas-doc li { margin-bottom: 4px; }
    .cas-doc li > ul, .cas-doc li > ol { margin-top: 4px; margin-bottom: 4px; }
    .cas-doc strong { color: #e0e0e0; font-weight: 700; }
    .cas-doc em { color: #b0b8c8; font-style: italic; }

    .cas-doc code {
        font-family: "SF Mono", "Cascadia Code", "Fira Code", monospace;
        font-size: 0.85em; background: #1e1e1e;
        border: 1px solid #2a2a2a; border-radius: 3px;
        padding: 1px 5px; color: #4a9eff;
    }
    .cas-doc pre {
        background: #141414; border: 1px solid #2a2a2a;
        border-radius: 4px; padding: 12px 16px;
        overflow-x: auto; margin: 0 0 14px 0;
    }
    .cas-doc pre code {
        background: none; border: none; padding: 0;
        color: #c8c8c8; font-size: 0.88em;
    }
    .cas-doc blockquote {
        border-left: 3px solid #2a2a2a; margin: 0 0 12px 0;
        padding: 4px 16px; color: #888;
    }
    .cas-doc table { border-collapse: collapse; width: 100%; margin-bottom: 14px; font-size: 0.9em; }
    .cas-doc th {
        text-align: left; border-bottom: 2px solid #2a2a2a;
        padding: 6px 12px; color: #e0e0e0;
        font-family: "SF Mono", "Cascadia Code", "Fira Code", monospace;
        font-size: 0.85em; text-transform: uppercase; letter-spacing: 0.05em;
    }
    .cas-doc td { padding: 5px 12px; border-bottom: 1px solid #1e1e1e; vertical-align: top; }
    .cas-doc tr:hover td { background: #161616; }
    .cas-doc hr { border: none; border-top: 1px solid #2a2a2a; margin: 20px 0; }
    .cas-doc a { color: #4a9eff; text-decoration: none; }
    .cas-doc a:hover { text-decoration: underline; }

    /* Code workspace */
    .cas-code {
        font-family: "SF Mono", "Cascadia Code", "Fira Code", monospace;
        font-size: 13px; line-height: 1.6; color: #c8c8c8;
        padding: 24px 32px; max-width: none;
    }
    .cas-code pre {
        background: transparent; border: none; padding: 0;
        margin: 0; overflow-x: auto; white-space: pre;
    }
    .cas-code code { background: none; border: none; padding: 0; color: #c8c8c8; }

    /* List workspace */
    .cas-list {
        font-family: Georgia, "Times New Roman", serif;
        font-size: 14px; line-height: 1.8; color: #c8c8c8;
        max-width: 640px; padding: 24px 32px;
    }
    .cas-list h1 {
        font-size: 1.4em; font-weight: 700; margin-bottom: 20px;
        color: #e8e8e8; border-bottom: 1px solid #2a2a2a; padding-bottom: 6px;
        font-family: "SF Mono", "Cascadia Code", "Fira Code", monospace;
    }
    .cas-list ul, .cas-list ol { padding-left: 1.4em; margin: 0; }
    .cas-list li {
        margin-bottom: 8px; padding-left: 4px;
        border-bottom: 1px solid #1a1a1a; padding-bottom: 6px;
    }
    .cas-list li:last-child { border-bottom: none; }
    .cas-list li input[type=checkbox] { margin-right: 8px; accent-color: #4a9eff; }
    .cas-list strong { color: #e0e0e0; }
""").strip()


# ── Renderers ────────────────────────────────────────────────────────

def _render_document(text: str) -> str:
    _MD.reset()
    body = _MD.convert(text or "")
    return f'<div class="cas-doc">{body}</div>'


def _render_code(text: str) -> str:
    """Render raw code as a monospace pre block with HTML escaping."""
    escaped = html_module.escape(text or "")
    return f'<div class="cas-code"><pre><code>{escaped}</code></pre></div>'


def _render_list(text: str) -> str:
    """Render a markdown list with the list-specific CSS class."""
    _MD.reset()
    body = _MD.convert(text or "")
    return f'<div class="cas-list">{body}</div>'


# ── Public API ───────────────────────────────────────────────────────

def render(markdown_text: str, ws_type: str = "document") -> str:
    """Render workspace content to an HTML fragment.

    ws_type controls which CSS class and rendering strategy is used:
      "document" — rich markdown with serif font
      "code"     — monospace pre block, no markdown processing
      "list"     — markdown list with checklist-friendly styling
    """
    if ws_type == "code":
        return _render_code(markdown_text)
    elif ws_type == "list":
        return _render_list(markdown_text)
    else:
        return _render_document(markdown_text)


def render_with_styles(markdown_text: str, ws_type: str = "document") -> str:
    """Render and embed CSS — for standalone HTML export."""
    content = render(markdown_text, ws_type)
    return f"<style>\n{WORKSPACE_CSS}\n</style>\n{content}"
