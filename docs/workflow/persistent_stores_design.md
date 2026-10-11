# Persistent Stores Design

> **Status note — KB surface simplified to notes-only (2026).**
> This doc was written when the knowledgebase had two surfaces (structured `graph.json` + per-topic notes). The graph surface has been **removed**; the knowledgebase is now notes-only — per-topic markdown files under `knowledgebase/notes/` plus a `notes/_index.json` registry. Any section below that references `graph.json`, `index.json`, `entities`, `relationships`, `kb_upsert_*`, or `kb_shape: "graph+notes"` describes the earlier design and does not reflect current behavior. The code of truth is:
> - `pkg/orchestrator/agents/workflow/step_based_workflow/kb_graph.go` — only seeds `notes/_index.json` now.
> - `pkg/orchestrator/agents/workflow/step_based_workflow/kb_update_agent.go` — prompts are notes-only.
> - `pkg/orchestrator/agents/workflow/step_based_workflow/kb_tools.go` — holds `BuildStepKBGuidance` / `BuildKBContributionReviewMessage` for direct-write mode (shell + `diff_patch_workspace_file` under `notes/`).
> - `pkg/workflowtypes/types.go` — `ResolveKBShape` always returns `"notes-only"`; legacy `"graph+notes"` values collapse at load.
>
> Direct-write mode is a per-step option (`knowledgebase_write_method: "direct"`) in which the step agent writes to `notes/` inline via a post-completion self-review turn. Agent mode (default) keeps using the post-step KB update agent, which now also targets only `notes/`.

This document captures the planned improvements to how workflows manage persistent data, report output, and knowledgebase access control.

---

## Background: Current Stores

Workflows currently have three persistent stores at the workspace root:

| Store | Location | Written by | Purpose |
|-------|----------|------------|---------|
| **Learnings / Skills** | `learnings/_global/` | Learning agent (automatic) | HOW to execute — patterns, selectors, auth flows |
| **Knowledgebase** | `knowledgebase/` | Any step agent | Domain facts + doubles as persistent data store |
| **Reports** | `reports/{group}/` | Report agent | Human-readable run summary |

The tension: **knowledgebase is doing two jobs** — storing domain context to help agents make better decisions, and acting as a database for structured data that needs to survive across runs. These have different access patterns and formats and should be separated.

---

## 1. New `db/` Folder

### What it is

A dedicated folder for **structured persistent data in JSON format**, separate from the knowledgebase. Steps use it to store state, results, or any data that needs to survive across runs and be readable by later steps or other groups.

### Folder structure

```
workspace/
├── db/               ← new, always enabled, JSON files only
├── knowledgebase/    ← graph-structured domain knowledge (see section 4)
├── learnings/        ← procedural skill knowledge (HOW to run)
├── planning/         ← plan.json, step_config.json (read-only to builder via guard; mutated via plan-mod tools only)
├── reports/          ← report_plan.md (widget definitions; builder-writable)
├── soul/             ← new, builder's long-term memory: soul.md (see section 8)
└── runs/
```

Note: `reports/` no longer exists — the report is a live frontend view over `db/` + KB rather than a static archive (see section 2).

### Behaviour

- Always created on workspace init (no toggle, unlike knowledgebase)
- All steps get read + write access by default
- JSON format only — per-file schema is defined by the builder at design time (see Write semantics below); no runtime enforcement
- Never deleted during cleanup
- Injected into execution agent prompt as an absolute path

### Write semantics (single-db model)

There is exactly one `db/` per workspace — not partitioned by group or iteration. All groups and all reruns write into the same files. This keeps the report simple (one live view) but makes schema design a hard requirement, not a convention.

**Builder responsibility (design time).** When the interactive builder defines a `db/` file, it must decide and record:

- **Primary key** — what uniquely identifies a row (`company_id`, `url`, composite)
- **Group coexistence** — do multiple groups share the file (then include a `group` field and key by `(group, id)`) or does each group get its own file (`db/companies_{group}.json`)?
- **Merge rule** — upsert by key (default), append-only, or replace

These decisions go into a short schema note per file — inline in `report_plan.md` near the widget that consumes it, or a dedicated `reports/db_schema.md`. Step agents read the note at execution time so they know the contract.

**Step agent responsibility (runtime).** Agents MUST NOT overwrite `db/` files wholesale. The execution prompt instructs them to: read the file, upsert by the builder-defined key (or append, per the schema note), then write back the merged content.

For reliability a typed helper (`db_upsert(file, key, rows)`) is preferable to raw shell redirection — LLMs under pressure will happily `cat > file.json` with only their own rows and wipe the other groups' data. Leaving writes as shell + prompt is workable short-term but will produce lost-row bugs under stress. Without explicit schemas and write discipline, any workflow with more than one group or rerun will silently clobber prior data the first time two paths share a file.

### Key implementation touchpoints

| File | Change |
|------|--------|
| `controller_execution.go` | Add `DBFolderName = "db"` constant + `getDBPath()` |
| `controller_run_manager.go` | Always create `db/` on init (no `UseKnowledgebase`-style gate) |
| `controller_agent_factory.go:397` | Add `db/` to read+write in `setupExecutionFolderGuard` |
| `controller_agent_factory.go:806` | Same for orchestrator agent guard |
| `controller_todo_task.go:91` | Add `db/` to read+write paths |
| `execution_only_agent.go` | Tell agents about `db/` path and JSON convention |
| `delegating_agent_agent.go` | Same |
| `pre_validation.go:1080` | Add `"db"` to known workflow folder names |
| ~~`evaluation_types.go`~~ (retired) | ~~Add `DBWrite bool` to `EvaluationStep`~~ |
| ~~`controller_agent_factory.go:800`~~ (retired) | ~~Evaluation folder guard~~ |
| Workflow builder prompt | Recommend a dedicated subfolder for measurement outputs |

