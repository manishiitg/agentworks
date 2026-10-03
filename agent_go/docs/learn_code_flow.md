# Scripted and Agentic Step Execution

This is the current source of truth for scripted workflow execution.

## Source layout (PLAT-298)

Read `workflow.json.code_layout_version` first. New workflows have version 1:

- Canonical source: `code/<step-id>/main.py`; shared packages anywhere in `code/`.
- The controller executes that source directly; no run-folder copy or copy-back.
- `WORKFLOW_CODE_ROOT` is the absolute import root; `PYTHONPATH` includes it and
  `WORKFLOW_CODE_DEPS` (`.sandbox-cache/python-packages`). Install dependencies
  with `python3 -m pip install --target "$WORKFLOW_CODE_DEPS" ...`, not a separate
  interpreter/venv that the controller does not use.
- Builder tests use `execute_step(fast_path_only=true)`. Authoring/repair turns
  return to the controller for execution through the same shell runner. A failed
  script cannot be replaced by hand-made successful output in this layout.
- Step source and unlocked shared helpers are writable within the workflow;
  locked code is read-only. Execution outputs remain in `STEP_OUTPUT_DIR`.
- Statistics live beside the source in `code/<step-id>/script_metadata.json`.
  Entry-point hashes track entry revisions; they are not whole-tree snapshots.
- Imports and clones preserve the source manifest's version. Manifest capability
  updates preserve the existing version; no implicit migration is performed.

Absent/zero versions retain the legacy `learnings/<step-id>/main.py` layout and
copy/save-back behavior described below. Directory existence never changes the
version. All source, dependencies, database assets and retained run artifacts
are under the persistent workspace docs root, not a release directory. Run
retention can still prune old run outputs; durable reporting media belongs in
`db/assets/`. Disk persistence does not replace a backup/restore policy.

A workflow step's execution model is decided by its plan `type` alone:

- `regular` — a **scripted** step. Its work is a reusable `learnings/{step-id}/main.py` that is tried on every run before any LLM turn, and repaired by the LLM when it fails.
- `message_sequence` — an **agentic** (conversational) step. The LLM acts each turn; no persistent script is saved. It may still use code execution for the current run.

There is no separate mode field. The former `agent_configs.declared_execution_mode` (`"scripted"` / `"agentic"`, earlier `learn_code` / `code_exec`) was retired by workflow contract v1.0.38–1.0.39 (PLAT-287): it was implied on one type and forbidden on the other, so it could only ever drift from the plan (PLAT-280 was exactly such a stray declaration). Workflows migrated before v1.0.39 may still carry the key in `step_config.json`; it is ignored once the plan type is explicit and is stripped by the v1.0.39 upgrade.

## Overview

| Plan `type` | Execution | Script persistence |
|---|---|---|
| `regular` | Scripted: saved-script fast path, then LLM generation/repair | `learnings/{step-id}/main.py` is saved back after each successful repair |
| `message_sequence` | Agentic: LLM turns; code execution for the run if `use_code_execution_mode` is on | none — a leftover `learnings/{step-id}/main.py` is stale debt and should be deleted |

`use_code_execution_mode` (`agent_configs`) is independent of the type: it decides whether the agent writes and runs code through the bridge at all. A `regular` step always has it forced on — the agent needs the tool index and `get_api_spec` to write `main.py`. A `message_sequence` can turn it on for ad-hoc scripting without becoming scripted.

## Choosing and changing the model

Prefer a `regular` (scripted) step for stable, deterministic work:

- structured data transforms
- report building
- deterministic validation logic
- fixed API call sequences
- repeatable file processing
- browser flows with stable selectors and predictable navigation

Prefer a `message_sequence` when the logic changes from run to run:

- exploratory browser work
- adaptive investigations
- tasks where the agent must improvise heavily based on page state or live results
- one-off data collection patterns that are unlikely to stabilize into a reusable script

Tools:

- `add_scripted_step` creates a `regular` step; `add_message_sequence_step` creates a sequence.
- `change_step_type(step_id, target_type="scripted"|"message_sequence", reason)` moves an existing step between the two in place — same id, description, dependencies, validation and position — and records the change (with the reason) in the plan changelog. To scripted, its conversational items are dropped and `learnings/{step-id}/main.py` must then be written with `update_scripted_step(code=...)`; to a sequence, one execute-and-verify item is synthesized and `lock_code` is cleared.
- `update_step_config(...)` no longer accepts an execution-mode field; it still owns `use_code_execution_mode`, `lock_code`, tiers, models and access flags.
- `run_saved_main_py(step_id, group_id?)` is valid only for `regular` steps, because only those have a persistent saved-script fast path.

## Configuration

### Scripted step

```json
{
  "id": "step-id",
  "type": "regular",
  "title": "Fetch and normalize prices",
  "description": "..."
}
```

with its script at `learnings/step-id/main.py`. The matching `step_config.json` entry carries only tier/model/tool/access settings.

### Agentic step

