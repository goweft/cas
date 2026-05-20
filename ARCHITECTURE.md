# CAS Architecture

**Last updated:** 2026-05-19
**Verified against:** commit `0ab8230`

---

## What CAS is

CAS is a terminal shell where conversation and direct manipulation coexist in
the same interface. The left panel is a persistent chat; the right panel is a
live workspace (document, code, or list) that tokens stream into as the model
generates. Once generated, the workspace is yours to edit directly.

The design resolves a split in HCI between agent delegation and direct
manipulation: agents generate, users manipulate.

---

## Binary layout

```
cmd/cas/main.go          Entry point. Wires store → shell → TUI and starts
                         the Bubble Tea event loop.

internal/
  intent/     detect.go  Zero-latency regex classifier. Fires before any LLM
                         call. Maps a user message to a Kind and workspace type.
  contract/   contract.go  Design by Contract enforcement. Pre/post/invariant
                         checks on workspace operations. Fail-closed.
  workspace/  workspace.go  Workspace type and lifecycle (create, update, close,
                         history/undo). Three types: document, code, list.
  shell/      shell.go   Session coordinator. Wires intent → workspace →
                         LLM → store → conductor.
              resolve.go Cross-workspace fuzzy title resolution for combine
                         and context-aware edit.
  llm/        llm.go     Multi-provider LLM bridge. Streaming and non-streaming.
                         Provider: CAS_PROVIDER env (ollama | anthropic).
  store/      store.go   Store interface (SessionStore).
              sqlite.go  SQLiteStore — production persistence at ~/.cas/cas.db.
              memory.go  MemoryStore — in-memory, used in tests.
  runner/     runner.go  Sandboxed code execution. Language detection, temp-file
                         write, subprocess with timeout, stdout/stderr capture.
  plugin/     plugin.go  Lua plugin runtime (gopher-lua). Loads ~/.cas/plugins/
                         *.lua. Sandboxed VM; no file I/O, no network.
  conductor/  conductor.go  Behavioral learning. Observes interactions, builds
                         ~/.cas/profile.json, injects context into LLM prompts.
  mcp/                   MCP integration layer (in progress).

ui/           model.go   Bubble Tea TUI model. Renders chat + workspace panels,
                         handles keyboard, streams tokens into workspace view.

tests/tui/               TUI integration tests. Spawn the real binary in tmux
                         and interact with it as a user would.
```

---

## Request flow

```
User message
     │
     ▼
intent.Detect()          ← regex only, no LLM call, no latency
     │
     ├─ KindClose   →  workspace.Manager.Close()
     ├─ KindRun     →  runner.Run(workspace.Content)
     ├─ KindCombine →  shell.handleCombine() + resolve.resolveAll()
     ├─ KindEdit    →  contract check → llm.Stream() → workspace.Update()
     ├─ KindCreate  →  contract check → llm.Stream() → workspace.Create()
     └─ KindChat    →  llm.Stream() → chat reply
                              │
                              ▼
                       conductor.Observe()   ← updates ~/.cas/profile.json
                              │
                              ▼
                       store.Save*()         ← SQLite
```

Plugin commands are checked before intent detection. If the message matches a
registered plugin prefix, the plugin handler fires and the flow short-circuits.

---

## Design by Contract

The contract package implements Bertrand Meyer's Design by Contract as the
security primitive for workspace operations.

Contracts are constructed and frozen before any LLM call. The model cannot
see, modify, or reason about them. Violations are always fatal to the
operation — no fallback, no retry, no LLM-assisted recovery. The three phases:

- **Preconditions** — checked before the operation starts (e.g. workspace type
  is allowed)
- **Postconditions** — checked after the operation completes (e.g. content size
  within limit)
- **Invariants** — structural rules that must always hold

`DefaultWorkspaceContract` is applied to all create/update operations.
Callers extend it with operation-specific rules before freezing.

---

## Persistence

Single local SQLite database at `~/.cas/cas.db`. No remote component.

The `Store` interface defines four concerns:

| Concern    | Types                                              |
|------------|----------------------------------------------------|
| Sessions   | `SessionRow`                                       |
| Messages   | `MessageRow`                                       |
| Workspaces | `WorkspaceRow` (live), `HistoryRow` (versioned)    |
| History    | Undo via `Undo()`, version replay via `ApplyVersion()` |

Two concrete implementations: `SQLiteStore` (production) and `MemoryStore`
(tests). The interface makes them interchangeable.

---

## LLM providers

Provider is selected by `CAS_PROVIDER` environment variable.

| Provider    | Value        | Default models                                    |
|-------------|--------------|---------------------------------------------------|
| Ollama      | `ollama`     | `qwen3.5:9b` (doc/list/chat), `qwen2.5-coder:7b` (code) |
| Anthropic   | `anthropic`  | `claude-sonnet-4-6` (doc/list/chat), `claude-haiku-4-5-20251001` (code) |
| Groq        | `groq`       | `llama-3.3-70b-versatile` (all types); set `GROQ_API_KEY` |

Model overrides: `CAS_MODEL_{TYPE}` env vars (e.g. `CAS_MODEL_CODE`).

Both providers use streaming. The `llm` package exposes `Stream()` and
`Complete()` with identical signatures — the provider selection is internal.

---

## Behavioral learning (Conductor)

The Conductor observes every shell interaction and writes a profile to
`~/.cas/profile.json`. After a minimum of 2 messages and 1 workspace, it
derives a natural-language context string (dominant doc types, workspace
preferences, recent edit patterns) and appends it to LLM system prompts.

The Conductor is fail-safe: a corrupt or missing profile never breaks the
shell. Observe errors are logged and discarded.

---

## Code execution (Runner)

The runner detects language from content (shebang, keywords), writes a temp
file, and spawns a subprocess with a 30-second timeout. Supported: Python,
Go, Bash, JavaScript (Node), Ruby.

Sandboxing is currently process-level (timeout, working directory isolation).
OS-level sandbox (seccomp/namespaces) is documented as a future hardening
step.

---

## Lua plugins

Plugins are `.lua` files in `~/.cas/plugins/`. At startup the plugin registry
loads all files into isolated gopher-lua VMs. Each plugin can register
commands via `cas.command(name, desc, handler)`. The sandbox prohibits file
I/O, `os.execute`, and network access. A bad plugin does not block others.

Plugin commands are matched before intent detection — exact match first, then
prefix match (case-insensitive).

---

## Tests

```
go test ./...                                     # 245 unit tests
TUI_INTEGRATION=1 go test -v -tags=integration \
  ./tests/tui/ -timeout 300s                      # 8 TUI integration tests
```

TUI tests spawn the real binary in tmux and interact with it as a user would,
catching runtime issues that unit tests miss. Requires tmux and a running
Ollama instance.

CI runs on GitHub Actions (`.github/workflows/ci.yml`) against Go 1.25.

---

## What does not exist

The following have been claimed in various contexts and are **not** in this
codebase:

- Remote architecture / decoupled session and execution layers
- `ExecutionContext` or `SessionStore` as a transport protocol
- `docs/remote-architecture.md`
- Any "Phase N complete" implementation
- `~/projects/loom` dependency

The store is local SQLite only. There is no server component.
