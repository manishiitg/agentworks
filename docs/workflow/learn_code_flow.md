# Learn Code and Code Execution Modes

This is the current source of truth for scripted workflow execution.

> **Update 2026-10-04 (PLAT-436): runs never self-heal.** In any run (schedule,
> webhook, `run_full_workflow`, a route called by an agent) a scripted step runs
> its saved `main.py` and nothing else. A missing or failing script fails the step
> with its real error and Pulse reports it; no LLM generates, repairs or stands in
> for the script, nothing is saved back, and there is no `code_exec` fallback.
> Generation, repair and save-back below happen only when the Builder runs the
> step itself with `execute_step` (Workshop mode, not `fast_path_only`, not a
> scheduled session). Decision: `docs/DECISIONS.md`, ticket
> `bugs/pulse_platform/step-execution/plat-436.md`.

`learn_code` and `code_exec` are not separate systems. They are two execution modes built on the same code-execution foundation:

- `code_exec`: the agent writes and runs code for the current run only.
- `learn_code`: the agent writes and maintains a reusable `main.py` that is retried on future runs before the LLM is called.

## Overview

At the workflow step level, scripted execution is controlled by two fields in `agent_configs`:

- `use_code_execution_mode`
- `declared_execution_mode`

The current behavior is:

| Setting | Effect |
|---|---|
| `use_code_execution_mode: false` | Step uses normal direct-tool execution, not scripted code execution |
| `use_code_execution_mode: true` + `declared_execution_mode: "code_exec"` | Step uses ephemeral code execution only |
| `use_code_execution_mode: true` + `declared_execution_mode: "learn_code"` | Step uses persistent scripted execution with saved `main.py` fast path |

Important implementation detail:

- `learn_code` is detected only when `declared_execution_mode == "learn_code"`.
- `code_exec` is the fallback mode whenever scripted execution is enabled but the step is not explicitly marked as `learn_code`.
- `syncDeclaredExecutionModeConfig()` forces `use_code_execution_mode=true` when `declared_execution_mode` is `learn_code` or `code_exec`.

That means the recommended workflow config is to set both fields explicitly for every scripted step.

## Recommended Usage

Default to `code_exec`. Promote a workflow step to `learn_code` only when all gates are satisfied:

- the user explicitly asked for scripted/learn-code execution
- the behavior is highly deterministic, with stable inputs, tools, output contract, and little/no per-instance judgment
- there is broad stability evidence, normally 10+ successful runs across the relevant variable groups/scenarios with eval or metric evidence still at target

Good `learn_code` candidates after those gates:

- structured data transforms
- report building
- deterministic validation logic
- fixed API call sequences
- repeatable file processing
- browser flows only when the user explicitly requested scripted browser automation and 10+ scenario-covering runs prove durable selectors and predictable navigation

Use `code_exec` when the step still benefits from scripting, but the exact logic changes from run to run:

- exploratory browser work
- adaptive investigations
- tasks where the agent must improvise heavily based on page state or live results
- one-off data collection patterns that are unlikely to stabilize into a reusable script

## Configuration

### Preferred `learn_code` config

```json
{
  "id": "step-id",
  "agent_configs": {
    "use_code_execution_mode": true,
    "declared_execution_mode": "learn_code",
    "declared_execution_mode_reason": "Stable scripted flow with reusable Python"
  }
}
```

### Ephemeral `code_exec` config

```json
{
  "id": "step-id",
  "agent_configs": {
    "use_code_execution_mode": true,
    "declared_execution_mode": "code_exec",
    "declared_execution_mode_reason": "Adaptive step that changes between runs"
  }
}
```

### Workshop defaults

Workshop guidance treats `code_exec` as the default. `learn_code` is an opt-in promotion after explicit user request plus deterministic behavior and 10+ scenario-covering successful runs. The workshop tools also expose:

- `update_step_config(...)`
- `run_saved_main_py(step_id, group_id?)`

`run_saved_main_py` is valid only for `learn_code` steps, because only those steps have a persistent saved-script fast path.

## Shared Architecture

Both modes use the same bridge-based execution model.

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

## Mode Resolution and Precedence

The execution loop resolves mode in two layers:

1. Determine whether the step is in persistent scripted mode:
   - `isScriptedExecutionModeConfig(cfg)` returns true only for `declared_execution_mode == "learn_code"`.
2. Determine whether code execution is enabled at all:
   - step config `use_code_execution_mode`
   - otherwise workflow/preset default
   - then `learn_code` forces code execution on

Additional behavior:

- Step config overrides workflow default.
- Workflow default no longer auto-enables code execution globally.
- Provider-specific auto-enable is handled per agent for CLI providers such as `claude-code`, `pi-cli`, and `codex-cli`.

## `learn_code` Flow

`learn_code` adds persistence and a saved-script fast path on top of normal code execution.

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

### Authoring and repair (the Builder's own `execute_step` only)

If the saved script fails or does not exist and the Builder is running the step
itself (see the update above; a run fails the step instead):

1. The execution agent writes or repairs `execution/{step-path}/code/main.py`.
2. The controller reruns pre-validation.
3. On failure, it starts a learn-code repair loop.

Repair loop behavior:

- up to 3 fix iterations
- fresh Tier 1 (High) repair agent each iteration
- feedback message includes: task description, pointer to current `main.py` on disk (not inlined), static code review issues, last execution output + exit code, and attempt counter
- validation details are intentionally omitted from feedback to prevent the LLM from fabricating outputs that match the schema
- diffs are written under `execution/{step-path}/code/fix-diffs/`

### Save-back behavior (the Builder's own `execute_step` only)

