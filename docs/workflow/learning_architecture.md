# Learning Architecture

This document describes the current workflow architecture as implemented now.

The older model in this repo treated learning as part of a larger learning-plus-validation architecture with LLM validation modes, per-step learning files, and exploration/exploitation prompt strategies. That is no longer the right mental model.

## Current Reality

Today the learning side of workflow runtime is built around two simpler ideas:

- **Learning writes into a shared global skill** at `learnings/_global/`.
- **Scripted code steps can also persist step-specific scripts** under their own learnings folder.

If you remember the older architecture, these are the most important updates:

- Learning is no longer primarily about per-step prose learnings.
- The learning agent now writes domain knowledge into a global skill folder, usually centered on `learnings/_global/SKILL.md`.
- For `learn_code` steps, `main.py` is the executable truth; `SKILL.md` is secondary guidance.

## Learning

### What learning writes now

The main learning destination is the **global skill**:

- `learnings/_global/SKILL.md`
- optional supporting files under:
  - `learnings/_global/references/`
  - `learnings/_global/scripts/`
  - other skill-structured files

`SKILL.md` should stay lean. Treat it as the index and overview for the workflow runbook, not the place for detailed accumulated guidance. Keep it under roughly 80-100 lines, with links to focused `references/<topic>.md` files. Detailed selectors, auth flows, API quirks, timing/wait rules, file-format notes, retry patterns, and step-specific HOW guidance should live in those reference files.

The learning agent prompt in [`learning_agent.go`](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/learning_agent.go) is explicit:

- accumulate **domain knowledge across all workflow steps**
- keep it focused on the target system
- merge findings into one shared skill
- follow skill structure, not old flat learning-note files
- keep `SKILL.md` as a short index and put detailed HOW knowledge in reference files

The controller also hardwires global learning mode in [`controller_learning.go`](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_learning.go):

- `UseGlobalLearning = "true"`
- `ContributingStepID`
- `ContributingStepTitle`
- optional `GlobalSkillObjective`

### Step-specific learnings still exist, but differently

There are still step-specific artifacts, but they are no longer the main prose-learning model:

- `learn_code` / scripted steps save reusable scripts under `learnings/{step-id}/`
- especially `learnings/{step-id}/main.py`
- scripted steps may also keep `SKILL.md` notes for edge cases and repair hints
- metadata remains per step in `learnings/{step-id}/.learning_metadata.json`

So the current split is:

- **global domain knowledge** → `learnings/_global/`
- **step-specific executable artifacts** → `learnings/{step-id}/`

### Learning objective

The current system expects a workflow-level objective for the global skill:

- `global_skill_objective`

This tells the learning agent what kind of reusable knowledge should be accumulated, for example:

- auth flow patterns
- selectors
- API patterns
- common failure modes
- target-system structure

This is a better description of the current design than the older “extract learnings per step until stable” framing.

## Learning lifecycle

### Success learning

After a successful step:

- runtime can launch **success learning** in the background
- it reads recent execution history and validation result
- it updates the global skill
- it updates step metadata

This happens in [controller_learning.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_learning.go).

Important current details:

- success learning is the real active learning path
- learning detection via a separate LLM-based “did we learn something new?” phase has been removed
- metadata is updated using a rule-based path instead

### Learning metadata

Runtime learning metadata is observational. It gives the workflow builder and
review tools evidence for deciding whether a step's `learnings_access` should
remain read-write, but it does not mutate that access.

Current metadata logic in [`controller_learning_detection.go`](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_learning_detection.go):

- the step description is hashed (SHA256 of trimmed `step.GetDescription()`) on every successful run
- if the hash matches the previously-stored `last_description_hash`, `description_hash_runs` increments
- if the hash differs, `description_hash_runs` resets to 1 and the stored hash is updated

Editing the step description resets the description-hash run counter. This is
review evidence only; runtime execution does not automatically change learning
access.

### Locking and disabling learning

The important current controls on `AgentConfigs`:

- `learnings_access` (string enum: `"read" | "read-write" | "none"`) — primary gate. Mirrors `knowledgebase_access`.
  - `"read"` (default) — step sees `_global/SKILL.md` in its prompt; does NOT contribute.
  - `"read-write"` — step reads AND contributes. Requires `learning_objective` to be non-empty.
  - `"none"` — step neither reads nor contributes. The true disable.
- `learning_objective` (string) — the **extraction instruction** for the post-step learning agent. Required when access is `"read-write"`. No longer a gate.
- `global_skill_objective` (workflow-level, not per-step) — describes what domain knowledge the global skill should accumulate.

Auto-migration for legacy configs (runtime-only, no file rewrites): if `learnings_access` is unset, `learning_objective` non-empty infers `"read-write"`; empty infers `"read"`.

Recommended usage:

- leave `learnings_access` unset (defaults to `"read"`) for most steps — they benefit from cross-step context.
- set `learnings_access: "read-write"` + a non-empty `learning_objective` on steps that produce durable HOW-knowledge about the target system.
- set `learnings_access: "none"` for steps that are truly throwaway or whose context would pollute the global skill (e.g. pure file moves).
- change a mature or redundant contributor to `learnings_access: "read"` so it still consumes shared guidance without running a write turn.

### Failure learning

The older docs described a full failure-learning architecture. That is not a good description of the current codebase.

What still exists:

- some comments, metadata fields, and workshop text still reference failure learning

What matters operationally now:

- the active, clearly implemented learning path is success learning into the global skill

Until failure-learning behavior is re-established as a first-class runtime path, docs should not present it as a central architecture pillar.

## Scripted code steps

For `learn_code` steps, learning and execution are intentionally split:

- `main.py` is the executable artifact
- `SKILL.md` is supporting knowledge
- global skill captures reusable domain knowledge
- step folder captures the reusable script and related metadata

That means learning for scripted steps is not just “write prose notes.” It is:

- maintain reusable code in `learnings/{step-id}/main.py`
- maintain reusable domain knowledge in `learnings/_global/`

See [learn_code_flow.md](learn_code_flow.md).

## Current file layout

### Global

```text
learnings/
  _global/
    SKILL.md
    references/
    scripts/
```

### Step-specific

```text
learnings/
  <step-id>/
    .learning_metadata.json
    SKILL.md                # optional supporting notes
    main.py                 # scripted steps
    scripts/
    diffs/
```

Not every step uses every file. The important distinction is:

- `_global/` is the shared workflow skill
- `<step-id>/` is the step-specific artifact area

## What to update in other docs

When editing related workflow docs, keep these rules consistent:

- describe learning as global-skill-first
- gate both read access and write contribution through `learnings_access`
- for scripted steps, describe `main.py` as the executable source of truth
- description-hash metadata is review evidence only; runtime does not automatically change learning access
- leave validation details to the dedicated pre-validation docs

## Code references

- [`controller_execution.go`](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go): learning triggers and post-execution flow
- [`controller_learning.go`](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_learning.go): success learning and global-skill write path
- [`learning_agent.go`](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/learning_agent.go): global skill prompt and skill-structured output
- [`controller_learning_detection.go`](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_learning_detection.go): learning metadata updates
- [`interactive_workshop_manager.go`](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go): current user-facing guidance for global learning

## Related docs

- [pre_validation_guide.md](pre_validation_guide.md)
- [step_config_format_specification.md](step_config_format_specification.md)
- [learn_code_flow.md](learn_code_flow.md)
