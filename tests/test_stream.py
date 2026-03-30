"""Tests for the streaming SSE endpoint."""
import json
import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from cas.api import create_router
from cas.shell import Shell


@pytest.fixture
def client():
    shell = Shell()
    app = FastAPI()
    app.include_router(create_router(shell))
    return TestClient(app)


def _parse_sse(raw: str) -> list[dict]:
    """Parse raw SSE text into list of {event, data} dicts."""
    events = []
    for block in raw.strip().split("\n\n"):
        lines = block.strip().split("\n")
        event = data = None
        for line in lines:
            if line.startswith("event: "): event = line[7:]
            elif line.startswith("data: "): data = line[6:]
        if event and data:
            try:
                events.append({"event": event, "data": json.loads(data)})
            except json.JSONDecodeError:
                events.append({"event": event, "data": data})
    return events


class TestStreamEndpoint:
    def test_stream_returns_event_stream(self, client):
        r = client.post("/api/cas/message/stream", json={"message": "hello"})
        assert r.status_code == 200
        assert "text/event-stream" in r.headers["content-type"]

    def test_stream_chat_event_sequence(self, client):
        r = client.post("/api/cas/message/stream", json={"message": "hello"})
        events = _parse_sse(r.text)
        event_names = [e["event"] for e in events]

        # session and intent arrive before any tokens
        assert event_names[0] == "session"
        assert event_names[1] == "intent"
        assert "chat_reply" in event_names
        assert event_names[-1] == "done"

    def test_stream_intent_event_before_tokens(self, client):
        """The intent event must arrive before any token events."""
        r = client.post("/api/cas/message/stream", json={"message": "hello"})
        events = _parse_sse(r.text)
        event_names = [e["event"] for e in events]

        intent_idx = event_names.index("intent")
        first_token_idx = event_names.index("token") if "token" in event_names else len(event_names)
        assert intent_idx < first_token_idx

    def test_stream_intent_event_has_kind(self, client):
        r = client.post("/api/cas/message/stream", json={"message": "hello"})
        events = _parse_sse(r.text)
        intent = next(e for e in events if e["event"] == "intent")
        assert "kind" in intent["data"]
        assert intent["data"]["kind"] == "chat"

    def test_stream_intent_event_has_ws_type(self, client):
        r = client.post("/api/cas/message/stream", json={"message": "hello"})
        events = _parse_sse(r.text)
        intent = next(e for e in events if e["event"] == "intent")
        assert "ws_type" in intent["data"]

    def test_stream_workspace_intent_before_tokens(self, client):
        """For workspace creation, intent arrives before all tokens."""
        r = client.post("/api/cas/message/stream", json={"message": "write a project proposal"})
        events = _parse_sse(r.text)
        event_names = [e["event"] for e in events]

        intent_idx = event_names.index("intent")
        first_token_idx = event_names.index("token")
        assert intent_idx < first_token_idx, "intent must precede first token"

    def test_stream_workspace_intent_kind(self, client):
        r = client.post("/api/cas/message/stream", json={"message": "write a project proposal"})
        events = _parse_sse(r.text)
        intent = next(e for e in events if e["event"] == "intent")
        assert intent["data"]["kind"] == "create_workspace"
        assert intent["data"]["ws_type"] == "document"

    def test_stream_code_intent_ws_type(self, client):
        r = client.post("/api/cas/message/stream", json={"message": "write a python script to sort"})
        events = _parse_sse(r.text)
        intent = next(e for e in events if e["event"] == "intent")
        assert intent["data"]["kind"] == "create_workspace"
        assert intent["data"]["ws_type"] == "code"

    def test_stream_session_event_has_id(self, client):
        r = client.post("/api/cas/message/stream", json={"message": "hello"})
        events = _parse_sse(r.text)
        session_event = next(e for e in events if e["event"] == "session")
        assert "session_id" in session_event["data"]
        assert len(session_event["data"]["session_id"]) == 12

    def test_stream_token_events_present_for_chat(self, client):
        r = client.post("/api/cas/message/stream", json={"message": "hello"})
        events = _parse_sse(r.text)
        tokens = [e for e in events if e["event"] == "token"]
        assert len(tokens) > 0
        for t in tokens:
            assert "token" in t["data"]

    def test_stream_chat_reply_matches_tokens(self, client):
        r = client.post("/api/cas/message/stream", json={"message": "hello"})
        events = _parse_sse(r.text)
        tokens = "".join(e["data"]["token"] for e in events if e["event"] == "token")
        reply_event = next(e for e in events if e["event"] == "chat_reply")
        assert reply_event["data"]["text"] == tokens

    def test_stream_workspace_create_sequence(self, client):
        r = client.post("/api/cas/message/stream", json={"message": "write a project proposal"})
        events = _parse_sse(r.text)
        event_names = [e["event"] for e in events]

        assert event_names[0] == "session"
        assert event_names[1] == "intent"
        assert "token" in event_names
        assert "workspace" in event_names
        assert "chat_reply" in event_names
        assert event_names[-1] == "done"

    def test_stream_workspace_event_has_full_ws(self, client):
        r = client.post("/api/cas/message/stream", json={"message": "write a proposal"})
        events = _parse_sse(r.text)
        ws_event = next(e for e in events if e["event"] == "workspace")
        ws = ws_event["data"]
        assert "id" in ws
        assert "title" in ws
        assert "content" in ws
        assert ws["is_active"] is True

    def test_stream_workspace_content_has_heading(self, client):
        r = client.post("/api/cas/message/stream", json={"message": "write a proposal"})
        events = _parse_sse(r.text)
        ws_event = next(e for e in events if e["event"] == "workspace")
        assert ws_event["data"]["content"].startswith("#")

    def test_stream_workspace_registered_after_stream(self, client):
        r = client.post("/api/cas/message/stream", json={"message": "write a proposal"})
        events = _parse_sse(r.text)
        ws_id = next(e for e in events if e["event"] == "workspace")["data"]["id"]
        r2 = client.get(f"/api/cas/workspace/{ws_id}")
        assert r2.status_code == 200
        assert r2.json()["id"] == ws_id

    def test_stream_reuses_session(self, client):
        r1 = client.post("/api/cas/message/stream", json={"message": "hello"})
        sid = next(e for e in _parse_sse(r1.text) if e["event"] == "session")["data"]["session_id"]
        r2 = client.post("/api/cas/message/stream", json={"session_id": sid, "message": "hello again"})
        events2 = _parse_sse(r2.text)
        sid2 = next(e for e in events2 if e["event"] == "session")["data"]["session_id"]
        assert sid == sid2

    def test_stream_invalid_session_returns_404(self, client):
        r = client.post("/api/cas/message/stream",
                        json={"session_id": "nonexistent", "message": "hello"})
        assert r.status_code == 404

    def test_stream_intent_in_chat_reply(self, client):
        r = client.post("/api/cas/message/stream", json={"message": "hello"})
        events = _parse_sse(r.text)
        reply = next(e for e in events if e["event"] == "chat_reply")
        assert reply["data"]["intent"] == "chat"

    def test_stream_workspace_intent_in_chat_reply(self, client):
        r = client.post("/api/cas/message/stream", json={"message": "write a resume"})
        events = _parse_sse(r.text)
        reply = next(e for e in events if e["event"] == "chat_reply")
        assert reply["data"]["intent"] == "create_workspace"

    def test_stream_done_is_last_event(self, client):
        r = client.post("/api/cas/message/stream", json={"message": "hello"})
        events = _parse_sse(r.text)
        assert events[-1]["event"] == "done"
