# Agent Agent Steps

## Purpose

A `agent` is the canonical **agent step**: a persistent, ordered conversation with one coding agent. Use one large sequence per coherent shared-context span, when later turns must retain the reasoning and tool context from earlier turns.

The step may declare `predefined_routes`. Those routes are bounded specialist
capabilities, not a prescribed checklist: the sequence agent receives
`call_sub_agent` and decides at runtime whether, when, and how often to call a
specialist. With no routes it remains a single-agent sequence. Legacy
Saved legacy step names normalize to `agent` on read and are rewritten by the v1.0.47 contract migration.

Routed and non-routed sequences use the same execution system prompt and the
same default tool policy. Declaring routes does not switch the sequence to a
different agent profile; it only appends delegation guidance and enables the
sub-agent lifecycle tools.

The preferred shape is: complete the whole span, re-open authoritative evidence and prove every criterion, repair verified gaps, then double-check before the top-level validation gate. Require run-specific proof/provenance in the output. Use multiple large sequences only when contexts should not be shared because of security/credentials, independently rerunnable outputs or failure domains, clean-room independence, human/routing boundaries, or context contamination.

Supported item types:

- `user_message`: send one focused follow-up instruction.
- `foreach`: render and send one follow-up instruction for each row returned by a read-only SQLite query.
- `prevalidation`: run a backend validation gate and send concrete repair feedback to the same conversation when it fails.

Deterministic code is not a sequence item. Put code in a standalone `regular` step configured with `declared_execution_mode: "scripted"`. Connect it to adjacent conversational steps through explicit `context_dependencies`, `context_output`, and validation schemas.

## Shape

```json
{
  "id": "review-and-correct",
  "type": "agent",
  "title": "Review and correct",
  "description": "Review the latest generated report against the workflow goal.",
  "context_dependencies": [
    "runs/iteration-0/default/execution/generate/report.json"
  ],
  "context_output": ["report.json", "report_proof.json"],
	"items": [
    {
      "id": "prove",
      "type": "user_message",
      "message": "Re-open authoritative evidence, prove every criterion, identify unsupported conclusions, and write run-specific source references and criterion checks to report_proof.json."
    },
    {
      "id": "repair-and-double-check",
      "type": "user_message",
      "message": "Repair every verified gap, update the proof record, and double-check the complete report against the sources.",
      "write_access": { "db": true }
    }
	],
	"predefined_routes": [
		{
			"route_id": "source-checker",
			"route_name": "Source checker",
			"condition": "When a claim needs independent source verification",
			"sub_agent_step": {
				"type": "agent",
				"id": "source-checker",
				"title": "Source checker",
				"description": "Verify the requested claim against authoritative evidence.",
				"items": [{"id": "verify", "type": "user_message", "message": "Double-check the evidence and report discrepancies."}]
			}
		}
	],
  "validation_schema": {
    "files": [
      {"file_name": "report.json", "required": true, "validation_type": "json"},
      {"file_name": "report_proof.json", "required": true, "validation_type": "json"}
    ]
  }
}
```

The step `description` is the system-level charter shared by every turn. Items
are the actual user messages, in order, describing how the agent should carry
out and verify that charter.

The top-level `validation_schema` is the sequence's final contract. When it is present, the runtime automatically runs it after the configured work turns and before synthetic learning/knowledge closing turns. A failed gate is returned to the same conversation as a repair turn and retried. Explicit `prevalidation` items are only needed for intermediate checkpoints; a final explicit gate with the same schema is not run twice.

Every agent turn is persisted through the normal execution-log pipeline, including its conversation, tool and LLM calls, model, timing, token usage, and failure details. The sequence also writes the standard final execution summary after all turns and final validation complete, so Pulse and cost analysis read it like any regular step.

## Foreach

Use `foreach` when every selected database row must receive the same conversational treatment.

```json
{
  "id": "review-each-finding",
  "type": "foreach",
  "source_sql": "SELECT id, summary FROM findings WHERE status = 'open' ORDER BY id",
  "message": "Review finding {{.id}}: {{.summary}}",
  "max_iterations": 50
}
```

`source_sql` is read-only and runs against `db/db.sqlite`. Each row is bound to `.` in the Go template.

## Write Access

