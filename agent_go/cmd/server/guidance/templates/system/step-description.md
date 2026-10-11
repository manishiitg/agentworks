## Writing Optimized Step Descriptions (Prompt Engineering)

A step's `description` is the durable system-level charter the execution agent
receives on every turn. Every word costs context and has system-level authority,
so keep it stable and task-defining. For `message_sequence`, the ordered
`items[]` are the actual user messages that tell the agent how to carry out,
inspect, verify, or repair that charter. Read this before writing or editing a
step's `description` or sequence items.

### Check what the runtime already supplies

Before authoring, read `references/step-system-prompts.md` from this same
builder-reference skill. It is the canonical source used by execution and
orchestrator runtime code, not a separately maintained summary. Read its named
section for the step type and the managed-DB sections it references. Template
conditions and placeholders are resolved per run; they are not permission grants.

The runtime supplies platform path/output conventions, effective Folder Guard
permissions, managed database access, store responsibilities, variable/secret
handling, completion/blocker reporting, and role-specific execution rules.
Skills, learnings, browser instructions and shared KB access depend on the
configured step and runtime. Keep those mechanics out of descriptions. Do not
move copies of platform rules into global learnings or another skill either.

Keep the task's data contract: table names, relevant filters, writer ownership,
business keys and idempotency requirements, exact domain KB topics or files,
evidence requirements, and binding business/approval constraints. For example:
"Read active archetypes from `archetype_registry`; use the risk rules in
`knowledgebase/strategy_framework.md`; upsert results by `experiment_id`."
Do not add connection instructions, raw SQLite commands, or general shell/path
rules that duplicate or contradict the runtime. Confirm that referenced files
exist and are accessible under the step's configured permissions.

For an existing run, use `get_step_prompts(step_id="...")` to inspect its saved
system prompt and user message, selecting the relevant attempt/iteration. This
also shows run-specific context unavailable in the source template. A saved
prompt is evidence for that run, not a preview of later configuration changes;
new steps have no saved prompt until execution starts.

### Description defines WHAT; items carry turn instructions; skills and learnings carry reusable HOW

Write the description as a stable charter focused on the step's objective: what
result to achieve, which inputs or evidence govern it, the scope and business
rules, what success means, and where the result belongs. Keep binding constraints
here, including approval requirements and actions outside the step's authority.
These define the task even when the implementation changes.

For `message_sequence`, put execution phases and conversational instructions in
`items[]`: perform the work, re-open authoritative evidence, critique it, repair
verified gaps, or incorporate new runtime input. Do not repeat the description in
the first item. The first item should tell the agent what to do now under the
charter, not redefine the charter. Live Workshop `human_input` and parent-agent
delegation instructions are also user messages, not description mutations.

### Use this section layout for the description

This layout is for agent steps (message_sequence and orchestrator). Scripted, routing, branch, human-input and Crew
steps are driven by code, routes, a question to a person or a Crew; keep their description short and do not force
the sections on them.

Write the description with these Markdown sections, in this order. Omit a section
only when it has nothing to say; do not add other sections.

```
## Goal       — what the step achieves, in one to three sentences
## Inputs     — what it reads: prior step outputs, DB tables, KB notes by name
## Output     — what it produces and where; the shape stays in validation_schema
## Rules      — binding business constraints, approval limits, actions outside its authority
## Done when  — the success boundary, including what counts as a valid no-op
## Guides     — the skill/learning references to use, named only, never copied
```

Everything else belongs somewhere else: execution phases in `items[]`, reusable
procedures in skills and learnings, domain facts and decisions in the
knowledgebase, platform mechanics nowhere (the runtime supplies them).

Naming a path in Inputs or Guides delivers it: a workflow path under
`learnings/`, `knowledgebase/`, `code/shared/`, `soul/` or `db/README.md`
becomes readable for that step (never writable) whatever its learnings and
knowledgebase access, and small files (up to 4,000 characters, 12,000 in total)
are attached to its prompt.<!-- product:knowledgebase --> Name Brain notes as `brain:<folder>/<note>`.<!-- /product --> A named
path that does not exist is reported to the step and in the plan edit response.

<!-- product:knowledgebase -->Brain use is part of the charter, in plain words; there is no folder binding
to configure. The workflow's Brain access (Off, Read, Read & write) and the
owner's folder roles bound what is possible; the step's sections say what this
step does within that. Name what it reads under Inputs or Guides
(`brain:server A/Latency/runbooks/deploy.md`, delivered read-only), what it writes
under Output ("add one line per incident to `brain:server A/Latency/Incidents/`"),
and any limit under Rules ("never write to Brain outside `server A/Latency/`").
Look up real folder paths with `brain_browse` before writing them. A step that
must not write gets `knowledgebase_access: read`; one with no Brain use,
`none`.
<!-- /product -->
A description holds no history. Dated observations ("measured on 2026-08-10…"),
incident narratives and "this exists because…" explanations go to the
knowledgebase as a decision or to the learnings as a verified technique, or are
deleted once the fix is in place. A repair after a failed run changes the rule,
the item or the guide it concerns; it does not append a story to the charter.

