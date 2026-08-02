# CAS Development Schedule

**Status:** Living document — revised as blocks resolve.
**Date:** 2026-04-26 (last revised 2026-08-02)

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

**Shipped.** ARCHITECTURE.md, README pass, posts live on dev.to, releases
tagged.

- Top-level `ARCHITECTURE.md` describing actual layout
- README pass: verify test count claims, feature list, install instructions
- Publish one of the existing blog drafts (post 01, 02, or 04 in
  `~/projects/blog/`) — pick the one closest to ready
- Tag a release of current state

### Block 2 — Multi-backend (1–2 weeks, Track 2)

**Shipped, beyond plan.** Groq plus OpenAI and OpenRouter, with provider
validation, `--providers`, and active-provider display in the status bar.

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

**Decided 2026-08-01: continue Track 2.** Adoption at decision time was
flat (1 star, 0 forks, single-digit traffic), post 07 unpublished. The
listed Track 2 items are now executed: release automation with
cross-platform binaries (v0.3.1, v0.3.2), example plugins verified by CI,
the deployment doc, and cross-OS test execution — which surfaced and
fixed a real workspace-ordering bug. The decision point re-opens on the
same three options once post 07 publishes and adoption has had a chance
to respond.

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
- **2026-08-02.** Blocks 1–2 marked shipped. Block 3 decided: continue
  Track 2. Track 2 list executed (v0.3.1/v0.3.2 releases, example
  plugins, deployment doc, cross-OS CI plus the ordering fix it caught).
  Next decision gated on post-07 adoption signal.