```json
{
  "id": "step-id",
  "type": "message_sequence",
  "title": "Judge the day's setups",
  "items": [ ... ]
}
```

Add `"use_code_execution_mode": true` to its `agent_configs` when the turns should script against the bridge for the current run.

## Shared Architecture

Both models use the same bridge-based execution model.

The execution agent does not call most MCP tools directly. Instead it:

1. Uses `get_api_spec(tool_name)` to inspect a tool's HTTP contract.
2. Uses `execute_shell_command` to write and run Python or shell code.
3. Calls per-tool HTTP endpoints such as:
   - `POST /tools/mcp/{server}/{tool}`
   - `POST /tools/custom/{tool}`

Core env vars injected into scripted runs include:

- `MCP_API_URL`
- `MCP_API_TOKEN`
- `STEP_OUTPUT_DIR`
- `STEP_EXECUTION_DIR`
- resolved `SECRET_*` and `VAR_*` values

This is the same bridge used by CLI-style providers that require HTTP tool routing.

## Structured Python output

Sandboxed Python commands can publish structured data without managing output
files. The shared runner supplies this helper without a package installation:

```python
from agentworks_output import set_output

set_output({"text": extracted_text})
```

The helper serializes JSON with non-finite numbers rejected, then atomically
persists result.json under the assigned STEP_OUTPUT_DIR. Persistence errors
raise. Multiple successful calls replace the value. Configure the existing
context_output as result.json when another step consumes it. Relay authored
messages use references such as {{steps.extract_pdf_text.output.text}}.
Stdout remains a log; existing direct output-file writes remain supported.

## Mode Resolution and Precedence

The execution loop resolves the model in two layers:

1. Is the step scripted? — yes iff its plan type is `regular`.
2. Is code execution enabled at all?
   - step config `use_code_execution_mode`
   - otherwise workflow/preset default
   - a scripted step forces it on

Additional behavior:

- Step config overrides workflow default.
- Workflow default no longer auto-enables code execution globally.
- Provider-specific auto-enable is handled per agent for CLI providers such as `claude-code`, `pi-cli`, and `codex-cli`.
- Transitional: a `regular` step whose not-yet-stripped `step_config.json` entry still says `declared_execution_mode: "agentic"` (a legacy agentic regular step on a workflow below contract v1.0.38) is run as a `message_sequence` until the v1.0.38 upgrade makes its type explicit. After v1.0.39 no such entry exists.

## Scripted (`regular`) Flow

A scripted step adds persistence and a saved-script fast path on top of normal code execution.

### Persistent paths

| Path | Purpose |
|---|---|
| `learnings/{step-id}/main.py` | Canonical saved script for future runs |
| `learnings/{step-id}/diffs/` | Diffs between saved versions |
| `execution/{step-path}/code/main.py` | Per-run working copy that the LLM edits |
| `execution/{step-path}/code/fix-diffs/` | Diffs between repair iterations in the same run |
| `execution/{step-path}/` | Output folder for artifacts validated by the step |

### Fast path

Before the LLM runs, the controller attempts `tryRunSavedLearnCodeScript(...)`.

High-level flow:

1. Check whether `learnings/{step-id}/main.py` exists.
2. Run static review on the saved script.
3. Copy the saved script into `execution/{step-path}/code/` when needed.
4. Clean the step output directory while preserving `code/`.
5. Run `python3 main.py` with workflow env vars and step arguments.
6. Run pre-validation on outputs.
7. If script execution and validation pass, finish with zero LLM tokens for that run.

### Static review before fast path

The controller reviews the saved script before trusting it. It rejects fast path when it sees patterns such as:

- hardcoded execution paths
- hardcoded fallbacks for required env vars
- sibling-step path hacks
- writes outside the managed step output area
- direct writes into system-managed directories like `knowledgebase/` or `learnings/`

When static review fails, the system skips the fast path and falls back to LLM repair/generation.

### LLM generation and repair

If fast path fails or no saved script exists:

1. The execution agent writes or repairs `execution/{step-path}/code/main.py`.
2. The controller reruns pre-validation.
3. On failure, it starts a repair loop.

Repair loop behavior:

- up to 3 fix iterations (configurable via `LearnCodeMaxFixIter`)
- fresh Tier 1 (High) repair agent each iteration
- feedback message includes: task description, pointer to current `main.py` on disk (not inlined), static code review issues, last execution output + exit code, and attempt counter
- validation details are intentionally omitted from feedback to prevent the LLM from fabricating outputs that match the schema
- diffs are written under `execution/{step-path}/code/fix-diffs/`

### Save-back behavior

After execution, the controller saves the latest script back into `learnings/{step-id}/` unless the script has syntax errors or `lock_code` freezes the saved script.

This means a scripted step is not only a fast path. It is also the persistent script-maintenance path.

### Learning access vs code lock

Learning writes use an access level; saved code has a separate lock:

| Setting | Controls | Effect |
|---|---|---|
| `learnings_access` (`"read"\|"read-write"\|"none"`) | SKILL.md read/write at a coarse level | Default `"read"` — step sees `_global/SKILL.md` but doesn't contribute. `"read-write"` (+ non-empty `learning_objective`) opts into contribution. `"none"` opts out of both. Mirrors `knowledgebase_access`. |
| `lock_code: true` | main.py | Prevents LLM-rewritten scripts from being saved back to learnings. Skips the fix loop entirely (falls back directly to per-run code execution). |

When `lock_code: true` is set on a step:

- **Fast path**: Saved script is still copied from learnings to execution and run normally
- **Fix loop**: Skipped entirely (`maxFixIter = -1`) — no repair agents are created, no tokens spent on fixes that would be discarded
- **Save-back**: Blocked — the LLM's rewritten script is NOT copied back to learnings
- **Fallback**: Falls through directly to per-run code execution (tools directly, no main.py)
- **Metadata**: `script_metadata.json` is still updated (run history, failure patterns) for observability

This means a locked script that keeps failing will repeat the same failure every run. The user must manually fix `learnings/{step-id}/main.py` or set `lock_code: false` to let the system fix it.

To force a complete rewrite: delete `learnings/{step-id}/main.py` (not the execution copy), then run `execute_step`. The LLM will generate fresh.

### Fallback after repair exhaustion

If the repair loop is exhausted (or skipped due to locked learnings), the controller disables persistent scripted mode for the remaining outer retries and continues in plain per-run code execution.

That fallback is important:

- the saved script is the preferred stable path
- per-run code execution is the recovery path when the saved script is not currently salvageable within the repair budget

## Agentic (`message_sequence`) Flow with Code Execution

A sequence with `use_code_execution_mode` on uses the same bridge and env model, but it does not rely on a persistent saved script.

Behavior:

- the agent writes and runs code for the current step run
- no saved `learnings/{step-id}/main.py` fast path is attempted
- no `run_saved_main_py` support
- the step still benefits from script-based batching, loops, parsing, and multi-tool orchestration

This is the right model when scripting is useful but persistence would create more churn than value.

## Prompting Expectations for Scripted Steps

The controller prompt for scripted execution expects:

- outputs to be written under `STEP_OUTPUT_DIR`
- script working files to live under `STEP_EXECUTION_DIR` / `code/`
- variables to be passed through env vars or runtime args, not hardcoded
- diagnostic output to go to stdout/stderr so repair loops can reason over failures

For a scripted (`regular`) step, the prompt also emphasizes:

- maintaining a reusable `main.py` and repairing it incrementally
- **no fabricated data**: every output value must trace to a real data source (MCP tool call, API response, or input file)
- **browser automation rules**: snapshot-first agent_browser interaction, fresh refs, and durable persisted selectors
- **tool discovery**: call `get_api_spec` before writing browser/MCP code to learn exact parameter schemas instead of guessing
- `script_metadata.json` is referenced by path (not inlined) so the LLM reads it on demand

## When to Use Which

Choose a `regular` (scripted) step when:

- the task shape is stable
- the script should improve over time
- you want future runs to be cheap and fast
- you want a reviewable `main.py` artifact in learnings

Choose a `message_sequence` when:

- the task shape changes too much between runs
- persistence would encode brittle assumptions
- the agent needs exploratory or dynamic behavior each time

## Operational Notes

- CLI providers may force code execution behavior because they route tools through the HTTP bridge.
- A `regular` step forces `UseCodeExecutionMode = true` regardless of provider — this ensures the agent gets the tool index and `get_api_spec` virtual tool for proper tool discovery when writing `main.py`.
- Learning agents are still separate from execution agents; code execution mode mainly affects execution-time tool access and scripting behavior.
- `learn_code_script_execution` events exist specifically for saved-script runs and repair visibility in the UI.
- `error_summary` in `script_metadata.json` run records is stored in full (not truncated). `error_snippet` in `last_failure` is capped at 2000 chars for prompt inclusion.

## Key Files

| File | Role |
|---|---|
| `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go` | Main execution loop, fast-path invocation, repair loop, fallback handling |
| `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_learn_code.go` | Saved-script execution, static review, save-back, diff capture |
| `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_message_sequence.go` | Sequence runtime; the type-based scripted/agentic decision and the transitional legacy-agentic shim |
| `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/change_step_type_tool.go` | `change_step_type`: in-place conversion between the two models |
| `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go` | Workshop guidance, `run_saved_main_py`, `update_step_config` |
| `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/step_config.go` | Applies step config (tiers, models, tools, access, `use_code_execution_mode`, `lock_code`) |
| `agent_go/cmd/server/server.go` | Per-tool HTTP endpoints and bridge env setup |
| `agent_go/pkg/workspace/execute_shell_command.go` | Shell execution guardrails and tool-routing constraints |

## Related Docs

- [Step Config Specification](step_config_format_specification.md)
- [Learning Architecture](learning_architecture.md)
