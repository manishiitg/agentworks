[← platform / coding-agent-bridge](index.md)

# PLAT-234 — `search_web_llm` with a CLI-backed provider needs a multi-minute caller budget, not ~180s

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | harness_issue (guidance gap), severity medium across findings. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `guidance fixed; no platform 180s timeout exists; review wording corrections applied` |
| Last synchronized | `2026-08-29` |

- **Priority:** harness_issue (guidance gap), severity medium across findings.
- **Findings:** Twitter/social-media `PUL-743BAC2B`, `PUL-CA44C7EC`,
  `PUL-0B1A2E4B` — three independent runs, all `search_web_llm` with
  `provider=codex-cli, model_id=gpt-5.5`, all citing the identical
  `deadline_seconds=180`, all recovering via the sanctioned native
  `WebSearch` fallback after the timeout.

## Investigation: confirmed there is no platform-side 180s timeout to fix

Checked every layer between the tool call and the actual CLI process,
starting from the same "is this even fixable in-repo" question asked for
PLAT-224/PLAT-232:

- `search_web_llm`'s own tool schema (`agent_go/pkg/workspace/advanced_tools.go`)
  has no `timeout`/`deadline` parameter at all.
- The live server's own `TOOL_EXECUTION_TIMEOUT` is `90m`, not 180s
  (confirmed against the actual running process's environment, not
  documentation).
- The codex-cli adapter's own interactive-turn timeout
  (`multi-llm-provider-go/pkg/adapters/codexcli/codexcli_interactive_adapter.go`)
  defaults to `0` (unbounded) by explicit design — the code comment reads
  "Workflow/background callers own their execution deadline; the adapter
  should not cancel a still-running tmux coding agent before the outer
  workflow timeout." The same file's prompt-wait ceiling
  (`defaultCodexPromptMaxWait`) is `90 * time.Minute`, confirming cold CLI
  startup is expected to legitimately take minutes, not seconds.
- No "180" timeout constant exists anywhere in this repo or in the sibling
  `mcpagent`/`multi-llm-provider-go` repos tied to search or shell execution.

`search_web_llm(codex-cli)` calls `GenerateContent` with no interactive
session ID, so every call gets a fresh bounded tmux session
(`codex-bounded-<ts>-<rand>`) — a real CLI process launched fresh, every
time, with no reuse across calls. Nothing in this repo enforces or exposes
a caller-facing timeout around the resulting turn. The 180-second deadline
in all three findings' `strategy_today.json`/`proposed_primitives` records
is the *workflow's own* self-chosen research budget, not anything this
platform imposes or could shorten/lengthen on the workflow's behalf.

Precision matters here: the evidence measures the complete search turn
(process start, reasoning, searching, and response generation together)
exceeding 180 seconds on an endpoint that was otherwise healthy. It does
not isolate cold-start as the specific dominant contributor, and it only
covers `codex-cli` — the other CLI-backed search providers (`cursor-cli`,
`claude-code`, `pi-cli`) were not separately measured and should not be
assumed to share the same "180s is too short" conclusion without evidence.

This is the same boundary shape as PLAT-224/PLAT-232, but the missing piece
here is different: not a third-party binary passthrough, but an undocumented
latency characteristic of a platform-provided tool that no caller had any
way to budget for correctly.

## Fix

Added two notes to `workspace-media-tools.md` (the `builder-reference`
skill's full tool reference, loaded by every session holding
`search_web_llm`): one inline on the `search_web_llm` bullet, one under
"Common mistakes" naming the exact failure shape (~180s deadline killing a
healthy-but-slow `codex-cli` turn) and the corrective action (widen the
budget, don't conclude the provider is broken from one timeout). Both are
scoped to `codex-cli` specifically and to "no platform-imposed 180-second
timeout" rather than "no timeout at all" or a blanket cold-start claim
across every CLI-backed provider, per the independent review below.

## Verification

`go build ./...` and `go test ./cmd/server/guidance/...` pass (template
renders clean). No Go code changed — like PLAT-223/224/232/233, this is a
guidance-only fix because there is no platform-owned timeout to change.

## Reverify

No live step has loaded this corrected guidance through the deployed server
yet. Reverify by observing whether a future codex-cli `search_web_llm` call
in a Track-A-style research step is given a multi-minute budget instead of
~180s, and whether that reduces reliance on the WebSearch fallback for this
provider.

## Independent review (2026-08-29)

The root classification and the three SQLite dispositions are supported:
the retained findings all used a workflow-authored 180-second deadline, the
live server's outer `TOOL_EXECUTION_TIMEOUT` is 90 minutes, the tool schema
does not expose a timeout parameter, and the codex-cli adapter does not add a
provider-level 180-second turn timeout. All three cited findings are recorded
`resolved` with PLAT-234 as their resolution.

The review identified two wording corrections, both now applied to
`workspace-media-tools.md`:

1. The original guidance said the backend imposed no timeout. The confirmed
   90-minute outer tool ceiling is still a backend timeout; the corrected
   conclusion is narrower: **there is no platform-imposed 180-second
   timeout**.
2. The retained evidence measures the complete codex-cli search turn. It does
   not separately measure process startup, model reasoning, web search, source
   retrieval, and final response generation. Therefore it supports saying a
   **fresh codex-cli search turn can exceed 180 seconds**, but not that cold
   start alone regularly consumes more than 180 seconds before searching
   begins. The same latency claim should not be generalized to cursor-cli,
   claude-code, or pi-cli without equivalent measurements.

These guidance-accuracy corrections do not reverse the issue disposition. The
immediate mitigation remains appropriate: callers should not interpret one
workflow-owned 180-second deadline as a provider outage, and should use a
larger budget or the sanctioned fallback when that latency is unacceptable.

**Corrections applied (2026-08-29):** `workspace-media-tools.md` and the
"Fix"/"Investigation" sections above were reworded to say "no
platform-imposed 180-second timeout" (not "no timeout"), to attribute the
observed >180s duration to the full search turn rather than cold start
specifically, and to scope the claim to `codex-cli` rather than every
CLI-backed provider. `go build ./...` and `go test
./cmd/server/guidance/...` pass after the edit.

**Second correction applied (2026-08-29):** the guidance's causal framing
was too absolute — "a timeout here means the caller's own deadline was too
tight, not that the provider is broken" reads as a binary claim covering
every future timeout, when the underlying evidence is a single day's
findings (three occurrences, same session) showing one healthy-but-slow
turn, not proof that a timeout can *never* indicate a real provider
problem. Reworded both the inline `search_web_llm` note and the "Common
mistakes" bullet: a timeout no longer *proves* the budget was too
tight — it "doesn't by itself prove the provider is broken" and "can
simply be a too-tight budget," with an explicit escalation path (widen the
budget and retry; treat *persistent* timeouts across multiple widened
attempts as evidence of a real problem, not a budget one). `go build
./...` and `go test ./cmd/server/guidance/...` pass.