Reads and writes inherit the step-level DB, knowledgebase, and learnings configuration, matching regular execution steps. Most items require no access declaration.

Use a non-empty item override only to narrow a turn to selected stores:

```json
{
  "write_access": {
    "db": true,
    "knowledgebase": false,
    "learnings": false
  }
}
```

The override cannot grant access that the step does not have. Write access is folder-level. Per-file path lists are rejected because they create a misleading security boundary.

## Deterministic Fetching Before Agentic Processing

Use explicit plan steps:

```text
regular scripted: fetch-and-normalize-authoritative-data
  -> agent: analyze-verify-and-repair-from-fetched-data
```

The scripted step owns `code/fetch-and-normalize-authoritative-data/main.py`, batches related deterministic API/SDK calls or CLI commands under one source/auth/retry/output contract, and writes validated DB rows or an explicit output artifact with provenance and freshness. Its working directory is its own `code/<step-id>/` directory. This makes failures, retries, permissions, logs, and costs visible at the workflow-step level. The agent consumes those results for judgment, synthesis, semantic verification, and repair; it does not re-fetch or re-parse stable response shapes conversationally.

## Runtime artifact layout

An Agent step owns one artifact subtree:

```text
runs/<run>/execution/<parent-step>/
├── <parent outputs>
├── shared/
├── agents/<route-id>/
│   ├── session.json
│   └── calls/<call-id>/
└── scripts/
    ├── items/<item-id>/calls/<call-id>/
    └── routes/<route-id>/calls/<call-id>/
```

Each child receives its call directory as `STEP_OUTPUT_DIR` and may collaborate
through the owning parent subtree. A script always executes
`code/<script-step-id>/main.py` with `code/<script-step-id>/` as its working
directory. There is no fallback to flattened sub-agent, `agents`,
or older scripted-item artifact paths; workflows on older contracts must migrate.

When call selection requires judgment, use an agentic request-specification step before the scripted executor and a later agent to interpret the result. Do not create one scripted step per endpoint or tiny transform.

## Workflow Contract v1.0.10

Contract v1.0.10 removes legacy `type: "code"` items.

The v1.0.10 upgrade is started manually from the workflow's interactive Builder
chat and calls `migrate_agent_code_items`. Migrations run in order
and each completed migration must stamp its expected version. Schedules do not
run this migration; they continue against their saved contract, while manual
chat execution and direct webhooks remain blocked until incompatible legacy
artifacts are migrated.

Contract v1.0.11 then upgrades `builder/improve.html` to the schema-2 Pulse history contract. It preserves the time-ordered history while adding canonical Signals / Reflection / Improvements attribution used by the Pulse popup. The v1.0.10 migration keeps its own trusted finalizer, so workflows on v1.0.9 can still traverse both upgrades safely.

The migration automatically converts only unambiguous top-level sequences containing code items and their immediately following prevalidation gates. It:

1. Copies each script to `learnings/<step-id>/main.py`.
2. Creates a standalone scripted regular step for each code item.
3. Preserves cumulative dependencies, outputs, validation, and step configuration.
4. Replaces the legacy sequence only after the migrated plan validates.

Mixed conversational/code sequences, nested sequence code, `input_json`, missing outputs, and unusual write ownership are not guessed. The manual migration stops with `MESSAGE_SEQUENCE_CODE_MIGRATION_BLOCKED` and an actionable split requirement without stamping v1.0.10.

The runtime also rejects any remaining code item with a precise v1.0.10 upgrade message. This prevents an old workflow from starting and failing halfway through execution.

## Authoring Rules

- Keep each user message focused on one outcome.
- Treat the agent as the agent. Add `predefined_routes` only for bounded specialists the agent may choose dynamically; do not encode a fixed checklist as delegation.
- Use the same conversation only when shared context is valuable.
- Put the final deterministic acceptance contract in the top-level `validation_schema`. Use explicit prevalidation items only for intermediate checks, not subjective review.
- Use `foreach` only with bounded, read-only queries.
- Put all deterministic code in standalone scripted regular steps.
- Put fixed API/SDK/CLI fetching, pagination, stable parsing/normalization, and mechanical persistence in coherent scripted fetchers; feed their durable outputs to large agents.
- Pass data between steps through declared files or the workflow database, never hidden in terminal context.
