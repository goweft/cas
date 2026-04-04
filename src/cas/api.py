"""CAS API router — FastAPI routes for workspace and session management.

The router is stateless: all session and workspace state lives in a Shell
instance injected via create_router(). This makes the module testable and
avoids module-global mutable state.

Usage:
    # Standalone (dev_server.py)
    router = create_router()

    # With an explicit shell (tests, or Heddle mount)
    shell = Shell(store=InMemoryStore())
    router = create_router(shell=shell)
"""

from __future__ import annotations

import json
import re
from pathlib import Path

from fastapi import APIRouter, HTTPException
from fastapi.responses import FileResponse, HTMLResponse, Response, StreamingResponse
from pydantic import BaseModel

from cas.audit import get_cas_auditor
from cas.llm import (
    model_for,
    build_chat_messages,
    build_edit_messages,
    build_workspace_messages,
    stream_chat,
)
from cas.renderer import WORKSPACE_CSS, render, render_with_styles
from cas.shell import Shell, detect_intent
from cas.contracts import load_contract_from_config
from cas.workspaces import WorkspaceClosed, WorkspaceNotFound

_STATIC_DIR = Path(__file__).parent / "static"


class MessageRequest(BaseModel):
    session_id: str | None = None
    message: str


class WorkspaceUpdateRequest(BaseModel):
    title: str | None = None
    content: str | None = None


def _safe_filename(title: str) -> str:
    slug = re.sub(r"[^\w\s-]", "", title.lower())
    slug = re.sub(r"[\s_-]+", "-", slug).strip("-")
    return slug or "document"


_CODE_EXTENSIONS = {"py", "js", "ts", "sh", "rb", "go", "rs", "java", "c", "cpp", "sql"}


def _code_extension(title: str) -> str:
    words = title.lower().split()
    for word in words:
        if word in _CODE_EXTENSIONS:
            return word
    return "py"


