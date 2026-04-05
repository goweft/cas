<p align="center"><img src="banner.svg" alt="cas" width="100%"></p>

<h1 align="center">CAS</h1>
<p align="center"><strong>Conversational Agent Shell</strong></p>
<p align="center">
  A terminal shell where conversation generates workspaces and you control them directly.<br>
  Single static binary · deterministic contracts · streaming · persistent sessions
</p>

[See It Work](#see-it-work) · [The Idea](#the-idea) · [How It Works](#how-it-works) · [Keyboard Reference](#keyboard-reference) · [Quick Start](#quick-start)

---

## See It Work

```
┌─ chat ──────────────────────┐ ┌─ [d] Project Proposal ──────────────────┐
│                             │ │                                          │
│ you › write a project       │ │ # Project Proposal                       │
│       proposal for a local  │ │                                          │
│       AI platform           │ │ ## Executive Summary                     │
│                             │ │                                          │
│ cas › Created document      │ │ This proposal outlines a self-hosted     │
│       workspace "Project    │ │ AI platform built on local inference...  │
│       Proposal". Edit       │ │                                          │
│       directly or ask me    │ │ ## Architecture                          │
│       to make changes.      │ │                                          │
│                             │ │ The platform consists of three layers... │
│ you › add a security        │ │                                          │
│       section               │ │ ## Security                              │
│                             │ │                                          │
│ cas › Updated workspace     │ │ All tool calls pass through deterministic│
│       "Project Proposal".   │ │ contracts before execution...            │
│─────────────────────────────│ │                                          │
│ > █                         │ │                                    ↕ 45% │
└─────────────────────────────┘ └──────────────────────────────────────────┘
  tab: workspace  enter: send  ctrl+c: quit
```

Tokens stream into the workspace as they are generated. The left panel is persistent conversation — the right panel is the workspace you control directly.

---

## The Idea

Most AI tools collapse into one of two failure modes.

In the first, the AI acts as an **overlay** — a chat window bolted onto existing applications. The underlying friction remains.

In the second, the AI acts as an **agent** — it operates your tools on your behalf. The user is now a passenger.

CAS is a different arrangement. Conversation is for **generating** things. Once generated, you **control** them directly.

You say: *write a project proposal*. A workspace tab opens alongside the chat — tokens streaming into it as the model generates. When generation ends, you can type in it, ask CAS to make changes, or both. The AI built the tool. You wield it.

This resolves a debate in HCI running since 1997. Ben Shneiderman argued that direct manipulation gives users a sense of control that delegation never can. Pattie Maes argued that interface agents reduce cognitive load and handle complexity. They were both right. CAS addresses it architecturally: **agents generate, users manipulate.**

---

## How It Works

### Intent detection

Every message is classified before any model call — no LLM, no latency:

```
"write a project proposal"   → create workspace (document)
"create a python script"     → create workspace (code)
"add a section about budget" → edit active workspace
"edit it directly"           → chat (self-edit exclusion → not an LLM edit)
"hello"                      → chat
```

Self-edit phrases are checked first, before edit patterns — this prevents "edit it directly" from being misclassified and triggering an unwanted LLM call.

### Deterministic contracts

Every workspace operation passes through a contract layer before execution:

```go
contract.CheckPreconditions()   // is this operation permitted?
contract.CheckInvariants()      // are all invariants satisfied?
contract.CheckPostconditions()  // did the output meet requirements?
```

Contracts are enforced by deterministic Go code external to the model. The model cannot modify, bypass, or reason about them. Any violation is fatal to the operation — fail-closed always.

Based on Bertrand Meyer's Design by Contract (1986).

### Multi-provider model routing

Select inference backend via `CAS_PROVIDER`:

**Ollama (default)** — local, private:
- Documents / lists / chat → `qwen3.5:9b`
- Code → `qwen2.5-coder:7b`

**Anthropic API** — cloud, no GPU:
- Documents / lists / chat → `claude-sonnet-4-6`
- Code → `claude-haiku-4-5-20251001`

Override any model: `CAS_MODEL_CODE=qwen3.5:27b ./cas`

### Behavioral learning

A Conductor module observes your usage patterns across sessions:

- Which workspace types you create most
- What topics appear in your documents  
- Whether you tend to rewrite or add sections

This builds a profile at `~/.cas/profile.json` that feeds back into LLM system prompts, progressively adapting the tool to how you work. More sessions → better context → better output.

### Persistence

Sessions, workspaces, and conversation history persist across restarts via SQLite (WAL mode) at `~/.cas/cas.db`. Workspaces restore as tabs on next launch.

---

## Keyboard Reference

| Key | Action |
|---|---|
| `Enter` | Send message (chat focused) |
| `Tab` | Switch focus between chat and workspace panels |
| `Esc` | Return to chat focus from workspace / save + exit edit mode |
| `[` / `]` | Previous / next workspace tab (workspace focused) |
| `e` | Enter inline edit mode for the active tab |
| `↑` / `↓` | Scroll content (workspace focused) or scroll history (chat focused) |
| `PgUp` / `PgDn` | Scroll workspace content by 10 lines |
| `Ctrl+S` | Save in edit mode (stay in edit mode) |
| `Ctrl+C` | Quit (or discard edits in edit mode) |

**Edit mode** (amber border) — full terminal editor via `charmbracelet/bubbles` textarea: cursor movement, word wrap, standard editing keys. `Esc` saves and returns to view. `Ctrl+C` discards.

---

## Architecture

```
CAS (terminal shell)
  └── Contracts (deterministic enforcement — Bertrand Meyer's Design by Contract)
       └── Heddle (optional: MCP mesh runtime, audit logging)
```

CAS runs standalone. Heddle integration is optional and adds trust enforcement, credential brokering, and tamper-evident audit logging.

```
internal/
├── intent/      Zero-latency intent detection — regex, no LLM call
├── contract/    Design by Contract enforcement, fail-closed
├── workspace/   Workspace lifecycle, three types, contract-enforced
├── shell/       Session manager, wires all packages, ProcessMessage/StreamMessage
├── llm/         Ollama + Anthropic streaming/sync clients, model routing
├── store/       Store interface, SQLiteStore (WAL), MemoryStore (tests)
└── conductor/   Behavioral learning — observe, profile, user_context
ui/              Bubble Tea TUI: split panel, tabs, streaming, inline edit
cmd/cas/         Entry point: --db, --memory flags
```

---

## Quick Start

```bash
git clone https://github.com/goweft/cas-go.git
cd cas-go
go build -o cas ./cmd/cas
```

### With Anthropic API (no GPU required)

```bash
export CAS_PROVIDER=anthropic
export ANTHROPIC_API_KEY=sk-ant-...
./cas
```

### With Ollama (local, private)

```bash
ollama pull qwen3.5:9b
ollama pull qwen2.5-coder:7b
./cas
```

### Flags

```
./cas --memory          # in-memory store, no persistence (useful for testing)
./cas --db /path/to.db  # custom SQLite path (default: ~/.cas/cas.db)
```

### Tests

```bash
go test ./...

# LLM integration tests (requires running provider):
CAS_INTEGRATION=1 go test ./internal/shell/...
```

---

## References

Shneiderman & Maes (1997). "Direct Manipulation vs. Interface Agents." *Interactions, 4(6).*

Meyer (1988). *Object-Oriented Software Construction.* Prentice Hall. (Design by Contract)

Norman (1986). "Cognitive Engineering." In *User Centered System Design.* (Gulf of Execution/Evaluation)

Horvitz (1999). "Principles of Mixed-Initiative User Interfaces." *CHI '99.*
