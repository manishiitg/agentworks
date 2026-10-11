# Tiered LLM Allocation

## Overview

Tiered allocation is the workflow's auto-selection mode for runtime LLM choice.

- **Manual mode**: preset and step configs choose explicit models
- **Tiered mode**: the runtime resolves Tier 1/2/3 from a per-agent-type default plus a few explicit overrides
- **Phase LLM**: configured separately and not part of the tier resolver

The current resolver lives in `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/tiered_llm.go`.

## Tiers

- **Tier 1**: High reasoning
- **Tier 2**: Medium reasoning
- **Tier 3**: Low reasoning

## Default Tier per Agent Type

| Agent | Default Tier |
|---|---|
| Execution | Tier 1 (High) |
| Learning | Tier 2 (Medium) |

Regular execution steps can adapt from High to Medium after three successful
runs with the same step-description hash. Tier selection does not inspect the
contents of the learnings folder, and Low is never selected automatically.

A validation or execution failure does **not** change the selected tier. Normal
validation retries continue the same agent conversation with validator feedback,
and the controller carries the failure into the final `CONCERNS:` summary for
Pulse. Persistent model/tier changes belong to the LLM/cost/tool/runtime lens of
Pulse `workflow_review` and the existing approval flow. Historical
`llm_ops_review` records remain valid evidence but are no longer scheduled as a
separate reviewer.

`disable_tier_optimization=true` still forces execution agents to Tier 1.

## Selection Priority

### Execution agents

Current priority in `selectExecutionLLM()`:

1. step `execution_llm`, always when set
2. `sub_agent_llm` from context, unless dynamic tier selection is enabled
3. tiered resolution
4. no valid config => error

Inside tiered resolution, execution uses this order:

1. workshop tier override from context
2. `preferred_tier` from sub-agent context
3. `disable_tier_optimization=true` => Tier 1
4. evaluation mode => Tier 2
5. default execution tier (Tier 1)

### Learning agents

Current priority in `selectLearningLLM()`:

1. tiered learning resolution
2. inherited workflow primary model
3. no valid config => error

## Phase LLM

Phase agents are independent of tiered maturity selection.

- planning/evaluation-design/debugging-style phase work uses the configured `presetPhaseLLM`
- the phase LLM is set separately from Tier 1/2/3
- if no phase LLM is configured, those phase paths can fail

One exception exists:

- final output generation prefers **Tier 2** in tiered mode and only falls back to `presetPhaseLLM` if Tier 2 cannot be resolved

## Todo Task Behavior

Todo-task steps add two tier-related controls in `agent_configs`.

### Orchestrator tier

- `orchestrator_llm`: exact model override, highest priority
- `delegating_agent_tier`: explicit tier override in tiered mode
- default: Tier 1

Current priority in `selectTodoTaskOrchestratorLLM()`:

1. `orchestrator_llm`
2. `delegating_agent_tier`
3. Tier 1

### Sub-agent tier selection

`preferred_tier` is **always a REQUIRED parameter** on `call_sub_agent` and
`call_generic_agent`. The orchestrator must reason about task difficulty on
every delegation — this is prompt discipline, not a conditional feature. Calls
without `preferred_tier` are rejected by the handler.

How the chosen tier translates into an actual model depends on runtime config:

- **Tier resolver configured + no pinned `execution_llm`** → `preferred_tier`
  resolves to the matching Tier 1/2/3 model. Cost optimization is active.
- **Parent step has `execution_llm` set** → `sub_agent_llm` context key wins at
  resolution in `selectExecutionLLM`. The pinned LLM is used for all sub-agents.
  The `preferred_tier` value is recorded in events/logs but does not change
  model choice.
- **No tier resolver + no pin** → falls through to the orchestrator's parent
  LLM. `preferred_tier` is informational only.

There is no `enable_dynamic_tier_selection` flag and no way to turn tier
selection off. If you want all sub-agents to share one model, pin
`execution_llm` on the parent step — the tier parameter stays required but its
value is ignored at resolution time.

## Manual vs Tiered Mode

In tiered mode:

- per-step `execution_llm` still overrides the execution selector directly
- per-step `learning_llm` has been removed; use tiered allocation instead
- runtime otherwise uses the tier resolver
- `orchestrator_llm` and `sub_agent_llm` still work because they are separate todo-task overrides

In manual mode:

- explicit step/preset model config is used
- tier resolver is absent

## Frontend Notes

Relevant UI files:

- `frontend/src/components/PresetModal.tsx`
- `frontend/src/components/WorkflowLLMConfigModal.tsx`
- `frontend/src/components/events/orchestrator/StepEditPanel.tsx`
- `frontend/src/components/workflow/BulkStepConfigModal.tsx`

The UI still exposes:

- preset tier configuration
- independent Phase LLM
- todo-task orchestrator tier
- dynamic tier selection toggle
- direct `orchestrator_llm` and `sub_agent_llm` overrides

## Events

`StepProgressUpdatedEvent` can include:

- `used_tier`
- `used_tier_label`

This is populated for tiered start events using the default execution tier.

## Key Files

- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/tiered_llm.go`
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_factory.go`
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_todo_task.go`
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller.go`
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/final_output.go`
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/workflow_events.go`