### Evaluation runs and `db/` (retired — eval subsystem removed; kept as history)

Evaluation steps can read and write `db/` — write access is opt-in per step via a `DBWrite` flag. There is no hard folder-guard split between regular and eval writes; the folder guard grants read and (optionally) write on all of `db/`. How `db/` is organized is up to the plan author.

**Best practice (builder prompt guidance, not enforcement)**

The workflow interactive builder prompt should recommend — but not enforce — that eval output goes to a dedicated namespace so it doesn't clobber regular-step data that the live report displays:

- Suggest `db/eval/` as the default subfolder for eval writes
- Suggest JSON or markdown under that path (narrative score summaries, structured scores, etc.)
- Suggest that regular `db/` files and eval output files have disjoint names

If a plan deliberately wants evaluation output mixed into the same `db/` files as regular steps (e.g. the eval step is producing the canonical data, not just scoring it), the builder lets that through. The tradeoff is on the plan author: enabling `DBWrite` on an evaluation step means that step's output is free to mutate whatever `db/` files it touches, including ones the live report reads. The builder should flag this when enabling the toggle.

Add a `db_write` boolean to `EvaluationStep` in `evaluation_types.go`:

```go
type EvaluationStep struct {
    ID              string
    Title           string
    Description     string
    PreValidation   *ValidationSchema
    SuccessCriteria string
    ContextOutput   string
    DBWrite         bool `json:"db_write,omitempty"` // If true, evaluation step can write to db/
}
```

Folder guard (`controller_agent_factory.go:800`):
- **Read**: always add `db/` to read paths
- **Write**: add `db/` to write paths only if `DBWrite: true` on the evaluation step

### Open questions

- Should `db/` writes be blocked from learn-code scripts (similar to the `knowledgebase/learnings/` check in `controller_learn_code.go:457`)?

---

## 2. Dynamic Report System

### What changes from current

| | Current | Proposed |
|--|---------|----------|
| **Report source** | Raw execution artifacts from `runs/{runFolder}/execution/` | `db/` files + `knowledgebase/graph.json` (workspace-scoped) |
| **Generation** | Report agent runs once, produces static markdown | No agent — frontend renders live from `report_plan.md` |
| **Storage** | `reports/{group}/{timestamp}.md` archive | No archive — always reflects current `db/` state |
| **Definition** | `planning/output_plan.json` (instructions for the agent) | `reports/report_plan.md` (markdown widget definitions) |
| **If no data** | Empty report with headings | Report not shown at all |

### `report_plan.md` format

Stored at `reports/report_plan.md`. Created and maintained by the workflow interactive builder.

`##` headings define sections. Fenced blocks define widgets. A `widget:row` block groups widgets side-by-side. Standalone widget blocks are full-width.

````markdown
## Overview

```widget:text
source: db/summary.json
path: description
```

```widget:row
- chart | source: db/results.json | path: counts
- chart | source: db/monthly.json | path: totals
```

## Companies Found

```widget:table
source: knowledgebase/graph.json
path: entities
filter: type=company
```
````

### Widget types

Three types only:

| Block | Description | Data shape at `path` |
|-------|-------------|---------------------|
| `widget:text` | Markdown text block | String value |
| `widget:chart` | Bar or line chart | `[{ "label": string, "value": number }]` |
| `widget:table` | Data table — columns from object keys | Array of objects |
| `widget:row` | Layout wrapper — groups widgets side-by-side | — |

Inside `widget:row`, each line is `- {type} | source: {path} | path: {key}`.

### Widget fields

| Field | Required | Description |
|-------|----------|-------------|
| `source` | yes | `db/{file}.json` or `knowledgebase/graph.json` |
| `path` | yes | Dot-notation key into the JSON file |
| `filter` | no | `key=value` — filters array items (useful for KB entities) |

### Data sources

| Source | What it points to |
|--------|------------------|
| `db/{file}.json` | Any JSON file in the workspace-level `db/` folder |
| `knowledgebase/graph.json` | KB graph — entities and relationships |

Both are workspace-scoped. The report always reflects the latest state.

Widgets can point at any path under `db/` — including eval outputs if the plan writes them under `db/eval/` (the recommended convention, see section 1). The report does not distinguish "regular" vs "eval" data automatically; if the builder wants an Evaluation section in the report, it adds widgets pointed at the eval files explicitly.

**If `db/` has no data yet**, the report is not shown — no empty state, no placeholder widgets.

### How the frontend renders it

1. Fetch and parse `reports/report_plan.md`
2. Split on `##` headings → sections
3. For each fenced block, parse type + fields
4. For `widget:row`, parse the inline widget list
5. For each widget, fetch `source` file, walk `path`, apply `filter`
6. If any required source file is missing or resolved data is empty → do not render the report
7. Render each widget using its type component

### Who creates `report_plan.md`

The **workflow interactive builder** (chat phase). The user describes what they want to see; the builder agent writes the markdown document based on what steps write to `db/` and what the KB captures. Since the builder knows the step plan, it can infer the right `source` paths.

