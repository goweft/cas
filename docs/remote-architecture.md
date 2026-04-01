# CAS Remote Access Architecture

**Status:** Phase 1 complete — Protocols, LocalExecutionContext, InMemoryStore shipped  
**Date:** 2026-03-31  
**Scope:** Making CAS location-independent across session, execution, and transport layers

---

## Problem Statement

CAS currently assumes co-location: the shell, the AI backend, the filesystem, and the user all live on the same machine. This limits CAS to a single-seat local tool. The goal is to break that assumption cleanly so that "remote" becomes a property of configuration, not a separate code path.

This is not about exposing a port. It is about decoupling three things that are currently fused:

1. **Where the session lives** (state, conversation history, workspace metadata)
2. **Where work happens** (file creation, editing, tool invocation)
3. **Where inference runs** (Ollama, Anthropic, Groq)

Inference routing is already partially addressed by `model_for()` and the planned multi-backend work. This document focuses on the first two, which are prerequisites for meaningful remote access.

---

## Design Principles

- **No separate remote code path.** Local and remote use the same abstractions. The difference is configuration, not branching logic.
- **Contracts become load-bearing.** Locally, the contract layer provides safety guarantees. Remotely, it provides security boundaries. The same mechanism serves both purposes.
- **Progressive trust.** A local session has full filesystem authority. A tunneled session has scoped authority. A public session has only what contracts explicitly permit.
- **Session portability over session replication.** The goal is to reconnect to persistent state, not sync state across devices.

---

## Layer 1: Session Persistence ✅

### Delivered

- **`SessionStore` Protocol** (`protocols.py`) — runtime-checkable interface formalising CASStore's existing API. Any backend that implements 12 methods is a drop-in replacement.
- **`InMemoryStore`** (`memory_store.py`) — dict-backed implementation proving the Protocol is swappable. Used as the default test store (replaces MagicMock).
- **`CASStore`** confirmed as SessionStore-conformant via `isinstance` check.
- **Shell reconstruction test** — building a new Shell on the same store restores workspaces and sessions, simulating process restart with state preserved.

### What gets serialized (via CASStore/SQLite)

- Session ID and creation timestamp
- Conversation history (messages, not inference state)
- Workspace manifest (which workspaces exist, their metadata and content)
- Active workspace reference (via closed_at IS NULL)

### What does not get serialized

- Ollama connection state (re-establish on reconnect)
- In-flight inference (non-recoverable by design)
- Transient UI state (owned by the frontend)
- Execution context binding (re-established at session load time)

---

## Layer 2: Execution Context ✅

### Delivered

- **`ExecutionContext` Protocol** (`protocols.py`) — abstraction over where file/command operations happen. Methods: `read_file`, `write_file`, `list_dir`, `exists`, `delete`, `mkdir`, `execute`.
- **`LocalExecutionContext`** (`execution.py`) — pathlib-backed implementation with full scope enforcement:
  - Path traversal → `ContractViolation`
  - Excluded patterns (`.env`, `.ssh/*`, `*.key`) → `ContractViolation`
  - Disallowed operations → `ContractViolation`
  - Oversized writes → `ContractViolation`
  - Command execution with 30s timeout
- **`SessionScope`** dataclass with `.permissive()` and `.restrictive()` presets.
- **Session binding** — each Session carries an `execution_context` field. Different sessions can point at different backends (local, SSH, container).
- **Shell wiring** — Shell accepts optional `execution_context`, creates `LocalExecutionContext` as default, binds it to new sessions.

### Scope Model

```
SessionScope:
  workspace_root: str             # where this session can operate
  allowed_operations: set[str]    # read, write, execute, delete
  max_file_size: int              # prevent abuse
  excluded_patterns: list[str]    # e.g., "*.env", ".ssh/*"
```

Local sessions → `SessionScope.permissive()`. Remote sessions → `SessionScope.restrictive()`.

### Future Implementations

| Context | How | When |
|---------|-----|------|
| SSH-tunneled | `asyncssh` or paramiko to a remote host | Phase 2 |
| Containerized | Docker/Podman exec into a sandboxed workspace | If needed |

---

## Layer 3: Transport and Auth (Not Yet Started)

### Private Access (Phase 2)

**Tailscale mesh** — lowest-friction option:
- weftbox joins a Tailscale network
- CAS binds to `0.0.0.0:8301` but is only reachable via Tailscale IPs
- No TLS certificate management (Tailscale handles encryption)
- No auth layer needed if the Tailscale network is single-user

Zero code changes to CAS. Purely network configuration.

### Wider Access (Phase 3+)

- Reverse proxy (Caddy) with automatic TLS
- Token-based authentication scoped to sessions
- Per-token rate limiting
- Contract layer enforces session scope boundaries

---

## How the Layers Compose

A concrete scenario: Steve on a laptop at a coffee shop, weftbox at home.

1. **Transport:** Laptop connects to weftbox via Tailscale.
2. **Session:** CAS loads persisted session from SQLite. History and workspaces restored.
3. **Execution:** Session's execution context is `Local` (ops happen on weftbox's filesystem).
4. **Inference:** `model_for()` routes to Anthropic API (Ollama cold-start over remote link is painful) or to Ollama if already loaded.

Nothing in Shell or workspace code knows this is remote. The abstraction boundaries handle it.

---

## Phasing

| Phase | What | Status |
|-------|------|--------|
| **1a** | `SessionStore` protocol + InMemoryStore impl | ✅ Shipped |
| **1b** | `ExecutionContext` protocol + LocalExecutionContext | ✅ Shipped |
| **1c** | Wire Session/Shell to use ExecutionContext | ✅ Shipped |
| **1d** | Replace MagicMock store with InMemoryStore in tests | ✅ Shipped |
| **2a** | Multi-backend AI routing (Anthropic, Groq, Ollama) | Pending |
| **2b** | Tailscale deployment | Pending (infra) |
| **2c** | SSH execution context | Pending |
| **3a** | Token-based auth middleware | Pending |
| **3b** | Reverse proxy + TLS | Pending |
| **3c** | Containerized execution context | If needed |

---

## Test Coverage

413 total tests across 14 test files. Key new test files:

- `test_execution.py` (40 tests) — file CRUD, directory ops, command execution, traversal blocking, excluded patterns, allowed operations, file size limits, scope presets, protocol conformance
- `test_protocols.py` (13 tests) — SessionStore conformance for both CASStore and InMemoryStore, workspace lifecycle, history, undo
- `test_session_context.py` (8 tests) — Session/Shell execution context binding, custom contexts, per-session isolation
- `test_store_integration.py` (7 tests) — end-to-end Shell→Store flow, session persistence, message isolation, Shell reconstruction

---

## Risks and Open Questions

- **Latency budget:** Inference over a remote link adds round-trip time. Measure with Tailscale before optimising.
- **Conflict model:** Two frontends on same session? Single-writer for now. Flag for later.
- **Workspace portability:** If execution context changes, workspace paths need remapping. Roots should be relative.
- **Offline resilience:** Remote AI unreachable → fail explicitly, not silently degrade.
- **Contract scope granularity:** Per-session scope sufficient? Start there; refine if too coarse.
