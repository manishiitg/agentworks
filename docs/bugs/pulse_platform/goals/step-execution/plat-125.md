[← goals / step-execution](index.md)

# PLAT-125 — every workflow step agent is handed the workshop chat's builder reference bundle, and acts on tools it does not have

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | goals |
| Area | step-execution |
| Summary | P1 — a step agent is told to call tools that are not registered |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — capability-derived selection shipped with regression tests; live reverify pending a run on a rebuilt server |
| Last synchronized | `2026-08-17` |

- **Priority:** P1 — a step agent is told to call tools that are not registered
  in its session. The call fails, nothing surfaces it, and the agent improvises
  a substitute — including inventing provider names. The run reports success.
- **Owner:** step execution agent construction
  (`pkg/orchestrator/agents/workflow/step_based_workflow/supplementary_prompts.go`),
  guidance materialisation (`cmd/server/guidance`)

## How it surfaced

A social-media run logged four `tools_unavailable` errors in one second:

```text
17:40:42 [TOOL_ERROR] virtual tool failed  args="{tool_name=[\"list_published_llms\"]}"
17:40:42 [TOOL_ERROR] virtual tool failed  args="{tool_name=[\"list_provider_models\"]}"
error="tools_unavailable: unknown=[list_published_llms]: these names are not
registered by any currently connected server. Registered tools for this session:
[agent_browser diff_patch_workspace_file execute_shell_command mutate_workflow_db
 query_workflow_db read_skill record_run_concern search_web_llm]"
```

The caller is a message-sequence sub-agent, not a chat or Pulse session:

```text
session_id=msgseq-iteration-0-default-default-step-6-sub-execute-allocate-…
```

It holds eight tools. None of them is a provider-configuration tool.

## Root cause

`supplementary_prompts.go:154` builds the step agent's reference identity from a
hardcoded mode string:

```go
func workflowReferenceSkill() *llmtypes.Skill {
	return guidance.MaterializeReferenceSkill("workshop")
}
```

`"workshop"` is a **guidance mode**, and it is the mode of the *workshop chat* —
the builder UI where a human designs a workflow. Materialising it yields 41
reference docs, among them `llm-provider-config`, which instructs the reader to
use `list_published_llms`, `list_provider_models`, `test_llm`,
`save_published_llm` and `set_provider_auth`.

Step execution is a different subsystem from the workshop chat and is not
described by the mode vocabulary at all. It borrowed the closest available mode.

Both step-agent construction paths share this — `controller_agent_factory.go`
lines 1399 and 1833 — so **every** step type receives the identical bundle:

```text
orchestrator · message_sequence · routing · regular · sub_agent
        → appendSupplementaryPrompts → the same 41 docs
```

There is no per-type differentiation today.

## Why the bundle is wrong for execution, doc by doc

The step-type entries in the corpus are **design-time** material written for the
builder, not the executing agent. Their own registry descriptions say so:

| kind | description says |
|---|---|
| `routing` | "Routing step **design**: when to use routing vs todo_task/message_sequence" |
| `regular` | "regular step **design** … Load before **adding** a scripted regular step" |
| `message-sequence` | "**patterns** — when same-context ordered turns should share one conversation" |
| `todo-task` | "todo_task step **design**: when to use vs routing / message_sequence" |
| `step-config` | "planning/step_config.json via **update_step_config**" |
| `running-steps` | "always iteration-0 in workshop **builder** → **execute_step**" |
| `execution-policy` | "per-group policy for **run_full_workflow**" |

A routing step does not need to read *when to choose routing*; that decision was
made when the workflow was built. The same holds for every other type.

A second group belongs to Pulse and the Finalizer, which obtain their guidance
from the other attach point (`workflow_phase_tools.go`, see PLAT-119):
`backup-strategy`, `publish-strategy`, `reporting-policy`, `fix-verification`,
`assumption-audit`, `debugging-flow`, `deployed-channel`.

`file-layout` is redundant: `appendSupplementaryPrompts` already injects explicit
absolute path discipline (`AbsWorkspacePath`, `STEP_OUTPUT_DIR`) as a supplement.

## Consequence beyond the failed call

The failure is not contained. Having been told a discovery tool exists and
finding it absent, the agent had no way to ask which providers are published, so
it guessed — producing 19 further `search_web_llm` errors the same day:

```text
search_web_llm could not find requested provider "vertex" in the published LLM set
search_web_llm could not find requested provider "minimax-coding-plan" …
search_web_llm requires workspace auth for requested provider …
```