### Report is not triggered by workflow execution

The report is no longer a phase that runs after execution completes. It is purely a frontend view — opened on demand. `db/` is populated during normal step execution and evaluation runs, so the report data is always there when the user opens it.

- `MaybeRunAutoFinalOutput()` is removed — no post-run auto-trigger
- The "Final Report" button in the toolbar no longer starts a phase — it just opens the report viewer
- The report viewer checks whether `db/` has data; if not, it shows nothing

### What goes away

- `final_output.go` — report agent removed entirely
- `planning/output_plan.json` — replaced by `reports/report_plan.md`
- `reports/` folder — no more archive files
- `WorkflowStatusReportExecution = "report-execution"` phase constant
- `runReportExecutionOnly()` in `workflow_orchestrator.go`
- `MaybeRunAutoFinalOutput()` — no post-run auto-trigger
- `handleGetFinalOutputs`, `handleGenerateFinalOutput`, `handleGetFinalOutputConfig`, `handleUpdateFinalOutputConfig` HTTP handlers
- `FinalOutputPopup.tsx` — replaced by dynamic report viewer

### Key implementation touchpoints

| File | Change |
|------|--------|
| New: `frontend/src/components/workflow/ReportViewer.tsx` | Parses `report_plan.md`, resolves sources, renders widgets |
| New: `frontend/src/components/workflow/widgets/TableWidget.tsx` | Table renderer |
| New: `frontend/src/components/workflow/widgets/TextWidget.tsx` | Markdown text renderer |
| New: `frontend/src/components/workflow/widgets/ChartWidget.tsx` | Bar/line chart (reuses existing SVG chart logic from `MarkdownRenderer.tsx`) |
| `frontend/src/services/api.ts` | Add `getReportPlan()`, `getDBFile()` API calls |
| `frontend/src/services/api-types.ts` | Add `ParsedReportPlan`, `ReportSection`, `ReportWidget` types |
| `WorkflowToolbar.tsx` | "Report" button opens `ReportViewer` (no phase start) |
| `cmd/server/workflow.go` | Add `handleGetReportPlan()`, `handleGetDBFile()` handlers; remove old final-output handlers |
| `workflowtypes/types.go` | Remove `WorkflowStatusReportExecution` |
| `workflow_orchestrator.go` | Remove `runReportExecutionOnly()` routing |
| Delete: `final_output.go` | Report agent no longer needed |

---

## 3. Step-Level Knowledgebase Access Control

### What it is

Replace the binary `disable_knowledgebase` per-step flag with an explicit access mode: `"read"`, `"write"`, `"read-write"`, or `"none"`. The folder guard enforces the mode.

### Current state

- `UseKnowledgebase bool` at preset/workflow level
- `DisableKnowledgebase *bool` in `AgentConfigs` — only disables, can't make a step read-only

### Proposed: `knowledgebase_access` + `knowledgebase_contribution` fields

```json
// planning/step_config.json
{
  "steps": [
    {
      "step_id": "step-1",
      "agent_configs": {
        "knowledgebase_access": "read"
      }
    },
    {
      "step_id": "step-2",
      "agent_configs": {
        "knowledgebase_access": "read-write",
        "knowledgebase_contribution": "Extract company names, industries, and decision-maker contacts from the scraped profiles. Focus on companies with 50-200 employees in the SaaS space."
      }
    }
  ]
}
```

`knowledgebase_contribution` is a plain string instruction that tells the KB update agent **what to extract** from this step's output and how to represent it in the graph. It is:
- Only relevant when `knowledgebase_access` is `"write"` or `"read-write"`
- Set by the user in the workflow interactive builder (not auto-generated)
- The trigger for the KB update agent — if not set, the KB update agent does not run for that step even if write access is granted

Valid values:

| Value | Read | Write | Notes |
|-------|------|-------|-------|
| `"read-write"` | yes | yes | Default when knowledgebase is enabled |
| `"read"` | yes | no | Step can consume but not modify |
| `"write"` | no | yes | Step can append/update but not read |
| `"none"` | no | no | Equivalent to `disable_knowledgebase: true` |

`DisableKnowledgebase *bool` is kept for backward compatibility and maps to `"none"` / `"read-write"`.

### Resolution logic

```
resolveKnowledgebaseAccess(stepConfig, presetEnabled):
  1. knowledgebase_access field set → use it directly
  2. disable_knowledgebase: true    → "none"
  3. disable_knowledgebase: false   → "read-write"
  4. preset enabled                 → "read-write"
  5. preset disabled                → "none"
```

### Key implementation touchpoints

| File | Change |
|------|--------|
| `planning_agent.go:227` | Add `KnowledgebaseAccess string` and `KnowledgebaseContribution string` to `AgentConfigs`; keep `DisableKnowledgebase` as deprecated |
| `controller_execution.go:1065` | Replace bool resolution with `resolveKnowledgebaseAccess()` helper |
| `controller_agent_factory.go:397` | `setupExecutionFolderGuard` takes `kbAccess string` instead of `...bool`; adds to read/write paths separately |
| `controller_agent_factory.go:806` | Same for orchestrator agent guard |
| `controller_learn_code.go:642` | Use same `resolveKnowledgebaseAccess()` helper |
| `execution_only_agent.go` | Prompt reflects actual access mode (e.g. `READ` vs `READ/WRITE`) |
| `delegating_agent_agent.go` | Same |

