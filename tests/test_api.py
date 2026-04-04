"""Tests for CAS API routes."""

import pytest
from fastapi import FastAPI
from fastapi.testclient import TestClient

from cas.api import create_router


@pytest.fixture
def client(shell):
    """TestClient wired to an isolated in-memory shell."""
    app = FastAPI()
    app.include_router(create_router(shell))
    return TestClient(app)


@pytest.fixture
def client_with_workspace(client):
    """Client that already has one active workspace."""
    r = client.post("/api/cas/message", json={"message": "write a project proposal"})
    ws = r.json()["workspace"]
    sid = r.json()["session_id"]
    return client, sid, ws
