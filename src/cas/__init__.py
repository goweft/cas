"""CAS — Conversational Agent Shell.

A session-layer interface where persistent natural-language conversation
dynamically generates interactive workspaces. Built on Heddle's MCP mesh
runtime with deterministic agent contracts for security enforcement.

Modules:
    contracts   — Deterministic precondition/postcondition/invariant enforcement
    workspaces  — Workspace types, lifecycle, and state management
    shell       — Chat session manager and intent routing
    renderer    — HTML workspace generation
    conductor   — Behavioral learning and proactive orchestration (Phase 2+)
"""

__version__ = "0.1.0-dev"
