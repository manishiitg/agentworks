## LLM Selection — picking & pinning the model that runs a step

This doc is about choosing which LLM executes workflow work. There are **two levels**: workflow-wide role allocation and per-step overrides. Default to workflow roles; reach for per-step pins only when a specific step needs one.

### Workflow roles

A workflow always resolves these roles:

- **Builder**: planning, eval design, debugging, normal workflow-builder chat,
  scheduled runs, and every turn of the post-run Pulse conversation (Gate,
  review dispatch, Finalize). One scheduled run is one conversation on one
  retained coding CLI, so it runs on one model throughout.
- **High execution**: first-time, ambiguous, or difficult step work.
- **Medium execution**: established work with useful context and learnings.
  Also Pulse upkeep: the Plan Drift, Technical and Architecture review agents
  and knowledgebase maintenance run on the Medium tier.
- **Low execution**: deterministic validation and mature routine work.
- **Pulse** (`pulse_llm`): the Pulse review turns, including Goal Work, run
  in the Pulse conversation itself on the retained coding CLI, so they use that
  conversation's model; a retained CLI cannot switch model mid-conversation.
  Not the Gate, worklist, report or notification turns: those are Builder.

The config has two modes:

- **`provider_profile` (simple)** stores only a coding-agent `provider`. The provider package supplies current defaults for every role at runtime, so an app update can improve those defaults without rewriting the workflow.
- **`explicit` (advanced)** pins `builder_llm`, `pulse_llm`, and all three entries under `tiered_config`. Every entry has a direct `provider` + `model_id`, optional provider `options` such as `reasoning_effort`.

Each call uses one selected model and coding-agent provider. Transient errors may
retry that same model; failures never trigger automatic model/provider switching.
Pulse reviews actual failures and must not recommend backup chains or flag their
absence as a configuration issue.

Saved configurations are reusable shortcuts for exact provider/model/options combinations. They are not required before a provider or model can be selected.

**Tools:**
- `list_provider_models` — authoritative models and supported options exposed by a provider. Use this before choosing a direct model.
- `list_published_llms` — optional saved model configurations that may be reused as shortcuts.
- `test_llm` — smoke-test a provider/model before committing to it.
- `set_workflow_llm_config(mode="provider_profile", provider="...")` — follow the provider's current role defaults.
- `set_workflow_llm_config(mode="explicit", builder_llm=..., pulse_llm=..., tier_1=..., tier_2=..., tier_3=...)` — pin the complete advanced role configuration. Do NOT edit `workflow.json` by hand.
- `get_llm_config` / `get_workflow_config` — inspect the current workflow roles and per-step overrides.

### Per-step overrides (use sparingly)

Pulse Architect owns persistent tier/model improvement proposals. Runtime does
not switch tiers from successful-run counts, learning changes, or failures.
Unconfigured execution steps default to High; evaluation steps default to Medium.
Existing explicit settings and one-run caller overrides keep their precedence.
Use measured, approved trials with quality guardrails and a rollback checkpoint;
load `architecture-review` for the evidence and decision contract.


Set via `update_step_config(step_id, ...)`:

- **`execution_tier`** (`"high"` | `"medium"` | `"low"`) — a *persistent* tier override for one step in tiered mode. Use **high** for subjective/ambiguous judgment, **medium** for normal checks, **low** for deterministic/file-shape checks. Prefer this over pinning an exact model when the intent is just "this step can usually run cheaper/faster".
- **`execution_llm`** (`{provider, model_id}`) — pins an *exact* model for one step. Use only when a specific model is genuinely required (a capability only that model has).
- **`validation_llm`** — same shape, overrides the model used for that step's validation. Learning model selection is handled by tiered allocation; there is no separate `learning_llm` setting.

**Precedence (highest wins):**
1. `execute_step(step_id, group_name, tier="...")` — a one-off tier for a single trial run; changes nothing persistent.
2. `execution_llm` — if set, it pins the model and **`execution_tier` is ignored** until the exact-model override is cleared.
3. `execution_tier` — persistent tier for the step.
4. Maturity-based default tier selection — what tiered mode does on its own.

**Clearing an override:** `update_step_config(step_id, clear=["execution_llm"])` (or `["execution_tier"]`) removes the override so the step inherits tiered/default behavior again.

### Model freshness and upgrades

- Provider-profile mode follows provider-owned role defaults automatically. A
  default moving from an older model to a newer model is expected and does not
  require rewriting the workflow.
- Exact pins do not move automatically. Audit explicit workflow roles and
  planning/evaluation step overrides with `list_provider_models(provider=...)`.
  Its catalog and `default_tier_models` are authoritative for availability and
  the provider's current role/tier recommendations.
- Classify each pin as unavailable/deprecated, supported but different from the
  current role/tier default, or current. Do not infer "latest" by sorting model
  names or version strings.
- A different default is a candidate for review, not automatic proof of better
  quality. Compare supported reasoning options, capabilities, cost,
  and the step's real purpose.
- Never silently replace an exact pin. Propose clearing the pin to inherit its
  tier when no model-specific capability is required, or propose one exact
  replacement. Ask the user to Upgrade, Keep current, or Decide later before
  changing configuration.

### Choosing — a short decision framework

1. **Start with a provider profile, not pins.** Let the coding-agent provider supply sensible role defaults. Use explicit mode only when the workflow needs a deliberate cross-provider or model-specific allocation.
2. **Don't force a cheaper tier before reliability and goal quality are proven.** When a material workflow goal is below target, keep the current model/reasoning tier for outcome-bearing, planning, judgment, diagnostic, recovery, eval, and verification steps. Drop a step to `medium`/`low` only after representative eval/run evidence is at target and proves equivalent output and downstream outcomes. A strictly mechanical deterministic non-bottleneck step may be trialed cheaper only with that evidence, explicit approval, and rollback. Missing evidence means do not downgrade.
3. **Use `execution_tier` for "usually cheaper/faster", `execution_llm` for "must be this exact model".** Don't hardcode a model when a tier expresses the intent.
4. **Trial before committing.** Use `execute_step(..., tier="...")` or `test_llm` to validate a model on a real run before making it persistent.
5. **Match tier to the work.** Subjective/ambiguous judgment → high; routine checks → medium; deterministic/file-shape → low.

### Cost awareness

- `review_workflow_costs(iteration?, group_name?, focus?)` — read-only cost analysis with safe-reduction recommendations (which steps could drop a tier).
- `get_cost_summary` — current spend snapshot.

### Installing / authorizing providers

- Provider-manifest models are directly selectable. A saved configuration is needed only when the same exact provider/model/options combination should be named and reused.
- To add credentials for a provider, use `set_provider_auth(provider, api_key?, region?, endpoint?, ...)` — never paste API keys into shell or config files.
- Provider-backed **media** capabilities (image/video/audio/text generation, transcription, web search) are a separate surface with their own provider/model contracts and discovery — see `read_skill(skills=[{"name":"builder-reference","path":"references/workspace-media-tools.md"}])`. This doc covers the LLM that *executes agent steps*, not media generation.