def create_router(shell: Shell | None = None) -> APIRouter:
    """Return a fully-configured APIRouter bound to *shell*.

    If shell is None a default Shell() is created (uses CASStore + SQLite).
    Pass an explicit shell for tests or alternative backends.
    """
    _shell = shell if shell is not None else Shell()
    router = APIRouter(prefix="/api/cas", tags=["cas"])

    def _resolve_session(session_id: str | None):
        if session_id is None:
            session = _shell.create_session()
            return session.id, session
        try:
            return session_id, _shell.get_session(session_id)
        except KeyError:
            raise HTTPException(status_code=404, detail="Session not found")

    @router.get("/session/latest")
    def get_latest_session():
        sessions = _shell._sessions
        if not sessions:
            return {"session_id": None, "messages": []}
        latest = max(sessions.values(), key=lambda s: s.created_at)
        return {
            "session_id": latest.id,
            "messages": [{"role": m.role, "text": m.text} for m in latest.history],
        }

    @router.post("/session")
    def new_session():
        session = _shell.create_session()
        return {"session_id": session.id, "messages": []}

    @router.post("/message")
    def send_message(req: MessageRequest):
        session_id, _ = _resolve_session(req.session_id)
        response = _shell.process_message(session_id, req.message)
        return {"session_id": session_id, **response.to_dict()}

    def _sse(event: str, data: str) -> str:
        return f"event: {event}\ndata: {data}\n\n"

    @router.post("/message/stream")
    def send_message_stream(req: MessageRequest):
        """Stream via SSE: session → intent → token×N → workspace? → chat_reply → done."""
        session_id, session = _resolve_session(req.session_id)
        message = req.message
        intent = detect_intent(message)
        user_context = _shell._conductor.user_context()
        auditor = get_cas_auditor()

        def _save_msg(role: str, text: str):
            msg = session.add_message(role, text)
            _shell._store.save_message(session_id, msg)

        def generate():
            yield _sse("session", json.dumps({"session_id": session_id}))
            yield _sse("intent", json.dumps({
                "kind": intent.kind,
                "ws_type": intent.ws_type,
                "title": intent.title_hint or "Untitled",
            }))

            if intent.kind == "create_workspace":
                ws_type = intent.ws_type
                title = intent.title_hint or "Untitled"
                msgs = build_workspace_messages(title, message, ws_type=ws_type, user_context=user_context)
                contract = load_contract_from_config("cas-workspace", _shell._contract_config)

                accumulated = ""
                for token in stream_chat(msgs, model=model_for(ws_type), temperature=0.6):
                    accumulated += token
                    yield _sse("token", json.dumps({"token": token}))

                content = accumulated.strip()
                if not content:
                    content = f"# {title}\n\n" if ws_type != "code" else ""
                elif ws_type != "code" and not content.lstrip().startswith("#"):
                    content = f"# {title}\n\n{content}"

                ws = _shell.workspaces.create(
                    title, content, contract,
                    workspace_type=ws_type, session_id=session_id,
                )
                auditor.log_workspace_create(ws, session_id)
                _shell._conductor.observe(intent.kind, message, workspace_title=ws.title, ws_type=ws.type)

                reply = f'Created {ws_type} workspace "{ws.title}". You can edit it directly or ask me to make changes.'
                _save_msg("user", message)
                _save_msg("shell", reply)
                yield _sse("workspace", json.dumps(ws.to_dict()))
                yield _sse("chat_reply", json.dumps({"text": reply, "intent": intent.kind}))

            elif intent.kind == "edit_workspace":
                active = _shell.workspaces.list_active()
                if not active:
                    reply = "No active workspace to edit. Ask me to create one first."
                    _save_msg("user", message)
                    _save_msg("shell", reply)
                    yield _sse("chat_reply", json.dumps({"text": reply, "intent": intent.kind}))
                else:
                    ws = active[-1]
                    msgs = build_edit_messages(
                        ws.title, ws.content, message,
                        ws_type=ws.type, user_context=user_context,
                    )
                    accumulated = ""
                    for token in stream_chat(msgs, model=model_for(ws.type), temperature=0.3):
                        accumulated += token
                        yield _sse("token", json.dumps({"token": token}))

                    new_content = accumulated.strip() or ws.content + f"\n\n{message}\n"
                    _shell.workspaces.update(ws.id, content=new_content)
                    ws = _shell.workspaces.get(ws.id)
                    auditor.log_workspace_update(ws, session_id, message)
                    _shell._conductor.observe(intent.kind, message, workspace_title=ws.title, ws_type=ws.type)

                    reply = f'Updated workspace "{ws.title}".'
                    _save_msg("user", message)
                    _save_msg("shell", reply)
                    yield _sse("workspace", json.dumps(ws.to_dict()))
                    yield _sse("chat_reply", json.dumps({"text": reply, "intent": intent.kind}))

            else:
                history = [
                    {"role": "user" if m.role == "user" else "assistant", "content": m.text}
                    for m in session.history[-6:]
                ]
                msgs = build_chat_messages(message, history, user_context)
                accumulated = ""
                for token in stream_chat(msgs, model=model_for("chat"), temperature=0.7):
                    accumulated += token
                    yield _sse("token", json.dumps({"token": token}))

                reply = accumulated.strip() or "I can help you create documents, code, and lists."
                _save_msg("user", message)
                _save_msg("shell", reply)
                _shell._conductor.observe(intent.kind, message)
                yield _sse("chat_reply", json.dumps({"text": reply, "intent": intent.kind}))

            yield _sse("done", "{}")

        return StreamingResponse(
            generate(),
            media_type="text/event-stream",
            headers={"Cache-Control": "no-cache", "X-Accel-Buffering": "no"},
        )

    @router.get("/workspaces")
    def list_workspaces():
        return [ws.to_dict() for ws in _shell.workspaces.list_active()]

    @router.get("/workspace/{workspace_id}")
    def get_workspace(workspace_id: str):
        try:
            return _shell.workspaces.get(workspace_id).to_dict()
        except WorkspaceNotFound:
            raise HTTPException(status_code=404, detail="Workspace not found")

    @router.put("/workspace/{workspace_id}")
    def update_workspace(workspace_id: str, req: WorkspaceUpdateRequest):
        try:
            ws = _shell.workspaces.update(workspace_id, title=req.title, content=req.content)
        except WorkspaceNotFound:
            raise HTTPException(status_code=404, detail="Workspace not found")
        except WorkspaceClosed:
            raise HTTPException(status_code=409, detail="Workspace is closed")
        return ws.to_dict()

    @router.delete("/workspace/{workspace_id}")
    def close_workspace(workspace_id: str):
        try:
            ws = _shell.workspaces.close(workspace_id)
        except WorkspaceNotFound:
            raise HTTPException(status_code=404, detail="Workspace not found")
        return ws.to_dict()

    @router.get("/workspace/{workspace_id}/history")
    def get_workspace_history(workspace_id: str):
        try:
            _shell.workspaces.get(workspace_id)
        except WorkspaceNotFound:
            raise HTTPException(status_code=404, detail="Workspace not found")
        history = _shell._store.load_history(workspace_id)
        return [
            {"version": h["version"], "title": h["title"],
             "saved_at": h["saved_at"], "content_length": len(h["content"])}
            for h in history
        ]

    @router.get("/workspace/{workspace_id}/history/{version}")
    def get_workspace_version(workspace_id: str, version: int):
        try:
            _shell.workspaces.get(workspace_id)
        except WorkspaceNotFound:
            raise HTTPException(status_code=404, detail="Workspace not found")
        entry = _shell._store.get_version(workspace_id, version)
        if not entry:
            raise HTTPException(status_code=404, detail=f"Version {version} not found")
        return entry

    @router.post("/workspace/{workspace_id}/undo")
    def undo_workspace(workspace_id: str):
        try:
            ws = _shell.workspaces.get(workspace_id)
        except WorkspaceNotFound:
            raise HTTPException(status_code=404, detail="Workspace not found")
        if not ws.is_active:
            raise HTTPException(status_code=409, detail="Workspace is closed")
        restored = _shell._store.undo(workspace_id)
        if restored is None:
            raise HTTPException(status_code=404, detail="No history to undo")
        _shell.workspaces.update(workspace_id, title=restored["title"],
                                 content=restored["content"], _skip_store=True)
        return _shell.workspaces.get(workspace_id).to_dict()

    @router.post("/workspace/{workspace_id}/restore/{version}")
    def restore_workspace_version(workspace_id: str, version: int):
        try:
            ws = _shell.workspaces.get(workspace_id)
        except WorkspaceNotFound:
            raise HTTPException(status_code=404, detail="Workspace not found")
        if not ws.is_active:
            raise HTTPException(status_code=409, detail="Workspace is closed")
        success = _shell._store.apply_version(workspace_id, version)
        if not success:
            raise HTTPException(status_code=404, detail=f"Version {version} not found")
        current = _shell._store.load_workspaces(lambda: ws.contract)
        if workspace_id in current:
            row = current[workspace_id]
            _shell.workspaces.update(workspace_id, title=row["title"],
                                     content=row["content"], _skip_store=True)
        return _shell.workspaces.get(workspace_id).to_dict()

    @router.get("/workspace/{workspace_id}/render", response_class=HTMLResponse)
    def render_workspace(workspace_id: str):
        try:
            ws = _shell.workspaces.get(workspace_id)
        except WorkspaceNotFound:
            raise HTTPException(status_code=404, detail="Workspace not found")
        return HTMLResponse(content=render(ws.content, ws_type=ws.type))

    @router.get("/styles/workspace.css")
    def workspace_css():
        return Response(content=WORKSPACE_CSS, media_type="text/css")

    @router.get("/workspace/{workspace_id}/export/{fmt}")
    def export_workspace(workspace_id: str, fmt: str):
        if fmt not in ("md", "html", "txt"):
            raise HTTPException(status_code=400, detail="Format must be md, html, or txt")
        try:
            ws = _shell.workspaces.get(workspace_id)
        except WorkspaceNotFound:
            raise HTTPException(status_code=404, detail="Workspace not found")

        filename = _safe_filename(ws.title)

        if fmt == "md":
            if ws.type == "code":
                ext = _code_extension(ws.title)
                body, media_type, dl_name = ws.content.encode(), "text/plain", f"{filename}.{ext}"
            else:
                body, media_type, dl_name = ws.content.encode(), "text/markdown", f"{filename}.md"
        elif fmt == "html":
            body = render_with_styles(ws.content, ws_type=ws.type).encode()
            media_type, dl_name = "text/html", f"{filename}.html"
        else:
            plain = re.sub(r"^#{1,6}\s+", "", ws.content, flags=re.MULTILINE)
            plain = re.sub(r"\*\*(.+?)\*\*", r"\1", plain)
            plain = re.sub(r"\*(.+?)\*", r"\1", plain)
            body, media_type, dl_name = plain.encode(), "text/plain", f"{filename}.txt"

        try:
            get_cas_auditor().log_workspace_export(ws, session_id="api", fmt=fmt)
        except Exception:
            pass

        return Response(content=body, media_type=media_type,
                        headers={"Content-Disposition": f'attachment; filename="{dl_name}"'})

    @router.get("/profile")
    def get_profile():
        return _shell._conductor.profile_summary()

    @router.delete("/profile")
    def reset_profile():
        _shell._conductor.reset()
        return {"reset": True}

    @router.get("/", include_in_schema=False)
    def serve_ui():
        return FileResponse(_STATIC_DIR / "index.html", media_type="text/html")

    return router