Put reusable execution methods in the step's skills and learnings: tool usage, selectors, API/authentication sequences, troubleshooting, and techniques verified in prior runs. Reference the relevant guidance and configure the step's skill/learning access so it can actually read it. Do not assume a skill exists or that a new step has already learned a procedure; provide needed guidance there, or let the execution agent choose a method within the task's constraints and retain verified reusable know-how through the configured learning flow.

For browser-test steps, describe the test outcome and any required live visibility.
Keep library imports, setup, and lifecycle in the accessible
`builder-reference/references/playwright-scripted.md` guidance. Verify that the
selected runtime can provide the requested visibility; do not promise it merely
because a step says “Playwright.” Preserve the user's language and runner choices.

Keep the output structure and automated checks in `validation_schema`. Descriptions, skills/learnings, and schemas should complement one another without repeating the same content.

### Earn every word

Before finalizing a description, cut anything that:
- Restates what the model already knows from training, its system prompt, or an already-loaded skill — do not re-explain what JSON is, what "verify" means, or what a tool already documents about itself.
- Restates content already reachable through `context_dependencies`, `learnings_access`, `knowledgebase_access`, or managed DB tools — reference the store ("read `learnings/_global/SKILL.md` for the login selectors") instead of copying its contents inline.
- Repeats the same constraint from a different angle, hoping one phrasing lands — state it once, precisely.
- Hedges with qualifiers that add no checkable meaning ("carefully," "thoroughly," "properly," "make sure to") — state the actual verifiable criterion instead.

### Let `validation_schema` name the shape — don't restate it in prose

`validation_schema` is rendered as part of the executing agent's system contract
for every step type — not only reactively after a failed attempt. So once a step
has a schema, neither the description nor an item needs to spell out the object's
keys; doing so duplicates the contract and lets the copies drift. Define the
schema, then let the description state the durable outcome and context the schema
cannot carry, while items state the work and verification to perform.

If an output needs a defined structure and no schema exists, add a light `validation_schema` or reference an accessible authoritative contract. Do not use the description as a substitute schema. Free-form output does not need an invented object structure.

### Keep `validation_schema` light — not exhaustive

Because the schema now renders into the prompt on every attempt, its size is a prompt-bloat cost like any other section, and an over-specified schema is a common failure mode in practice — it's easy to check everything the output happens to contain instead of only what actually matters. Validate the load-bearing contract points: does the file exist, do the handful of fields a downstream step, evaluator, or user genuinely depends on have the right shape and value. Do not add a check for every field just because the field exists, re-validate structure the model already got right by construction (e.g. re-checking a field it copied verbatim from an upstream file), or assert on free-form/optional content where a cosmetic variation is not actually wrong. Each check should answer "what real failure does this catch" — if the answer is "none, it's just thorough," cut it. A schema that mirrors the entire output document duplicates work the model already did and turns harmless variation into a spurious validation failure and retry.

### State the outcome, not a micromanaged procedure — unless the task genuinely needs one

Prefer: "Verify every listed API endpoint returns a 2xx status and its response body matches the declared schema; record failures in `api_failures.json`."

Over: "First, open the API testing tool. Then, for each endpoint, carefully send a request. Check the response. If it looks wrong, note it down. Make sure to be thorough."

The first names one precise, checkable outcome. The second spends four sentences restating the same instruction with decreasing specificity, and its vagueness ("looks wrong," "be thorough") gives the model nothing an evaluator could verify against.

When a reusable fixed procedure is necessary (browser selectors, a multi-stage
authentication flow, or an ordered API call chain), put it in a skill or learning
and reference it. Put a task-specific execution sequence in `items[]`. Keep only
an ordering constraint that defines correctness or authority in the description
itself—for example, approval must precede publishing—without copying the
operational procedure.

### Do not duplicate across steps

If two steps in the same plan describe the same task-specific policy, contract, or procedure, that is not two descriptions — it is one description and a reference. First remove anything already supplied by the runtime; do not relocate it. Move remaining shared task knowledge to `learnings/_global/SKILL.md` (durable HOW-to-operate knowledge), the knowledgebase (domain facts), or a validation schema (structural contract), and have each step reference it by name. A copy-pasted paragraph across steps is a maintenance liability — a fix to one copy silently leaves the others stale — as much as it is a length problem.

### A rough self-check before finalizing

Could someone who has never seen the workflow read the description and understand
the intended result, evidence, constraints, and success criteria? Could they read
the items and understand what the agent should do on each turn without finding a
second, conflicting charter? Do the separately supplied schema and referenced
skills/learnings provide the output contract and reusable execution guidance
without contradictions? The description should not reconstruct the schema or
teach the procedure. Delete any sentence or validation check that carries no
necessary contract information.

### Related

- `references/plan-design.md` for step-type selection, context flow, and validation design.
- Architecture Review audits existing prompt structure against these principles when evidence suggests a material design improvement. Applying them while authoring is cheaper than proposing a later migration.

- Plan Drift applies this same guide to due steps and records `step_prompt_quality`. It checks prompt meaning, supplied schemas, and accessible guidance; it does not require rewriting a compatible prompt or optimizing an unrelated architecture.
