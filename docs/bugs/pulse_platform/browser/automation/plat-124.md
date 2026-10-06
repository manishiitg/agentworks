[← platform / browser-automation](index.md)

# PLAT-124 — an oversized browser snapshot returned nothing, and its spill was unreadable

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | browser |
| Area | automation |
| Summary | P1 — on Linux, a fresh workflow without `tool_output_folder` |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implementation repaired; live reverify pending` — the read grants shipped, but a 2026-08-21 Linux run proved the granted bridge spill directory was not created before Landlock compiled the policy |
| Last synchronized | `2026-08-21` |

- **Priority:** P1 — on Linux, a fresh workflow without `tool_output_folder`
  cannot initialize the sandbox, so both shell and browser execution fail before
  the step can read inputs or produce outputs.
- **Owner:** `pkg/browser/executor.go` (snapshot inline cap),
  `controller_message_sequence.go` + `controller_agent_factory.go` (folder guard
  read paths)
- **Related:** [PLAT-073](../../goals/pulse-governance/plat-073-remaining-board.md)) cluster F (`dd9ede3c`)
  granted `tool_output_folder` to `setupExecutionFolderGuard` for exactly this
  reason; the two parallel guard builders were never updated and nothing pinned
  the parity.

## Two defects that compounded

Confirmed live 2026-08-17, workflow `confida-login`, group `confida-staging`,
step `execute-browser-and-capture-apis`.

**1. The snapshot cap discarded the evidence.** `maxInlineSnapshotOutputRunes`
is 24000. Four snapshots came back at ~30,365 runes — 26% over. The handler
returned `("", err)`:

```
SNAPSHOT_RESULT_TOO_LARGE: agent-browser returned 30365 runes (30365 bytes),
exceeding the 24000-rune inline safety limit. ... No snapshot content was
returned inline ... Retry deliberately with --selector <css> ... or --depth <n>
```

The tree was thrown away. The agent then had to guess a CSS selector for a page
it had not been allowed to see, on a step where turns were already the binding
constraint.

**2. The spill it was pointed at was unreadable.** Separately, when a bridge
tool result passes its inline cap, `mcpbridge` persists the full output to
`MCP_TOOL_OUTPUT_DIR` (`<workspace-root>/tool_output_folder`) and returns
`"full output saved to: <path>"`. But this step's folder guard granted:

```
read=[.../execution, soul, builder, db, knowledgebase, learnings/_global,
      learnings/execute-browser-and-capture-apis, ~/Downloads]
```

No `tool_output_folder`. So the agent was handed a path it was forbidden to
open, and got the dead-end:

```
access denied: <...>/tool-results/mcp-api-bridge-agent_browser-<n>.txt is a
truncated tool-result spill written by your coding CLI, outside every workspace
root. It cannot be read, and no workspace-relative path points at one.
```

`setupExecutionFolderGuard` had granted `tool_output_folder` since PLAT-073
cluster F, and its comment names *"a large agent_browser snapshot"* as the
motivating case. But `setupMessageSequenceFolderGuard` and
`setupKBUpdateFolderGuard` are separate parallel implementations that never
received it. Measured across the run: every `msgseq-*` session lacked it; only
`session-group-*` and generic `sub-exec-eval-*` sessions had it.

## 2026-08-21 Video Studio recurrence — a granted path that did not exist

Video Studio's `shortform-script` message-sequence step failed before reading
its valid `shortform-brief.md` dependency. Both `execute_shell_command` and
`agent_browser` returned `SANDBOX_UNAVAILABLE (missing tool_output_folder)`.
The workflow root had no `tool_output_folder` because `mcpbridge` previously
created it lazily only when an oversized result was first persisted.

The folder-guard parity repair above was therefore necessary but insufficient:
Landlock compiles its ruleset before the first bridge call and correctly refuses
to authorize a nonexistent path. A read grant cannot create its own target.

The shared repair belongs in `mcpagent`, where `MCP_TOOL_OUTPUT_DIR` is owned.
`buildBridgeMCPConfig` now creates the directory with mode `0700` when assigning
the environment variable, before any coding CLI or guarded tool can run. Setup
fails with a contextual error if the directory cannot be created; the sandbox
continues to fail closed rather than silently dropping the grant. Tests prove
both creation and the failure case. Live acceptance requires retrying the
Video Studio script step after deploying the rebuilt agent binary.

## Why the original design was right, and what was kept

The cap's own comment is the constraint any fix must respect:

> *"Snapshot output above this threshold must fail explicitly rather than being
> silently narrowed or truncated: the agent, not the runtime, chooses which
> evidence surface to inspect next."*

That is a real correctness hazard, not defensiveness. A **silently** truncated
accessibility tree is indistinguishable from a page where the element is
genuinely absent — and a QA step that records "control not found" from a cut-off
tree files a false negative. Raising or removing the cap is also wrong: ~30k
runes is ~7.5k tokens, and this session made 290 `agent_browser` calls; the cap
is doing real work on context.

So the fix keeps the principle and changes only what happens *at* the cap.

## Fix

**Snapshot handling** (`handleOversizedSnapshot`, replacing
`snapshotResultTooLargeError`):
- Return the first 24000 runes behind an unmissable banner —
  `SNAPSHOT_TRUNCATED` + *"THIS TREE IS INCOMPLETE. An element missing from the
  text below may still exist on the page — do NOT record it as absent"* — so the
  false-negative hazard is addressed by being louder, not by withholding.
- Offer three explicit next moves: `--selector`, `--depth`, or `--full-snapshot`.
- **`--full-snapshot`** is a new AgentWorks-level opt-in (stripped before the CLI
  runs, since `agent-browser` would reject it as an unknown subcommand). It
  returns the whole tree inline, letting the agent knowingly pay the context cost
  — the same "agent decides" principle the cap was written to protect. Anything
  past the bridge's own 128KB result cap is spilled and is now readable.

**Folder guard:** granted `tool_output_folder` (read-only) in
`setupMessageSequenceFolderGuard` and `setupKBUpdateFolderGuard`, matching
`setupExecutionFolderGuard`.

**Tests:** `TestOversizedSnapshotReturnsTruncatedHeadWithIncompletenessBanner`,
`TestFullSnapshotFlagReturnsWholeTreeAndIsStrippedFromCLIArgs`, and
`TestEveryFolderGuardBuilderGrantsToolOutputFolder` — the last one pins parity
across all three builders so this class of drift fails a test next time.

## Not fixed here

- **The three guard builders remain three implementations.** `tool_output_folder`
  is now consistent and pinned, but `setupMessageSequenceFolderGuard` still omits
  `planning/` and the run folder that `setupExecutionFolderGuard` grants — each
  for a documented reason (a step judging its own scope; a CDP step that
  *"burned four calls"* walking to its own output path). Whether message_sequence
  steps should have those too was not decided here. Consolidating the three into
  one builder with explicit per-caller options is the real repair.
- **`--full-snapshot` has had no live exercise.** Unit-tested only; no real
  browser run has requested it yet.

## Acceptance

- An oversized snapshot returns usable tree content plus an explicit
  incompleteness banner, and never costs a blind retry.
- A step that receives `"full output saved to <path>"` can read that path,
  from any of the three guard builders.
- `--full-snapshot` returns the complete tree and never reaches the
  `agent-browser` CLI as an argument.
- Verified on a live browser run, not only by unit test.
