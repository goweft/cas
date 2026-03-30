# CAS — Conversational Agent Shell

A shell where conversation generates workspaces and users control them directly.

---

## The idea

Most AI tools collapse into one of two failure modes.

In the first, the AI acts as an **overlay** — a chat window bolted onto existing applications. You ask Copilot to summarize your spreadsheet and it adds a sidebar. The tool didn't change; you just got a new way to talk at it. The underlying interface friction remains.

In the second, the AI acts as an **agent** — it operates your tools on your behalf. You ask it to book a meeting and it clicks through your calendar. The user is now a passenger. When the agent makes a mistake, you have to figure out what it did and undo it manually.

CAS is a different arrangement. Conversation is for **generating** things. Once generated, you **control** them directly.

You say: *write a project proposal*. A workspace opens alongside the chat — a real, editable panel. You can type in it directly. You can ask CAS to make changes. Both paths work. Neither is subordinated to the other. The AI built the tool. You wield it.

This resolves a debate in HCI that has run since 1997. Ben Shneiderman argued that direct manipulation — clicking, dragging, editing in place — gives users a sense of control that delegation never can. Pattie Maes argued that interface agents, acting on your behalf, reduce cognitive load and handle complexity. They were both right. The conflict is real. CAS addresses it architecturally rather than picking a side: agents generate, users manipulate.

---

## What it does

CAS is a split-panel web interface mounted inside [Heddle](https://github.com/goweft/heddle), a local MCP mesh runtime. On the left, a persistent conversation. On the right, workspace panels that appear in response to what you say.

Three workspace types:

- **Document** — markdown prose with a serif rendered view and a plain-text edit mode
- **Code** — raw code files, syntax-appropriate editor settings, no markdown processing
- **List** — structured checklists, todo lists, and inventories

Each workspace type routes to a different local model:
- Documents and lists → `qwen3.5:9b` (general reasoning, strong prose)
- Code → `qwen2.5-coder:7b` (coding-specialized, faster)

Everything runs on your hardware. No API calls to external services. No content leaves the machine.

---

## What makes it different

### Intent detection

CAS classifies every message before any model call:

```
"write a project proposal"   → create workspace (document)
"create a python script"     → create workspace (code)
"add a section about budget" → edit workspace
"edit directly"              → chat (user will edit manually)
"hello"                      → chat
```

This runs as pattern matching — no LLM call, no latency. The result is an `intent` event emitted before any tokens stream, so the workspace panel opens immediately as generation begins. You see the document being written word by word, not a blank wait followed by a sudden result.

### Deterministic contracts

Every workspace operation passes through a contract layer before execution:

```python
contract.check_preconditions(action)   # is this operation permitted?
contract.check_invariants()            # are all invariants satisfied?
contract.check_postconditions(action, result)  # did the output meet requirements?
```

Contracts are enforced by deterministic Python code external to the model. The model cannot modify, bypass, or reason about them. This addresses a fundamental problem in agent security: LLMs cannot guarantee deterministic behavior. A model that correctly refuses a malicious instruction 99% of the time still permits it 1% of the time. You cannot build provable security on probabilistic foundations. Contracts separate the probabilistic layer (generation) from the enforcement layer (what that generation is allowed to do).

The approach is based on Bertrand Meyer's Design by Contract (1986), adapted for the agent context.

### Persistence

Sessions, messages, and workspaces all persist across restarts. SQLite with WAL mode. Workspaces survive a server restart and restore automatically on page load. Conversation history resumes where it left off. Edit history is versioned — every update snapshots the previous state, and undo is itself undoable.

### Behavioral learning

A Conductor module observes your usage patterns over time:
- Which workspace types you create most
- What topics appear in your documents
- Whether you tend to rewrite or add sections

This builds a profile that feeds back into the LLM system prompts, progressively adapting the tool to how you work. The profile is visible in the UI via a `~` button in the topbar.

---

## Architecture

```
CAS (user-facing shell)
  └── Contracts (deterministic enforcement layer)
       └── Heddle (MCP mesh runtime, local models, audit)
```

CAS mounts inside Heddle as a FastAPI router at `/api/cas/`. It depends on Heddle but Heddle has no knowledge of CAS. The dependency is one-directional and the projects are kept separate: Heddle is public; CAS is private.

```
src/cas/
├── shell.py       # Session manager, intent detection
├── workspaces.py  # Workspace lifecycle, three types
├── contracts.py   # Deterministic contract enforcement
├── llm.py         # Ollama bridge, type-aware prompts, model routing
├── renderer.py    # HTML rendering: cas-doc, cas-code, cas-list
├── conductor.py   # Behavioral learning, user context
├── store.py       # SQLite persistence
├── api.py         # FastAPI router, SSE streaming
└── static/
    └── index.html # Split-panel UI
```

API routes:

```
POST /api/cas/message/stream    SSE: session → intent → token×N → workspace? → chat_reply → done
POST /api/cas/session           Create a fresh session
GET  /api/cas/workspaces        List active workspaces
PUT  /api/cas/workspace/{id}    Update workspace content
GET  /api/cas/workspace/{id}/render    Type-aware HTML render
GET  /api/cas/workspace/{id}/history   Edit version list
POST /api/cas/workspace/{id}/undo      Restore previous version
GET  /api/cas/workspace/{id}/export/{fmt}   md / html / txt
GET  /api/cas/profile           Conductor profile + generated context
```

---

## Current state

Working:

- Split-panel UI: conversation on the left, tabbed workspaces on the right
- Three workspace types with type-appropriate models, renderers, and editor settings
- Real-time streaming: tokens appear in the workspace overlay as they arrive
- Full persistence: sessions, messages, workspaces, and edit history
- Undo with snapshotting (undo is undoable)
- Export to markdown, HTML, and plain text
- Code edit mode: tab key indentation, no spellcheck, tighter line spacing
- Behavioral learning: conductor tracks patterns, generates context for prompts
- Profile panel: visible learning state with stats and type breakdowns
- Session management: new session button, history restored on page load
- Ollama cold-start UX: elapsed timer, 30-second warning, cancel button

Not yet built:

- Workspace rename from UI
- Pluggable model backends (Anthropic API, OpenAI)
- More workspace types (data tables, terminal, code execution)
- Multi-user or concurrent session support
- CAS as a login shell / desktop replacement

---

## Running it

CAS runs as part of the Heddle service:

```bash
sudo systemctl restart loom-dashboard
```

Then open: `http://localhost:8300/api/cas/`

Direct launch for development:

```bash
cd ~/projects/loom
source venv/bin/activate
python heddle_dashboard.py 8300
```

Tests (338 passing, ~3s):

```bash
cd ~/projects/cas
python -m pytest tests/ -q
```

---

## References

Shneiderman & Maes (1997). "Direct Manipulation vs. Interface Agents." *Interactions, 4(6).*

Meyer (1988). *Object-Oriented Software Construction.* Prentice Hall. (Design by Contract)

Norman (1986). "Cognitive Engineering." In *User Centered System Design.* (Gulf of Execution/Evaluation)

Horvitz (1999). "Principles of Mixed-Initiative User Interfaces." *CHI '99.*

Dennis & Van Horn (1966). "Programming Semantics for Multiprogrammed Computations." *CACM.*
