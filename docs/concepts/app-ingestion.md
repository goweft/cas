# App Ingestion

**Status:** Concept. Depends on sub-agent decomposition (planned, not yet designed).

---

## The idea

Current computer-use agents try to take the keyboard away from the user. An
agent opens a browser, clicks things, types things, and the user watches.
Consent is up-front and coarse: *"yes, you may drive my computer."*

App Ingestion inverts the direction. Instead of the agent leaving the shell to
reach into an app, the app comes **into** the shell as a workspace. The user
stays in the conversational layer. The app — or its API — becomes a first-class
citizen that inherits everything the shell already provides: contracts, audit,
scoped memory, sub-agent routing.

Dragging something into the shell is the capability grant. The gesture **is**
the scope. What happens inside the scope is governed by the workspace's
contract.

---

## The unifying abstraction: Ingestion → Workspace

*"Drag the API in"* and *"drag the app in"* are the same operation at the type
level. A source descriptor resolves into a `Workspace` with a typed operation
surface and a contract-wrapped action set. The shell doesn't distinguish
between the backing implementations — HTTP call, MCP tool, synthesized event on
a window handle — those are drivers behind a uniform interface.

```
┌──────────────────────────────────────────────────────┐
│ Shell (conversation + workspace layout)              │
│                                                      │
│  ┌─────────────┐  ┌─────────────┐  ┌─────────────┐  │
│  │ Workspace:  │  │ Workspace:  │  │ Workspace:  │  │
│  │ document    │  │ ingested    │  │ ingested    │  │
│  │             │  │ API (MCP)   │  │ web app     │  │
│  │             │  │ + sub-agent │  │ + sub-agent │  │
│  └─────────────┘  └──────┬──────┘  └──────┬──────┘  │
│                          │                │          │
└──────────────────────────┼────────────────┼──────────┘
                           │                │
                    ┌──────▼──────┐  ┌──────▼──────┐
                    │ MCP server  │  │ WebView     │
                    │ (Linear,    │  │ adopting    │
                    │  GitHub,    │  │ the app's   │
                    │  internal)  │  │ DOM         │
                    └─────────────┘  └─────────────┘
```

One abstraction, many drivers. This keeps the concept small.

---

## Four primitives

### 1. Ingestion protocol

What a "drag" actually dispatches.

- **API mode:** an OpenAPI URL, an MCP endpoint, a gRPC reflection target. The
  shell resolves the schema and materializes a workspace whose operations are
  the schema's operations.
- **App mode:** a window handle, a PID, or a URL. Resolution produces a
  capability-scoped handle plus a driver that can read and synthesize events
  on that surface.

The gesture itself is the consent — no separate permission prompt, no OAuth
dance per action. Revocation is equally ergonomic: close the workspace, the
grant is gone.

### 2. Workspace materialization

The ingested thing becomes a first-class workspace with a panel in the shell,
not merely a tool registered with the main agent. It owns:

- A **rendering surface** (tab in the shell, panel, or embedded view).
- A **typed operation set** (from the schema or the adopted surface).
- **Queryable state** (what is currently selected/loaded/displayed).
- A bound **sub-agent**.

This is the key architectural difference from a plugin or a plain MCP tool: the
workspace owns a surface and a sub-agent — it is not just a function table on
the main loop.

### 3. Sub-agent binding

Each ingested workspace gets its own sub-agent from the planned
decomposition work. That sub-agent carries a **scoped contract**:

- It can only invoke operations on its own workspace.
- Its memory reads are scoped to that workspace plus whatever the user has
  explicitly shared.
- Its autonomy level is governed by a **dial** — `suggest-only` →
  `confirm-each-action` → `run-to-completion`.

This is where App Ingestion stops being a UX idea and becomes a security
story: the blast radius of a rogue or confused agent is the workspace it was
dropped into, not the shell.

### 4. Scoped memory with intent tags

Chat-as-memory-store only works if values carry enough metadata to
disambiguate. Without intent tags, `email` in a banking workspace and `email`
in a work intake form will collide.

Schema is roughly:

```go
type MemoryEntry struct {
    Value           string
    IntentTag       string  // "personal passport", "work email for Acme"
    Scope           Scope   // workspace ID, shell-global, or specific source
    IngestionSource string  // which workspace wrote this
}
```

When a workspace queries memory for a field, the resolver ranks candidates by
intent-match plus scope-proximity. *"Work email for Acme Corp intake"* is not
retrieved for a personal banking form — even though both fields are literally
`email`.

