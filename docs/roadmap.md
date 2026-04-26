# CAS Development Schedule

**Status:** Draft. Subject to revision.
**Date:** 2026-04-26

---

## Starting point

CAS has been quiet since April 5. Current shipped scope: Go TUI, Anthropic +
Ollama providers, SQLite, Design by Contract, code execution, Lua plugins,
cross-workspace ops. Public on `goweft/cas`, CI green, feature-complete for
current scope.

A schedule only makes sense if there's appetite to resume. This document
assumes there is. If priorities shift, the right move is to revise this file
rather than work around it.

---

## Three independent tracks

Work falls into three shapes that don't block each other:

1. **Stabilize / surface.** Existing features made more discoverable and
   reliable. No new architecture.
2. **Operational reach.** More LLM backends, deployment patterns. Plumbing.
3. **New architecture.** Sub-agent decomposition → app ingestion. The
   forward work documented in `docs/concepts/app-ingestion.md`.

Each track ships something independently. If one stalls, the others continue.

---

## Sequence

### Block 1 — Audit and surface (1–2 weeks, Track 1)

Reconcile doc claims with reality. Cut a clean baseline.

- Top-level `ARCHITECTURE.md` describing actual layout
- README pass: verify test count claims, feature list, install instructions
- Publish one of the existing blog drafts (post 01, 02, or 04 in
  `~/projects/blog/`) — pick the one closest to ready
- Tag a release of current state

### Block 2 — Multi-backend (1–2 weeks, Track 2)

Add a third LLM provider. Groq is a reasonable target — fast, free tier,
distinctive from Anthropic and Ollama. This is the realistic gate before
pushing CAS to a wider audience: people without an Anthropic key or local
Ollama setup need an option.

- Audit existing provider interface for whether a third backend slots in
  cleanly
- Implement Groq provider
- Document provider switching in README
- Release

### Block 3 — Decision point

After Blocks 1 and 2 ship, the choice is:

- Continue Track 2 (more providers, Tailscale deployment, plugin examples)
- Begin Track 3 (sub-agent decomposition design pass)
- Step back, let CAS sit, work on something else

Don't commit to Track 3 in advance — the right move depends on what Blocks 1
and 2 reveal about both adoption signal and personal appetite.

### Block 4 — If Track 3 begins (3–6 weeks)

Sub-agent decomposition. Real architecture work. Genuinely uncertain time
estimate — could be twice the upper bound. This block is the one most likely
to slip.

- Design pass: what are the sub-agent boundaries? Don't assume the
  intent/generation/edit split — interrogate it from first principles
- Heddle contract surface for sub-agents
- Implementation behind a feature flag
- Integration test proving decomposition doesn't regress current behavior

### Block 5 — App ingestion Step 1 (1–2 weeks after Block 4)

Minimum buildable slice from `docs/concepts/app-ingestion.md`: API-mode
ingestion of one MCP server as a workspace with bound sub-agent, autonomy
dial, scoped memory.

---

## Not on the schedule

- **App ingestion Steps 2–3** (WebView, native windows). Conditional on
  Step 1 working.
- **Semantic dispatch.** Deferred until the architecture above is stable
  enough to host it.
- **Calendar commitments.** "Block 2" means "the second thing worked on,"
  not "by next Saturday."

---

## Principles

- **Each block ships something.** No long-running unmerged feature
  branches.
- **Tracks are independent.** If one stalls, drop it without dragging
  others down.
- **Audit before assuming.** Memory drift already happened once
  (see commit `73774d1` and `docs/concepts/app-ingestion.md` history).

---

## Revision log

- **2026-04-26.** Initial draft. Anchored to current state after audit
  on the same date.