---

## 4. Knowledgebase Graph Structure

### Motivation

Currently the knowledgebase has no enforced shape — agents write whatever they want. This makes it hard to query, merge across runs, or reason about what's known. A **file-based graph structure** gives it a fixed, queryable shape without requiring a graph database.

Inspired by graph-based agent memory research (Graphiti, GraphRAG, AGENTiGraph): knowledge is modelled as **entities (nodes)** and **relationships (edges)**. The basic unit is a triple: `(subject, predicate, object)`. Each fact has provenance (which step/run created it) and a validity window (when it was created, whether it was superseded).

### File layout

```
knowledgebase/
  graph.json    ← source of truth: entities + relationships
  index.json    ← lightweight overview: counts, types, last updated
  raw/          ← unstructured source material if needed
```

Execution agents get the `knowledgebase/` path injected (not the content — same as today). They read what they need via shell:

```bash
cat knowledgebase/index.json                                           # quick overview
cat knowledgebase/graph.json | jq '.entities[] | select(.type=="company")'
```

`graph.json` serves both consumers: the KB update agent (merge logic) and execution agents (on-demand reads). No separate summary file needed.

### `graph.json` schema

```json
{
  "version": "1",
  "updated_at": "2026-04-15T10:00:00Z",
  "entities": [
    {
      "id": "company-acme",
      "type": "company",
      "label": "Acme Corp",
      "properties": {
        "website": "https://acme.com",
        "industry": "SaaS"
      },
      "created_at": "2026-04-15T09:00:00Z",
      "source": { "step": "step-1", "run": "iteration-2/group-a" }
    }
  ],
  "relationships": [
    {
      "id": "rel-001",
      "from": "company-acme",
      "to": "person-john",
      "type": "has_contact",
      "properties": {
        "role": "CEO"
      },
      "created_at": "2026-04-15T09:00:00Z",
      "source": { "step": "step-2", "run": "iteration-2/group-a" }
    }
  ]
}
```

### `index.json` schema

A lightweight summary so agents can decide whether to load the full graph:

```json
{
  "entity_count": 42,
  "relationship_count": 87,
  "entity_types": ["company", "person", "product", "event"],
  "relationship_types": ["has_contact", "owns", "competes_with"],
  "last_updated": "2026-04-15T10:00:00Z",
  "last_updated_by": { "step": "step-3", "run": "iteration-2/group-a" }
}
```

### Key design principles

- **Agents write via the KB update agent** (see section 5) — step execution agents do not write directly to `graph.json`
- **Agents read freely** — execution agents with `knowledgebase_access: read` or `read-write` can shell-read `graph.json` or `index.json` directly
- **No graph DB required** — plain JSON files, readable with any shell tool (`cat`, `jq`)
- **Source provenance on every fact** — every entity and relationship records which step/run wrote it, enabling auditability and conflict detection
- **Purely JSON** — no markdown files in the knowledgebase; `graph.json` is the source of truth for both machine merging and LLM reading

---

## 5. KB Update Agent (Post-Step)

### What it is

A dedicated agent that runs **after each step completes** (parallel to the success learning agent) and updates `knowledgebase/graph.json` with facts extracted from that step's output. Mirrors the learning agent pattern exactly.

### When it runs

Both the success learning agent and the KB update agent run through **single-writer queues** — one worker goroutine per queue, draining jobs FIFO. Step completion enqueues and returns immediately; the workers process one agent at a time.

```
Step execution
    ↓
Validation passes
    ↓
enqueue(learningQueue, job)   → learning worker runs one agent at a time → writes learnings/_global/
enqueue(kbUpdateQueue, job)   → kb worker runs one agent at a time       → writes knowledgebase/graph.json
```

The two queues are independent — a learning agent can run in parallel with a KB update agent (they write different files), but two learning agents never run concurrently, and two KB update agents never run concurrently.

The KB update agent only enqueues if **all three** conditions are met:
- Knowledgebase is enabled at the preset level
- `knowledgebase_access` for the step is `"write"` or `"read-write"`
- `knowledgebase_contribution` is set for the step (non-empty)

If `knowledgebase_contribution` is not set, the job is skipped entirely — there is no generic fallback extraction.

### Serialization (learning and KB writes)

Both `learnings/_global/` and `knowledgebase/graph.json` are workspace-shared files merged from multiple step outputs. Running merges concurrently creates lost-update races. The fix is structural: a single worker per file group, with a buffered channel for incoming jobs.

```go
type KBUpdateJob struct {
    StepID      string
    OutputPath  string
    Contribution string
}

var kbUpdateQueue = make(chan KBUpdateJob, 100)

func startKBUpdateWorker() {
    go func() {
        for job := range kbUpdateQueue {
            runKBUpdateAgent(job) // one at a time, FIFO
        }
    }()
}
```

Properties this gives:
- **No merge races** — only one goroutine ever reads-modifies-writes `graph.json`
- **FIFO by completion order** — step-N's update sees the effects of step-(N-1)'s update, enabling cumulative entity/relationship linking instead of duplicate-then-reconcile
- **Non-blocking on step execution** — the enqueue is O(1); step completion never waits for learning or KB work
- **Full-run KB update shares the queue** — the manual `kb-update` phase (section 6) enqueues onto the same `kbUpdateQueue`, so it can't race with a live step's post-step update

Two separate queues:
- `learningQueue` → one worker → all learning agents serialized
- `kbUpdateQueue` → one worker → all KB agents (post-step and full-run) serialized

