[← platform / coding-agent-bridge](index.md)

# PLAT-024 — tool-error markers omit the tool name

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | mcpagent/multi-llm-provider tool-error attribution and logging |

| Coordination | Value |
|---|---|
| Assigned agent | `Claude Code` |
| Follow-up reviewer/fixer | `Codex` |
| Ticket state | `implemented` |
| Execution order | `C — after A, B, and E` |
| Last synchronized | `2026-08-04` |

> Claim this ticket as `in_progress` before implementation. Update this
> fragment during active work; synchronize the shared index at handoff.

- **Priority:** P2
- **Owner:** mcpagent/multi-llm-provider tool-error attribution and logging
- **Evidence:** 35 of 90 sampled `[TOOL_ERROR] CLI tool payload failure`
  markers have an empty `tool=` field; attribution requires regexing the inner
  JSON envelope.
- **Source:**
  [what_the_runtime_tells_an_agent_about_itself.md](../../../what_the_runtime_tells_an_agent_about_itself.md)
- **Problem:** runtime behavior is unchanged, but failure counts and ownership
  cannot be measured reliably from the canonical marker.
- **Implementation boundary:** populate the tool name from the structured call
  identity before formatting the marker, with a narrow envelope fallback only
  where the adapter genuinely omitted it.
- **Implementation (2026-08-04, mcpagent `d1eca1f`):** two-layer recovery, in
  priority order. (1) `streamingManager` records each `ToolCallStart`'s name
  against its `ToolCallID`; a `ToolCallEnd` with an empty name looks itself up
  by the same ID — authoritative, since it is the same call reporting its own
  name. (2) `toolerr.ToolNameFromResult`: a narrow regex matching only the one
  harness-generated phrase (`tool execution failed: layer=\S+ tool=(\S+)`)
  present in every unattributed marker sampled this session, tried only when
  no start event was recorded. Deliberately this narrow rather than a general
  `tool=` scanner: a broader pattern risks lifting an unrelated nested tool
  name and misattributing the marker to it. When `chunk.ToolName` is already
  non-empty, the transport wrapper's own name always wins over anything
  mentioned inside its own result. If neither source proves a name, the
  marker now says `unknown` explicitly rather than going out blank.
- **Verification:** four table-driven streaming tests (structured recovery,
  envelope fallback, explicit unknown, and a dedicated misattribution guard
  using the exact nested-failure shape from the existing
  `TestStreamingManagerPromotesNestedCLIToolFailureToErrorEvent`), plus five
  direct `toolerr.ToolNameFromResult` cases including two proving the regex
  does not match ordinary prose or an unrelated `tool=` assignment. Verified
  three of the four streaming cases fail without the fix.
- **Regression tests:**
  `TestStreamingManagerRecoversToolNameForUnattributedErrorEvent` in
  `agent/llm_generation_streaming_test.go`; `TestToolNameFromResult` in
  `toolerr/toolerr_test.go`.
- **Independent follow-up (Codex, 2026-08-04):** the first implementation
  recovered the name only inside the failure branch, after calling
  `CanonicalFailureForTool` with the empty end-chunk name. That bypassed the
  intentional suppression for reporting tools such as `query_workflow_db` and
  could promote legitimate domain rows into false tool errors. The effective
  name is now resolved before classification and reused for logging, events,
  CLI history, and callbacks. `TestStreamingManagerRecoversToolNameBeforeFailureClassification`
  proves an unnamed end chunk for `query_workflow_db` remains a successful,
  correctly named result when its payload merely describes a failed record.
- **Acceptance:** table-driven adapter/envelope fixtures always emit a stable
  tool name when one exists, explicitly emit `unknown` when none can be proven,
  and never misattribute a nested tool error to its transport wrapper. Met by
  direct fixture test; a real cross-workflow log scan confirming the empty
  `tool=` rate has dropped has not been run, so this stays `implemented`
  rather than `done`.