After a Builder-run learn-code execution, the controller saves the latest script
back (`code/{step-id}/`, or `learnings/{step-id}/` in the legacy layout) unless the
script has syntax errors or `lock_code` freezes the saved script. A run never saves
anything back.

### Learning access vs code lock

Learning writes use an access level; saved code has a separate lock:

| Setting | Controls | Effect |
|---|---|---|
| `learnings_access` (`"read"\|"read-write"\|"none"`) | SKILL.md read/write at a coarse level | Default `"read"` — step sees `_global/SKILL.md` but doesn't contribute. `"read-write"` (+ non-empty `learning_objective`) opts into contribution. `"none"` opts out of both. Mirrors `knowledgebase_access`. |
| `lock_code: true` | main.py | Stops the Builder's own `execute_step` from repairing or rewriting the script. Runs are unaffected: they never rewrite a script whether or not it is locked. |

When `lock_code: true` is set on a step:

- **Builder `execute_step`**: the saved script runs; the repair loop is skipped (`maxFixIter = -1`), no repair agents are created, and nothing is saved back.
- **Runs**: unchanged. The saved script runs and a failure fails the step.
- **Metadata**: `script_metadata.json` is still updated (run history, failure patterns) for observability.

A script that keeps failing repeats the same failure every run until the Builder
fixes `main.py` (set `lock_code: false` first if it is locked, then `execute_step`).

To force a complete rewrite: delete the saved `main.py` (not the execution copy),
then run `execute_step` as the Builder. The LLM will generate fresh.

### After repair exhaustion (the Builder's own `execute_step` only)

If the repair loop is exhausted, the Builder's `execute_step` continues in plain
`code_exec` mode for its remaining retries so the Builder can see what the agent
can do. This is an authoring aid; a run never falls back to `code_exec`.

## `code_exec` Flow

`code_exec` uses the same bridge and env model, but it does not rely on a persistent saved script.

Behavior:

- the agent writes and runs code for the current step run
- no saved `learnings/{step-id}/main.py` fast path is attempted
- no `run_saved_main_py` support
- the step still benefits from script-based batching, loops, parsing, and multi-tool orchestration

This is the correct mode when scripting is useful but persistence would create more churn than value.

## Prompting Expectations for Scripted Steps

The controller prompt for scripted execution expects:

- outputs to be written under `STEP_OUTPUT_DIR`
- script working files to live under `STEP_EXECUTION_DIR` / `code/`
- variables to be passed through env vars or runtime args, not hardcoded
- diagnostic output to go to stdout/stderr so repair loops can reason over failures

For `learn_code`, the prompt also emphasizes:

- maintaining a reusable `main.py` and repairing it incrementally
- **no fabricated data**: every output value must trace to a real data source (MCP tool call, API response, or input file)
- **browser automation rules**: snapshot-first agent_browser interaction, fresh refs, and durable persisted selectors
- **tool discovery**: call `get_api_spec` before writing browser/MCP code to learn exact parameter schemas instead of guessing
- `script_metadata.json` is referenced by path (not inlined) so the LLM reads it on demand

## When to Use Which Mode

Choose `learn_code` only when:

- the user explicitly asked for learn_code/scripted persistence
- the task shape is highly deterministic
- 10+ successful runs cover the relevant scenarios/groups
- eval/metric evidence is still at target
- you want future runs to be cheap and fast and accept the risk of freezing assumptions into `main.py`

Choose `code_exec` when:

- the task shape changes too much between runs
- persistence would encode brittle assumptions
- the agent needs exploratory or dynamic behavior each time

## Operational Notes

- CLI providers may force code execution behavior because they route tools through the HTTP bridge.
- `learn_code` steps force `UseCodeExecutionMode = true` regardless of provider — this ensures the agent gets the tool index and `get_api_spec` virtual tool for proper tool discovery when writing `main.py`.
- Learning agents are still separate from execution agents; code execution mode mainly affects execution-time tool access and scripting behavior.
- `learn_code_script_execution` events exist specifically for saved-script runs and repair visibility in the UI.
- `error_summary` in `script_metadata.json` run records is stored in full (not truncated). `error_snippet` in `last_failure` is capped at 2000 chars for prompt inclusion.

## Key Files

| File | Role |
|---|---|
| `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go` | Main execution loop, fast-path invocation, repair loop, fallback handling |
| `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_learn_code.go` | Saved-script execution, static review, save-back, diff capture |
| `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go` | Mode semantics, workshop guidance, `run_saved_main_py`, config sync helpers |
| `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/step_config.go` | Applies step config and syncs declared mode to boolean flags |
| `agent_go/cmd/server/server.go` | Per-tool HTTP endpoints and bridge env setup |
| `agent_go/pkg/workspace/execute_shell_command.go` | Shell execution guardrails and tool-routing constraints |

## Orchestrator (todo_task) learn_code

This doc covers regular-step learn_code. Todo-task orchestrators also have a learn_code fast path with different semantics:

- **Read-only at runtime** — builder writes `main.py` once; no repair loop, no save-back, no fix iterations
- Eligibility: `declared_execution_mode="learn_code"` + `len(predefined_routes) >= 1`
- Script calls sub-agents via `POST ${MCP_API_URL}/tools/custom/call_sub_agent`
- Fallback is fresh — LLM orchestrator starts from zero, no script state carried over
- See [orchestrator-step-type.md](orchestrator-step-type.md) for full details

## Related Docs

- [Step Config Specification](step_config_format_specification.md)
- [Learning Architecture](learning_architecture.md)
- [Orchestrator Step Type](orchestrator-step-type.md)