The learning agent today fires in a bare goroutine (`controller_learning.go:432`) with no serialization. It has the same latent race on `learnings/_global/` that the KB agent would have on `graph.json`; the same queue pattern retrofits cleanly and closes both gaps at once.

File writes inside each worker use write-to-temp-then-rename for crash safety:
```go
tmp := path + ".tmp"
os.WriteFile(tmp, data, 0o644)
os.Rename(tmp, path) // atomic on POSIX
```

### What it reads

- Step execution output (`runs/{runFolder}/execution/{step-id}/`)
- `knowledgebase_contribution` from step config — the user-defined extraction instruction
- Current `knowledgebase/graph.json` (to merge, not overwrite)
- `knowledgebase/index.json` (for context on what's already known)

### What it writes

- Updated `knowledgebase/graph.json` — merges new entities and relationships
- Updated `knowledgebase/index.json` — refreshed counts, types, last_updated

### Agent instructions

The `knowledgebase_contribution` string is the primary directive. Fixed rules on top:
- Extract **WHAT** the workflow discovered — entities, facts, relationships found in the output
- Do NOT capture HOW the step ran (that's for learnings)
- Merge carefully — do not duplicate existing entities, update properties if newer
- Record `source` on every new entity and relationship

### Key implementation touchpoints

| File | Change |
|------|--------|
| `controller_execution.go:2055` | Enqueue KB update job after validation passes (alongside learning enqueue) — no direct goroutine spawn |
| New file: `controller_kb_update.go` | KB update orchestration + `kbUpdateQueue` channel and worker (mirrors `controller_learning.go`) |
| New file: `kb_update_agent.go` | Agent definition and system prompt (mirrors `learning_agent.go`) |
| `controller_agent_factory.go` | Factory method for KB update agent |
| `controller_learning.go:432` | **Retrofit:** replace bare `go func()` with `learningQueue` enqueue; add `learningQueue` channel + worker at package init |
| Server startup (`cmd/server/server.go` or equivalent) | Start `learningQueue` and `kbUpdateQueue` workers once at process init |

### Folder guard for KB update agent

- **Read**: `runs/{runFolder}/execution/{step-id}/` + `knowledgebase/`
- **Write**: `knowledgebase/` only

---

## 6. Global KB Update Tool (Full-Run)

### What it is

A **manual phase** (`kb-update`) that processes all step outputs from a completed run and rebuilds or enriches the knowledgebase from scratch. Analogous to the report generation phase — triggered from the toolbar, not automatic.

Use cases:
- Knowledgebase was disabled during a run, now you want to populate it retrospectively
- A run completed before the KB update agent existed
- `knowledgebase_contribution` was updated after the run and you want to re-extract

Per-step `knowledgebase_contribution` from `step_config.json` drives the extraction — steps without a contribution defined are skipped, same as the automatic post-step flow.

### Flow

```
User clicks "Update KB" in toolbar → selects run folder
    ↓
POST /api/workflow/knowledgebase/update
    ↓
KBUpdateOrchestrator reads all outputs from runs/{runFolder}/execution/
    ↓
For each step with a non-empty knowledgebase_contribution:
    enqueue(kbUpdateQueue, job)   ← same queue used by post-step updates
    ↓
KB worker drains jobs FIFO — serialized against any live post-step KB updates
    ↓
graph.json + index.json written/merged
    ↓
Frontend: KnowledgebaseView refreshes
```

**Why share the queue:** the full-run phase could otherwise race with a still-running workflow's post-step KB updates, producing lost merges. Enqueuing onto `kbUpdateQueue` means the worker processes manual-phase jobs and live-step jobs in the order they arrive, with no concurrent writers to `graph.json`. The manual phase does not wait for an empty queue before returning to the user — it enqueues and reports a job count; progress is visible via the usual event stream.

### HTTP endpoint

```
POST /api/workflow/knowledgebase/update
Body: { workspace_path, run_folder, merge_strategy: "merge" | "replace" }
```

- `"merge"` — adds new entities/relationships, updates existing ones if newer
- `"replace"` — clears knowledgebase and rebuilds from this run's outputs

### Key implementation touchpoints

| File | Change |
|------|--------|
| `workflowtypes/types.go` | Add `WorkflowStatusKBUpdate = "kb-update"` |
| `workflow_orchestrator.go` | Route `kb-update` status to `runKBUpdateOnly()` |
| New file: `kb_update_execution.go` | Full-run KB update logic (mirrors `evaluation_execution.go`) |
| `cmd/server/workflow.go` | Add `handleKBUpdate()` handler + register route |
| `cmd/server/server.go` | Register `POST /api/workflow/knowledgebase/update` |

---

## 7. Interactive Builder Changes

The workflow interactive builder is a chat-based phase (`workflow-builder`) that lets users modify the workflow plan via natural language. The following UI additions are needed to support the features above.

### New: KB Management Popup

A new popup component (`KnowledgebaseView.tsx`) parallel to `LearningsView.tsx` and `FinalOutputPopup.tsx`:

- **View**: renders `knowledgebase/index.json` summary (entity count, types, relationship types, last updated)
- **Explore**: expandable entity list from `graph.json`
- **Run KB Update**: triggers the full-run KB update phase (section 6) for the selected run folder
- **Clear**: deletes and recreates `knowledgebase/graph.json` and `index.json`
- **Export**: downloads `graph.json`

### New: KB Update toolbar button

In `WorkflowToolbar.tsx` alongside the existing "Final Report" button:

```
"Update KB" → opens KnowledgebaseView → user selects run folder → clicks "Run KB Update"
    ↓
handleRunKBUpdate() → onStartPhase('kb-update', { selected_run_folder })
```

### Updated: Step config UI — KB access mode + contribution

In the per-step config panel, replace the existing `disable_knowledgebase` toggle with:

```
Knowledgebase access: [ read-write ▼ ]
                       read
                       write
                       read-write
                       none

KB contribution:
┌─────────────────────────────────────────────────────────┐
│ Extract company names, industries, and decision-maker   │
│ contacts from the scraped profiles. Focus on companies  │
│ with 50-200 employees in the SaaS space.                │
└─────────────────────────────────────────────────────────┘
```

- The contribution textarea is only shown when access is `"write"` or `"read-write"`
- Saving writes both `knowledgebase_access` and `knowledgebase_contribution` to `step_config.json`
- The interactive builder (workflow-builder chat phase) can also set these fields via plan modification tools — `knowledgebase_contribution` should be one of the fields the builder agent can populate when it adds or configures a step

### Updated: Preset modal

The existing `use_knowledgebase` toggle in `PresetModal.tsx` stays as the workflow-level gate. No change needed here.

### New: `db/` schema notes owned by the builder

Because `db/` is a single workspace-level store shared by all groups and reruns (see section 1), the builder is the only place where the full picture — which steps write which files, which widgets read them — exists. When the builder adds or updates a step that writes to `db/`, it must also declare the schema for each output file: primary key, group coexistence, merge rule.

This is part of the same builder flow that produces `report_plan.md`, since the write side (step) and read side (widget) reference the same `db/` file. Implies either a new plan-modification tool (`set_db_schema`) or an extension to `add_scripted_step` / `update_scripted_step` so the schema note is persisted alongside the step's db output declaration rather than left to the step agent to infer at runtime.

### Key implementation touchpoints

| File | Change |
|------|--------|
| New: `frontend/src/components/workflow/KnowledgebaseView.tsx` | KB management popup |
| `WorkflowToolbar.tsx` | Add "Update KB" button + `handleRunKBUpdate()` |
| Step config panel component | Replace `disable_knowledgebase` toggle with `knowledgebase_access` dropdown |
| `frontend/src/services/api.ts` | Add `updateKnowledgebase()`, `getKnowledgebaseIndex()`, `clearKnowledgebase()` API calls |
| `frontend/src/services/api-types.ts` | Add `KBIndex`, `KBUpdateRequest`, `KBUpdateResponse` types |

---

## 8. `soul.md` — Stable Workflow Intent

### What it is

A Markdown file containing the workflow's stable north star. Runtime, evaluation,
Pulse, and builder agents read its `## Objective` and `## Success Criteria`
sections to decide what the workflow must achieve.

### What it is NOT

- Not architecture or a design document — the current "how" remains revisable in `planning/plan.json`, step config, and implementation artifacts
- Not a decision log, navigation index, reference list, or store for agent assumptions
- Not workflow config — capabilities, LLM settings, schedules stay in `workflow.json`
- Not execution knowledge — HOW to run steps stays in `learnings/`
- Not business context, examples, or domain facts — those stay in `knowledgebase/`

### What it contains

It contains:

1. `## Objective` — the outcome the workflow exists to produce.
2. `## Success Criteria` — observable, checkable conditions for success.
3. Optional `## Constraints` — only boundaries explicitly stated or approved by
   the user as durable and non-negotiable.
4. Optional `## Notifications` — the user's Pulse notification preference.

Architecture, providers, models, channels, thresholds, step boundaries, tactics,
and other implementation choices are not stable merely because an agent wrote
them down. Pulse and Goal Advisor may challenge and replace them when evidence
supports a better approach.

### Relationship to `objective` and `success_criteria`

`soul.md` is the canonical source. `planning/plan.json` is the current
implementation attempt and no longer owns root objective/success fields. Runtime
helpers parse the two required H2 sections directly from `soul.md`.

### Format

```markdown
# LinkedIn Outreach

## Objective
Build a qualified list of prospects that can support effective outreach.

## Success Criteria
- Produce at least 20 verified, relevant leads per run.
- Keep duplicate and invalid-contact rates below the user-approved threshold.

## Constraints
- Never contact a prospect without human approval. <!-- only if user-approved -->

## Notifications
- Send a summary after every scheduled run.
```

### When the builder writes to it

- When the user confirms or changes the objective or success criteria.
- When the user explicitly marks a boundary as a durable workflow-wide constraint.
- When the user changes Pulse notification preferences.

Do not silently promote agent-inferred assumptions or implementation decisions
into `soul.md`. When authorship or durability is unclear and materially affects
the goal, Pulse surfaces it under **Assumptions challenged** for user review.

### Location

```
soul/soul.md
```

Dedicated top-level folder at workspace root, parallel to `planning/`, `db/`, `knowledgebase/`, and `learnings/`. It stays separate because it defines the stable outcome that the current plan is judged against.

The `soul/` folder is added to the builder's writable folder-guard paths. Workshop/Pulse may update it under the stable-intent contract; execution and evaluation agents read it but treat it as read-only.

### Key implementation touchpoints

| File | Change |
|------|--------|
| `controller_run_manager.go` | Create `soul/` folder on workspace init (alongside `db/`, `knowledgebase/`, etc.) |
| Builder folder-guard setup (`interactive_workshop_manager.go` / `controller_agent_factory.go`) | Add `soul/` to the builder's writable paths so shell writes to `soul/soul.md` go through |
| `interactive_workshop_manager.go` | Load `soul/soul.md` at session start; inject as `SoulContent` template var into all workshop mode prompts |
| `interactive_workshop_manager.go` | After user-confirmed objective/success/constraint changes, builder updates `soul/soul.md` via shell (guard permits it; no dedicated mutation tool needed) |
| `cmd/server/workflow.go` | Add `handleGetSoul()`, `handleUpdateSoul()` HTTP handlers |
| `pre_validation.go:1080` | Add `"soul"` to known workflow folder names |
| `WorkflowToolbar.tsx` | Expose `soul.md` in the workspace file browser (already visible if file exists) |

---

## Implementation notes (2026-04)

Written against the shipping code after the bulk of this design landed. Captures deltas from the sections above — read the originals first, treat this as the "what actually happened" addendum. The original sections are left as the design intent; where reality diverged, this section wins.

### Phase sequencing (what shipped when)

| Phase | Scope | Status |
|---|---|---|
| **1** | `db/` folder + folder guards + `DBWrite` eval flag; KB access control (`knowledgebase_access` + `knowledgebase_contribution`); `soul/soul.md` | Shipped |
| **2** | KB graph scaffolding (empty seed only); `learningQueue` + `kbUpdateQueue` single-writer queues; learning retrofit; KB update agent + post-step trigger; `reorganize_knowledgebase` builder tool | Shipped |
| **3** | Dynamic report system — `ReportViewer.tsx` + parser + toolbar wiring; removal of the old `final_output.go` report agent and everything that fed it | Shipped |
| **4** | `KnowledgebaseView.tsx` (KB viewer + clear + export) | Shipped (partial — see §Deferred) |
| **Deferred** | Full-run `kb-update` phase (§6), step-config UI panel for `knowledgebase_access` / `knowledgebase_contribution`, Phase 4 `run_full_kb_update` button | Not shipped |

### Major change: Go does NOT parse `graph.json` or `index.json`

Sections 4 and 5 originally envisioned Go-side types (`KBGraph`, `KBEntity`, `KBRelationship`), a `KBDelta` handshake type returned from the KB update agent, `ApplyKBDelta` for Go-side merging, and `RebuildKBIndex` for index regeneration.

**Actual implementation pivoted mid-build**: the LLM owns `graph.json` and `index.json` end-to-end via shell. Go does **not** parse, validate, or merge content. The only Go code touching these files is:

- `kb_graph.go` — 50 lines; two string-literal seed files (`emptyGraphJSON`, `emptyIndexJSON`) and `InitKBGraphFiles` which writes them once on workspace init when they don't exist. No types, no parsing.
- The KB update agent prompt (`kb_update_agent.go`) — describes the schema in natural language; the agent reads, merges, and writes via `cat`, `jq`, `diff_patch_workspace_file`.

**Consequence:** if the agent's prompt evolves the schema (e.g. adds a `confidence` field), Go's seed files must be updated manually to match. Documented as a constraint at the top of `kb_graph.go`.

### Major change: per-step KB access defaults to `"none"`, not `"read-write"`

Section 3's resolution logic specified:

```
preset enabled + no explicit step setting → "read-write"
```

**Actual implementation is stricter**: default is `"none"` regardless of preset state. KB access is opt-in per step.

```go
if !presetEnabled { return KBAccessNone }       // preset is hard gate
if explicit setting { return explicit }          // honor it
return KBAccessNone                              // default: disabled
```

Rationale: the prior default let any step shell-write into `knowledgebase/` despite the prompt saying "only the KB update agent writes graph.json". Opt-in by default removes the accidental-shell-write path.

**Breaking consequence:** existing workflows that implicitly used KB read access (any step doing `cat knowledgebase/graph.json` without declaring `knowledgebase_access`) will fail folder guard after this ships. Fix per-workflow by setting `"knowledgebase_access": "read"` on affected steps.

### Major change: deprecated fields fully deleted

The design (§3) proposed keeping `DisableKnowledgebase *bool` as a backward-compat shim indefinitely. **Actual:** the shim has been deleted outright. `LearningAfterLoopIteration` (already marked deprecated in the struct) is also gone. Any step_config.json with `"disable_knowledgebase": true` or `"learning_after_loop_iteration": ..."` is silently ignored after this ships.

The `update_step_config` tool's JSON schema no longer exposes either. The builder agent only sees `knowledgebase_access` + `knowledgebase_contribution` as the KB-control knobs.

### Major change: `reorganize_knowledgebase` builder tool (new, not in original design)

Added during Phase 2. The interactive builder can now apply natural-language transformations to `graph.json` (dedupe, rename types, purge bad provenance) via a dedicated one-shot agent `KBReorganizeAgent`. Serialized through the same `kbUpdateQueue` as the post-step KB update agent so they can't race.

- Tool name: `reorganize_knowledgebase(instruction: string)`
- Agent: `KBReorganizeAgent` in `kb_update_agent.go`
- Orchestration: `runKBReorganizePhase` / `RunKBReorganize` in `controller_kb_update.go`
- Registration: `RegisterReorganizeKnowledgebaseTool` in `planning_exports.go`
- Available in: workflow-builder mode only

### Major change: Section 2 report uses existing workspace API, not new wrappers

Section 2 listed `handleGetReportPlan` and `handleGetDBFile` as new HTTP handlers to write.

**Actual implementation**: those wrappers were built, then deleted. `ReportViewer.tsx` uses the existing workspace document API (`agentApi.getPlannerFileContent` → `/api/documents/{path}`) directly. No new backend endpoints. This removes ~80 lines of Go duplication that mirrored what the workspace service already exposes.

**Path whitelist deferred**: the original design included a server-side whitelist restricting widget sources to `db/**` and `knowledgebase/graph.json` / `index.json`. With the wrappers removed, the whitelist is gone too — widgets can technically point at any workspace file. The builder is expected to keep widget sources sensible (design doc already makes this a builder responsibility).

### Major change: `ChartBlock` removed from `MarkdownRenderer.tsx`

Section 2 originally planned to reuse the SVG bar/line chart in `MarkdownRenderer.tsx`'s existing `ChartBlock` component for `widget:chart` rendering. **Actual**: the markdown chart rendering was removed entirely (not used anywhere else in the app), and `ReportViewer.tsx` ships a simple HTML/CSS bar chart inline instead.

If SVG/line charts become necessary later, lift a small `Chart` component to `frontend/src/components/ui/Chart.tsx` and have both the markdown renderer and the report viewer consume it. Don't re-add chart rendering into `MarkdownRenderer`.

### Major change: scheduler auto-report generation removed

Section 6 described a full-run `kb-update` phase but didn't discuss what happens to the old scheduler behavior of auto-generating reports after workflow runs. **Actual**: `shouldAutoGenerateWorkshopReport` and `generateWorkshopScheduleReport` helpers in `cmd/server/scheduler.go` have been deleted. The dynamic report is a live frontend view — there is no artifact to generate post-run, so scheduled runs now complete without a report side-effect.

### Prompt changes (touching all three agent classes)

All three agent prompts now explicitly distinguish the three persistent stores:

- **`learnings/`** → HOW to run (methods, patterns, quirks)
- **`knowledgebase/`** → decisions, facts, strategies built up over time (entities + relationships; **only** the post-step KB update agent writes `graph.json`, never a step)
- **`db/`** → workflow state and results (rows produced/consumed this run; step-owned; upsert-by-key; never overwrite wholesale)

Applied in:
- Execution agent prompt (`execution_only_agent.go`)
- Todo-task orchestrator agent prompt (`delegating_agent_agent.go`)
- Workshop builder prompt (`interactive_workshop_manager.go`)

The workshop builder prompt also carries the full three-way comparison table and "when to use which" deciding questions so the builder agent can recommend the right store to users asking "where should I put X?"

### Concurrency model landed as single-writer queues (not mutex)

Section 5 considered three options: per-file mutex, single-writer queue, or append-only journal. **Implemented: single-writer queues**, via `queues.go`:

- `learningQueue` — one worker, FIFO; serializes the learning agent. Retrofitted into both the tracked (workshop) and fallback (non-workshop) launch sites. Closes a pre-existing race on `learnings/_global/` files.
- `kbUpdateQueue` — one worker, FIFO; serializes post-step KB updates **and** `reorganize_knowledgebase` tool invocations (both flows enqueue onto the same channel).

Workers lazy-start via `sync.Once` on first enqueue — no explicit boot wiring. Panic guard per job so a bad agent run can't kill the worker.

### Files actually touched

| Area | New files | Modified | Deleted |
|---|---|---|---|
| Persistent stores | `kb_graph.go`, `kb_update_agent.go`, `controller_kb_update.go`, `queues.go`, `workshop_helpers.go` | `controller_execution.go`, `controller_run_manager.go`, `controller_agent_factory.go`, `controller_todo_task.go`, `controller_learning.go`, `controller_batch_execution.go`, `controller_learn_code.go`, `evaluation_types.go`, `execution_only_agent.go`, `delegating_agent_agent.go`, `planning_agent.go`, `pre_validation.go` | — |
| Report system | `ReportViewer.tsx`, `reportPlanParser.ts` | `interactive_workshop_manager.go` (prompts + tool schema), `api-types.ts`, `api.ts` | `final_output.go`, `FinalOutputPopup.tsx` |
| KB UI | `KnowledgebaseView.tsx` | `WorkflowToolbar.tsx` (Database icon, popup wiring) | — |
| Workflow-level | — | `workflow.go`, `server.go`, `scheduler.go`, `workflow_orchestrator.go`, `types.go` (workflowtypes) | — |
| Markdown renderer | — | `MarkdownRenderer.tsx` (deleted ChartBlock + chart language branch) | — |

Rough LOC impact: **+~2,800 net added, ~5,200 removed** (dominated by the old report-agent deletion).

### Not yet shipped (§Deferred, per original phase 4)

- **Full-run `kb-update` phase (§6)** — manual "rebuild KB from a completed run" trigger with its own HTTP endpoint and orchestrator routing. `KnowledgebaseView` reserves space for a button but doesn't call it.
- **Step-config UI panel** — `knowledgebase_access` dropdown and `knowledgebase_contribution` textarea. Right now the builder must set these via `update_step_config` in chat or via hand-edited `step_config.json`.
- **Scheduler "Run KB Update" integration** — the scheduler could offer a post-run KB rebuild option; out of scope until §6 ships.

---

## References