Provider names attempted: `claude-code`, `codex-cli`, `cursor-cli`, `pi-cli`,
`minimax-coding-plan`, `vertex`. This is enumeration, not selection.

The platform already knows this pattern. `controller_agent_factory.go:726`
forces code-execution mode for scripted steps for exactly the same reason:

> without these, the LLM has to guess MCP server/tool names when writing main.py

Remove an agent's discovery surface and it invents names. Here the guidance
created the need for a discovery surface the session did not have.

## Required repair

Select the step agent's reference docs from signals that already exist at
agent-construction time, rather than from a mode:

| doc | attached when |
|---|---|
| `browser-usage` | `agent_browser` registered |
| `stores` | `query_workflow_db` / `mutate_workflow_db` registered |
| `workspace-media-tools` | `search_web_llm` / media tools registered |
| `mcp-bridge` | code execution / MCP bridge available |
| `code-authoring` | scripted step (the forced-code-execution branch, `controller_agent_factory.go:724-734`) |

A doc must not be attachable to a session lacking the tools it describes. That
invariant is what makes this class of bug unrepresentable, rather than fixing
this one instance.

Typical result: a message-sequence step with a browser receives 3 docs; a
scripted step receives `code-authoring` + `mcp-bridge`; a routing step with
neither receives approximately 1. Today each receives 41.

Per-step variation stays where it already works — the step's own
`effectiveSkills`, attached separately in the same function.

## Fix shipped

`kindMeta` gained a `Tools` field naming the runtime tools each doc explains.
`MaterializeStepExecutionReferenceSkill(StepExecutionSignals)` selects by those
signals instead of by mode, and `workflowReferenceSkill()` — previously
`MaterializeReferenceSkill("workshop")` — now takes them. Both call sites in
`controller_agent_factory.go` pass the agent's own `toolsToRegister` and
`isScriptedExecutionModeConfig(stepConfig)`.

Two of the five signals are not tool names, and are kept explicit rather than
inferred: `mcp-bridge` follows `config.UseCodeExecutionMode` (the HTTP bridge it
documents), and `code-authoring` follows the scripted step (the only surface
that authors `main.py`).

Measured on the exact tool set from the log line that opened this ticket:
**40 docs → 4**.

A pre-existing test,
`TestWorkflowReferenceSkillContainsExecutionDocsForEveryTransport`, already
asserted that a step must receive exactly `code-authoring`, `browser-usage`,
`mcp-bridge` and `stores`. That expectation was written before this ticket and
is unchanged by it — the capability-derived selection reproduces it from the
signals rather than from the workshop mode, which is independent confirmation
of the set.

## Acceptance

- A step agent's attached reference docs contain no tool name that is not
  registered in that session.
- A test drives a real step agent through the product path and asserts the
  attached doc set, not that the selection function returns the right list.
- Removing `agent_browser` from a step removes `browser-usage`; marking a step
  scripted adds `code-authoring`.
- The social-media `execute-allocate` shape produces zero `tools_unavailable`.

## Not fixed here

- **`html-output`** is deliberately excluded. A step does not need it; a step
  that writes HTML carries it through its own selected skills.
- **Nobody reads the tool-error logs.** Detection is not the gap:
  `mcpagent/toolerr` classifies deliberately, with a second `[TOOL_ERROR_SUSPECT]`
  marker added after a 2026-08-01 incident in which 34 bridge failures rendered
  as green checks. Every reference to those markers across all three repos is a
  producer; there are zero consumers, and the package doc names `grep` as the
  intended one. 716 error-level lines were logged the day this ticket was
  written and went unexamined until someone asked.

  These logs are for **platform maintainers**, not for Pulse or the agents.
  `tools_unavailable` in particular is always a platform defect — guidance and
  tool-set disagreeing — which no workflow author can act on, so routing it into
  Gate's evidence would only add worklist noise about something the workflow
  cannot repair. The gap is cadence and a baseline (which classes are new versus
  routine), not a new signal or a new destination.
- **`record_run_concern`'s "no trusted step identity" failure** (3 occurrences
  the same day, salesoutreach) is a different mechanism and was explicitly
  deferred by PLAT-123.
- **A related but separate mode-gating defect** was found and fixed while
  diagnosing this: `buildConfigurationAccessGuidance` appended the
  provider-configuration tool recommendation unconditionally, so `run`-mode
  sessions were also told about tools they never receive, even though the
  `llm-provider-config` registry entry excludes that mode. That fix does **not**
  address this ticket — the caller here resolves to `workshop`, where the gate
  legitimately passes.