---

## Buildable today vs. needs research

| Mode        | Feasibility          | Blockers                                   |
|-------------|----------------------|--------------------------------------------|
| **API**     | Buildable today      | Sub-agent decomposition, Workspace surface |
| **WebView** | Buildable after API  | Embedded browser runtime choice            |
| **Native**  | Research-phase       | Platform window adoption (X11/Wayland/AX)  |

**API mode** is buildable against current CAS. MCP infrastructure already
exists through Heddle. The missing pieces are:

1. The `Workspace` abstraction extended beyond a TUI panel — it must own
   operations, state, and a sub-agent binding.
2. The ingestion command (`cas ingest <source>`) and schema resolution.
3. The autonomy dial wired to contract strictness.

No new OS integration required.

**WebView mode** is the pragmatic middle step. Ingest a URL, run it in an
embedded WebView the shell controls, and get authoritative DOM access — no
vision fallback needed. This covers a large fraction of real-world apps and
should be the second milestone after API mode.

**Native app mode** is research-phase. Window adoption is platform-specific:
X11 embedding, Wayland's lack of a clean story here, macOS AX API, Windows
UIA. Pick one platform first (likely X11/Wayland given the Linux dev
environment) and treat the others as follow-on.

---

## Minimum buildable slice

The smallest demo that proves the thesis:

- API-mode ingestion of a single MCP server.
- Materialized as a workspace panel with a bound sub-agent.
- Sub-agent operates under a scoped Heddle contract.
- Autonomy dial exposed in the shell.
- Workspace-scoped memory with intent tags.

Pick a real target — Linear, GitHub, or one of the internal Heddle services —
and show end-to-end that *"drag it in, talk to it, it acts inside its
contract"* works.

Scope estimate: roughly a week **after** sub-agent decomposition lands,
because sub-agent binding is the load-bearing piece.

---

## Dependency chain

```
Current state ─────── code exec, Lua plugins, cross-workspace ops shipped.
                      SQLite persistence. Streaming TUI. Stable feature set.

Prerequisite ──────── sub-agent decomposition with Heddle contracts.
                      Not yet designed in detail. Load-bearing for what
                      follows — every ingested workspace binds a sub-agent.

Step 1 ────────────── API-mode ingestion
                      · Workspace surface (operations + state + sub-agent)
                      · Autonomy dial
                      · Scoped memory with intent tags

Step 2 ────────────── WebView-mode ingestion
                      · Embedded browser runtime
                      · Authoritative DOM access

Step 3 ────────────── Native app window adoption
                      · Platform-specific (research-heavy)
```

App Ingestion reuses the contract layer that already exists and extends off
the sub-agent work whenever that lands. Numbering is sequencing, not
committed planning — Step 1 is the smallest meaningful slice; Steps 2 and 3
are progressively more ambitious.

---

## Open questions

- **Scope persistence.** When a workspace is closed and later re-opened
  against the same source, does its memory persist? Default answer:
  workspace-scoped memory is per-ingestion and ephemeral; promotion to
  shell-global memory is an explicit user action.
- **Field binding for native apps.** Accessibility API vs. vision. AX is
  reliable but limited; vision handles anything but is flaky. The CAS-native
  answer is a hybrid where the contract verifies *"did the field actually
  receive the value I intended?"* as a postcondition — either mechanism is
  safe because the contract catches the miss.
- **Autonomy dial semantics.** Does the dial affect the sub-agent's
  prompting, the contract's strictness, both, or a third thing? Probably
  both, but the precise mapping wants a design pass of its own.
- **Multi-workspace sub-agent coordination.** If two ingested workspaces need
  to act together (e.g. *"take this Linear issue and file a GitHub PR for
  it"*), does the orchestration live in the main loop, in a third coordinator
  sub-agent, or in a combine-style operation analogous to the existing
  cross-workspace combine?

---

## Why this is worth writing down now

App Ingestion sits after the sub-agent decomposition work. Writing it down now does two things:

1. **Forces sub-agent decomposition decisions to be compatible with it.**
   Sub-agent work done without ingestion in mind will likely bake in
   assumptions that make Step 1 harder.
2. **Differentiates CAS from the 2026 computer-use wave.** Generic
   computer-use agents that drive a shared desktop are going to flood the
   space. *App comes to the shell* is a genuinely different shape, and
   staking the claim early is worth the page.
