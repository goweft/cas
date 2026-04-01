"""CAS — Conversational Agent Shell.

A session-layer interface where persistent natural-language conversation
dynamically generates interactive workspaces. Built on Heddle's MCP mesh
runtime with deterministic agent contracts for security enforcement.

Modules:
    contracts     — Deterministic precondition/postcondition/invariant enforcement
    protocols     — SessionStore, ExecutionContext, SessionScope interfaces
    execution     — LocalExecutionContext (pathlib-backed file/command operations)
    memory_store  — InMemoryStore (dict-backed SessionStore for tests/embedded use)
    workspaces    — Workspace types, lifecycle, and state management
    shell         — Chat session manager and intent routing
    store         — SQLite-backed persistence (CASStore)
    renderer      — HTML workspace generation
    conductor     — Behavioral learning and user context generation
"""

__version__ = "0.1.0-dev"
