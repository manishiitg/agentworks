# Refactor records

Implementation migrations: what was changed structurally, why, and how far it
got. Unlike `docs/bugs/`, these describe deliberate rewrites rather than
defects — but each was motivated by defects, and the motivating incidents are
linked from the entries below.

Status is load-bearing here. An in-progress refactor describes an API that does
not exist yet; a shipped one describes the current system.

| Document | Status |
|---|---|
| [canonical_agent_definition_construction.md](canonical_agent_definition_construction.md) | **Proposal only (2026-08-04); no implementation started.** Finish the existing immutable `mcpagent.AgentDefinition` migration so every workflow step, Pulse stage, background agent, scheduled run, and converted chat constructs instructions, skills, tool bindings, and runtime policy once. The proposal explicitly rejects adding a second public `AgentSpec`, reduces DB/KB/learnings authorization to reader or writer, and makes its first acceptance target the scheduled/standalone/converted-chat Pulse Fixer split tracked by PLAT-003. |
| [mcpagent_public_api_simplification.md](mcpagent_public_api_simplification.md) | **Complete and verified (2026-08-02).** Reduced `mcpagent.Agent` from 70 exported methods and 64 exported fields to 4 methods and 0 fields, and package functions from 148 to 45, with AST golden tests pinning the exact names. Motivated by custom_tool_category_as_agent_addressing.md: the large surface let callers sequence internal lifecycle themselves, so the same fact lived in several independently stale copies. The final registry follow-up is also complete: redundant `Agent.customTools` state was removed, and direct-tool discovery, bridge schemas, timeout selection, and execution now share one canonical record. |
| [lazy_per_terminal_event_loading.md](lazy_per_terminal_event_loading.md) | **Implemented and verified (2026-08-02).** Child terminals load their transcript on open via a cursor API instead of every session event being pulled and filtered client-side. The duplication hazard this note was written to avoid — reimplementing `getOwnedTerminalOwnerKeys` in Go — was resolved properly: `ResolveTerminalOwnerID` is one write-time resolver shared by the event store and terminal store, and main became a positive owner (`main:<session_id>`) rather than a frontend complement. The retention-policy follow-up is complete: a cross-language contract test pins the frontend set to the backend set plus exactly `streaming_start`, `streaming_chunk`, and `streaming_end`. |
| [native_streaming_stt.md](native_streaming_stt.md) | **Working, confirmed on a real microphone (2026-08-02); not yet in a release build.** Live dictation runs on a native Swift helper over FluidAudio (Apache-2.0) instead of re-transcribing the whole clip through Python/MLX every preview tick. Preview text is now punctuated and identical to the committed text, and the committed text lands in ~100ms against 1.2-2.4s before. Notably the streaming ASR model was tried and ABANDONED: it is built for voice-assistant turn-taking, so it froze at mid-sentence pauses and emitted nothing for short utterances; the batch model at ~120x realtime drives the preview instead. Remaining prize: WhatsApp voice notes still use Python, and moving them would delete the ~3.1GB venv, its install UI, and the MLX unbounded-cache workaround. |
| [terminal_live_attach_transport.md](terminal_live_attach_transport.md) | Shipped on `main`; sole transport. |
| [live_attach_app_vs_demo_debug.md](live_attach_app_vs_demo_debug.md) | Resolved, then re-architected in-band. Read with the entry above; alone it describes a superseded design. |
| [cli_live_input_unification.md](cli_live_input_unification.md) | Core implemented surgically. |
| [durable_ack_p0.md](durable_ack_p0.md) | **Done for codex/pi/muse/Claude (2026-09-19/20); Cursor live pending quota 9/21.** File-ack P0 + pane fast-confirm P1 split across all five CLI agents: observe-only durable arbiter behind the pane submit loop, accepted-but-unflushed outcome, and WhatsApp-style single/double delivery ticks in chat. |

## Reading order for the agent-API work

The public-API refactor is the largest change in this folder and it does not
stand alone. In order:

1. ../bugs/README.md — the shared shape of the defects that
   motivated it: one fact, two sources, nothing checking they agree.
2. ../bugs/custom_tool_category_as_agent_addressing.md
   — the specific evidence, including the "Why this now warrants a refactor"
   section.
3. [mcpagent_public_api_simplification.md](mcpagent_public_api_simplification.md)
   — the plan, its review, and the open questions.

## Convention

State a status in the first lines. A refactor doc without one is worse than no
doc: a reader cannot tell whether it describes the system, a plan, or an
abandoned direction, and the code will not tell them either.
