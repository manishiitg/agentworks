[← platform / coding-agent-bridge](index.md)

# PLAT-152 — NOT A DEFECT: every interactive adapter emits tool-call chunks; the original finding grepped only one file per package

| Field | Value |
|---|---|
| State | closed |
| Priority | P2 |
| Product | coding-agents |
| Area | bridge |
| Summary | P2 — not itself a user-visible bug (PLAT-149 already ships a |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `closed / not a defect` — the asymmetry does not exist. Verified 2026-08-19 across all four providers; see "Correction" below. No code change was made, and none should be: acting on the original finding would have introduced duplicate tool-call chunks. |
| Last synchronized | `2026-08-19` |

- **Priority:** P2 — not itself a user-visible bug (PLAT-149 already ships a
  provider-agnostic recovery path that does not depend on this), but it is
  the concrete evidence that mcpagent's coding-agent adapters are not
  standardized against a shared contract, which is the stated job of that
  layer.
- **Owner:** `multi-llm-provider-go/pkg/adapters/{claudecode,codexcli,picli}`
  interactive adapters.

## Correction (2026-08-19): the asymmetry does not exist

**Every interactive adapter emits tool-call chunks.** Counting emission sites
per PACKAGE rather than per file:

| provider | structured transport | interactive/tmux transport |
|---|---|---|
| picli | `picli_structured_adapter.go` | `picli_interactive_adapter.go` |
| claudecode | `claudecode_structured_adapter.go` | `claudecode_transcript_stream.go` |
| codexcli | `codexcli_structured_adapter.go` | `codexcli_transcript_stream.go` |
| cursorcli | `cursorcli_structured_adapter.go` | `cursorcli_transcript_stream.go` |

Four `StreamChunkTypeToolCall*` sites in each of the four packages. Concrete
line references, read directly rather than inferred:

- `claudecode_transcript_stream.go:211` (`ToolCallEnd`), `:220` (`ToolCallStart`)
- `codexcli_transcript_stream.go:142` (`ToolCallEnd`), `:152` (`ToolCallStart`)
- `cursorcli_transcript_stream.go:258` (`ToolCallStart`), `:261` (`ToolCallEnd`)

These are **live**, not end-of-turn reconstruction: `streamClaudeTranscript`
"tails the claude-code JSONL transcript" on a `time.NewTicker`
(`claudecode_transcript_stream.go:57,72,98`), and codex's tailer parses
`ToolName`/`ToolCallID`/`IsToolEnd` with per-`ToolCallID` dedup as rollout rows
are appended. So the open design question this ticket posed — live per-chunk
(Pi's shape) versus end-of-turn reconstruction (Claude/Codex's shape) — rests
on a distinction that is not there. Both are live per-chunk.

### What went wrong in the original analysis

The method was to grep `*_interactive_adapter.go` and conclude the capability
was absent from the **provider**. The evidence gathered was accurate — Claude's
and Codex's interactive adapter files genuinely contain zero
`StreamChunkTypeToolCall*` references — but the inference from "not in this
file" to "not in this provider" is what fails: the code lives in a sibling file
in the same package. The ticket even hedged carefully about inline construction
*within the grepped file*, which is why the gap looked closed rather than
unexamined.

Pi is the odd one only in **file layout**, not behavior: it emits inline from
its interactive adapter because pi's embedded JS harness gives it a structured
marker side-channel (`marker.Type == "tool_execution_start"`), whereas the
other three tail a JSONL transcript/rollout from a dedicated file. Same
contract, different source of truth.

**Cursor CLI is the tell.** It was not examined by the original analysis at
all, yet has the identical `*_transcript_stream.go` structure. Three of four
providers sharing one pattern is a consistent design, not two providers missing
something.

### Consequence

Acting on the original finding would have caused a regression, in either
direction it proposed: adding chunk emission to Claude/Codex/Cursor would have
**duplicated** every tool-call chunk, and "standardizing Pi to match" would have
meant moving working code for no behavioral gain.

The stated acceptance criterion — "a consumer of any interactive adapter's
`StreamChan` sees the same contract regardless of provider" — is **already
met**.

### Worth keeping

One framing point from the original ticket is off independently of the above:
it attributes this to "mcpagent's stated job." A single adapter's stream
contract is Layer 1 (`multi-llm-provider-go`); mcpagent is Layer 2
(orchestration above it). The ticket's own Owner field already points at
`multi-llm-provider-go/pkg/adapters/...`, so only the prose disagrees.

If a future ticket does find a genuine per-provider stream-contract gap, the
enforcement mechanism already exists and should be used rather than a one-off
reconciliation: `multi-llm-provider-go/coding_agent_certification.go` (capability
flag -> required certification -> real E2E proof), with `CertStructuredStreaming`
as the direct precedent. That matters because silent drift in exactly this area
has happened before — the structured adapters' own `"structured_cli"` transport
label was orphaned in a refactor precisely because nothing tested that a real
adapter still declared it.

## Original analysis (retained for the record — conclusion now known false)

## How it was found

While hunting PLAT-149's still-unlocated "mechanism B" — the source of
tool-call events carrying a provider's own native id (Claude's
`toolu_XXXXX`, Codex's `call_XXXX`) for interactive/tmux-transport sessions.
Ruling out every candidate in `claudecode_interactive_adapter.go` and
`codexcli_interactive_adapter.go` meant reading every adapter's `StreamChan`
send site directly, which surfaced this asymmetry by comparison rather than
by looking for it directly.

## What is proven

Direct grep for `StreamChunkTypeToolCallStart`/`StreamChunkTypeToolCallEnd`
construction, confirmed by reading the surrounding code at each hit:

- `pkg/adapters/picli/picli_interactive_adapter.go:1410` and `:1425` —
  Pi's interactive adapter constructs and sends both chunk types natively,
  as part of its own stream processing.
- `pkg/adapters/claudecode/claudecode_interactive_adapter.go` — zero
  references to either type anywhere in the file. The only chunk kinds sent
  directly on `streamChan <-` are `StreamChunkTypeTerminal` and
  `StreamChunkTypeStatusLine` (confirmed by grepping the raw send syntax, not
  just the type-constant name, since a send could in principle construct the
  value inline without naming the constant at the call site).
- `pkg/adapters/codexcli/codexcli_interactive_adapter.go` — same result: no
  literal construction of either chunk type anywhere in the file.

So of the three interactive (tmux) coding-agent adapters this platform runs,
exactly one reports its own tool calls through the adapter contract instead
of leaving them to be reconstructed after the fact by another layer.

## Why this matters here specifically

`mcpagent`'s stated job (per standing product direction) is to standardize
coding-agent behavior so the platform does not need per-provider fixes. This
adapter contract is the concrete place where that standardization is
currently missing: a consumer of the `StreamChan` cannot rely on tool-call
chunks existing for a session just because the provider is one of the
supported CLIs — it depends on which CLI. PLAT-149's recovery path was
deliberately built not to depend on this (matching by name/time against
`toolcalllog`, a provider-agnostic HTTP-layer signal instead), which is why
this asymmetry did not block that fix — but it means any *future* consumer
that expects adapter-level tool-call chunks uniformly will work correctly
against Pi and silently misbehave against Claude Code and Codex CLI, exactly
the kind of single-provider surprise the standardization goal exists to
prevent.

## What was not investigated here

- Whether Pi's mechanism is itself complete/correct (paired starts and ends,
  correct ids, no missed calls) — only that it exists, unlike the other two.
- Whether Claude Code's and Codex CLI's *end-of-turn transcript
  reconstruction* (`readClaudeTranscriptMessages` /
  `readCodexTranscriptMessages`, both unconditional, both already read in
  full while investigating PLAT-141/149) is meant to be the intentional
  substitute for live per-chunk reporting, or is simply what was built first
  and never reconciled with Pi's adapter. That design question — one shared
  contract both other adapters implement live, vs. Pi's live path being
  brought in line with the transcript-reconstruction approach instead — is
  the actual fix decision for whoever picks this up, not resolved here.

## Incidental finding this correction fed forward

Confirming these tailers are live in production (not opt-in/family-server-only
as PLAT-149 had believed) turned out to matter for a different reason than
this ticket's own topic: it means the tailers' unfixed
`ctx.Done()`-without-a-final-read race — a real gap, orthogonal to whether
Pi is "the odd one out" — is an active production exposure, not a
theoretical one. [PLAT-160](plat-160.md) picks that up.

## Acceptance (already met — see Correction)

- A decision is recorded on which shape is the platform's real adapter
  contract for tool-call reporting: live per-chunk (Pi's shape) or
  end-of-turn reconstruction (Claude/Codex's shape).
- Whichever is chosen, `claudecode_interactive_adapter.go`,
  `codexcli_interactive_adapter.go`, and `picli_interactive_adapter.go` all
  implement it the same way, so a consumer of any interactive adapter's
  `StreamChan` sees the same contract regardless of provider.
