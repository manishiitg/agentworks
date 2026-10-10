# Shared knowledge bases through workflow references

Status: deployed to server A; fresh real-workflow attachment acceptance remains unverified in this deployment check. Updated 2026-09-17: write access shipped (not yet deployed to server A) — see PLAT-325, a follow-up to PLAT-310.

## Purpose

Let a workflow use knowledge maintained by other workflows through the same shell
and file access it uses for its local knowledge base. Builders, execution agents,
and reviewers should discover the attached knowledge and have appropriate access.

Workflow boundaries follow work that needs to be planned, executed, and evaluated
together. A need for common knowledge should not force otherwise independent work
into one workflow.

For example, server A performance, security, and cost workflows could read architecture,
service descriptions, and API contracts maintained by an existing server A workflow.
Each consumer would retain its own local investigation notes.

## Decisions

- Configure attachments in the consuming workflow's `workflow.json`.
- Reference source workflows by stable ID, not a copied host path or display name.
- Allow multiple sources, each with a unique alias.
- Keep the source workflow's files canonical. Read them directly without copying,
  syncing, merging indexes, or requiring a new KB query tool.
- Make source knowledge accessible to the builder as well as eligible execution
  and review agents. Preserve ordinary shell use, including `cat`, `rg`, and scripts.
- Attach only the source workflow's own KB. Attachments are not transitive.
- Started with read-only attachments on the same execution host (first release).
  `access: "write"` (PLAT-325) adds notes/-only shared contribution, gated by an
  explicit grant from the source workflow's own owner — see "Write access"
  below. Access across servers remains a later extension.
- Existing workflows retain their current local knowledge and behavior when the
  new configuration field is absent.

## Configuration

Configure `workflow.json` through the workflow configuration API or builder tool (the IDs below are illustrative):

```json
{
  "knowledgebase_sources": [
    {
      "workflow_id": "wf_server A",
      "alias": "server A",
      "access": "read"
    },
    {
      "workflow_id": "wf_security",
      "alias": "security",
      "access": "read"
    }
  ]
}
```

| Field | Meaning |
| --- | --- |
| `workflow_id` | Stable ID of the source workflow, resolved through the authorized workflow registry |
| `alias` | Local name for this attachment; use lowercase letters, digits, and underscores, starting with a letter |
| `access` | `read` (default) or `write` (PLAT-325); unknown access modes are rejected |

Reject duplicate aliases, duplicate source IDs, self-references, inaccessible source
workflows, and sources on another execution host. Do not accept arbitrary filesystem
paths in this field. Validate IDs against the application's existing ownership and
access rules; editing JSON alone must not grant access to another owner's data.

A workflow may attach up to 20 sources. Aliases are at most 48 characters; `access`
is reserved because `WORKFLOW_KB_ACCESS` tracks the session’s KB access mode.

Access is checked for the entire consuming workflow audience: its owners and
readers must be allowed to read the source. A broadly visible consumer cannot
attach a private source merely because its current editor owns both. Legacy
workflows without an effective owner remain account-visible under existing rules.

The source workflow can itself have attachments, but resolving a reference stops
at that workflow's local `knowledgebase/`. Even mutual references must not trigger
recursive resolution or expand either workflow's access.

## Implementation

`getKnowledgebasePath(workspaceRoot)` resolves
`<workflow>/knowledgebase/`. That folder persists across runs of the same workflow.
It contains user-owned `context/` and workflow-maintained `notes/`, including
`notes/_index.json`. Local contribution and maintenance rules remain unchanged.

The existing attached-folder mechanism that resolves canonical host paths,
adds read/write permissions, exposes `WORKFLOW_FOLDER_*` environment variables,
and describes available folders in agent context. Live builder sessions can refresh
those grants. KB sources reuse this permission plumbing while retaining workflow
references as the saved configuration.

Relevant implementation entry points:

- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_execution.go`:
  local KB path and access mode resolution.
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_agent_factory.go`:
  agent read/write paths and KB access (`setupExecutionFolderGuard`).
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_message_sequence.go`:
  message_sequence item write paths (`setupMessageSequenceFolderGuard`).
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_orchestrator.go`:
  orchestrator's own top-level turn write paths (`setupOrchestratorFolderGuard`).
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/workflow_folder_access.go`:
  attached-folder resolution, environment variables, prompts, session refresh, and
  (PLAT-325) `writableExternalKBNotesPaths`.
- `agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go`:
  `update_workflow_config(knowledgebase_sources=..., kb_write_grants=...)` and
  `get_workflow_config` surfacing.
- `agent_go/pkg/common/types.go`: shell session grant reconciliation.
- `agent_go/pkg/workflowtypes/knowledgebase_sources.go`: configuration and syntax validation.
- `agent_go/pkg/workflowkb/`: local ID discovery, audience checks (`AudienceCanRead`,
  PLAT-325's `AudienceCanWrite`), canonical resolution, and confined file reads.
- `agent_go/cmd/server/workflow_access.go`: `WorkflowAccess.AllowedKBWriters` (PLAT-325).
- `agent_go/cmd/server/workflow_knowledgebase_routes.go`: consumer-scoped browsing API.
- `agent_go/cmd/server/workflow_manifest_routes.go`: manifest update endpoint, including
  (PLAT-325) `kb_write_grants`.
- `frontend/src/components/workflow/KnowledgebaseView.tsx`: knowledge browsing UI.
- `frontend/src/components/workflow/KnowledgebaseSources.tsx`: attach/detach UI and
  (PLAT-325) the write-access choice and grantor-side `KBWriteGrants` panel.

Session grant reconciliation recognizes both `WORKFLOW_FOLDER_*` and
`WORKFLOW_KB_*`. Each subsequent session policy lookup rebuilds these grants from
the saved configuration, removing stale KB paths while preserving independently
authorized folder access. Copied child sessions retain grant provenance too.

## Runtime access

At session creation, resolve every authorized source ID to its current canonical
KB folder on the execution host. Add only that folder to the relevant read grants;
a `"write"` source (PLAT-325) additionally adds the source's `notes/` subfolder —
never the KB root — to the relevant write grants, gated by the same
`knowledgebase_access` check that gates local KB writes. A `"read"` source is
kept out of write grants entirely. Expose one environment variable per
attachment:

```text
WORKFLOW_KB_server A=<resolved server A workflow>/knowledgebase
WORKFLOW_KB_SECURITY=<resolved security workflow>/knowledgebase
```

Then the agent can use the shell normally:

```bash
cat "$WORKFLOW_KB_server A/notes/_index.json"
rg -n "livekit" "$WORKFLOW_KB_server A/notes"
```

The runtime applies these grants to the command path guard, session shell
environment, and operating-system sandbox. An environment variable alone is not
a permission grant. Explicit KB opt-out removes both source grants and variables.

| Consumer | Access |
| --- | --- |
| Builder | Read every authorized attached KB through shell and supported file reads |
| Execution agent | Read attached KBs when its KB access mode permits reading; write into a `"write"` source's `notes/` when the step's own `knowledgebase_access` permits writing (PLAT-325); explicit `knowledgebase_access: none` opts out of both |
| message_sequence item | Same read/write rule as execution agents, narrowed further by the item's own `write_access.knowledgebase`/`kind: "knowledgebase"` (PLAT-325) |
| Scripted execution | Receive the same eligible read/write grants and environment variables as agent execution |
| Strategic and architecture reviewers | Discover and read attached KBs within their review profile; no write authority |
| KB maintenance/consolidation | Continue maintaining local notes only; do not reorganize source KBs |

## Write access (PLAT-325)

A consumer attaches a source with `access: "write"` (which also grants read).
This alone grants nothing: the source workflow's own owner must separately name
the consumer's workflow ID in `access.allowed_kb_writers` on the source's own
manifest, set via `update_workflow_config(kb_write_grants=[...])` or the
workflow manifest API's `kb_write_grants` field. `workflowkb.AudienceCanWrite`
requires both the ordinary read-audience check and this explicit grant; a
`"write"` source without a grant resolves as unavailable with a reason
distinct from an ordinary access-denied read.

Writes are confined to the source's `notes/` folder — never `context/`, never
the KB root, never `notes/_index.json` unless a step's normal note-authoring
flow would write it anyway. Contributed notes should tag the writing
workflow's ID (a frontmatter field, by convention) so the source workflow's own
agents can distinguish self-authored notes from contributed ones.

**Not built:** concurrency serialization. If two workflows' sessions write into
the same target `notes/` folder at the same moment, one write can be lost or
the file corrupted — this was an explicit scope cut, not an oversight. The
precedent to mirror if this needs solving later is
`learningsGlobalFileMutex` (`agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/learnings_direct.go`):
an in-process mutex keyed by the target's canonical `notes/` path, held for
the duration of the write turn.

Shell reads, supported file-read tools, and subprocesses use the session grants.
The Knowledge UI reads through `GET /api/workflows/knowledgebase-sources`, scoped
to the consumer's `workspace_path`. With no alias it returns attachment statuses;
with `alias` and a relative `path`, it reads a regular file inside the authorized
source KB (maximum 2 MB). It rechecks current attachments and access on each call.
Generic report endpoints do not acquire access to other workflows.

Only `knowledgebase/` is attached. This does not expose the source workflow's
`soul/`, `learnings/`, database, run outputs, credentials, or plan. Canonical path
validation and sandbox rules must prevent symlink escapes into those locations.

## Discovery and freshness

Agent context contains a compact source list: alias, workflow name/ID,
access, availability, environment variable, and index location. Agents read the
relevant index and selected documents when needed; do not inject every attached
document into every prompt.

Keep sources distinguishable. References to facts should name the source alias and
document, preserve available evidence and verification dates, and retain environment
or other applicability constraints. Shared documents are reference data, not a new
source of execution instructions or permissions.

Consumers see newly saved source content on subsequent reads. This is live file
access, not a frozen snapshot across an entire run. Source writers should use atomic
file replacement where appropriate, and readers should surface or retry a transient
index read failure rather than treating it as an empty KB.

Do not silently prefer local over shared knowledge, or one attachment over another,
when facts conflict. Use source evidence, scope, and verification dates; retain the
uncertainty when it cannot be resolved. Reading a source must not mark its content
fresh or rewrite the source workflow's freshness ledger.

## Attach, update, and detach

**Setup → Attached folders** manages shared KB attachments, with visible source
cards showing workflow name, alias, KB folder, shell variable, read-only access,
and any unavailability reason. Shared KBs appear separately from external folders.
Workflow editors can use **Attach knowledge**, choose a source and alias, and
remove an attachment with **Detach**. Readers can inspect but cannot manage sources.
The Knowledge UI keeps a compact local/shared source selector and read-only status
above its content. Attachment changes refresh other open source panels for the
same workflow; attachment controls live in Setup.
Unavailable attachments retain their reason and can be repaired or detached.

Builders use `get_workflow_config` to inspect attachments and discover eligible
source IDs. `update_workflow_config(knowledgebase_sources=[...])` replaces the
complete attachment list; preserve existing entries when adding one, and pass `[]`
to detach all. The workflow manifest update API accepts the same field. Both paths
validate sources before saving. Open builder sessions refresh their grants.

New execution and review sessions use the same resolver. A source rename or move
within the same host works through its stable ID without changing consumer config.

On detach, rename, or permission revocation, remove stale environment variables,
prompt entries, and KB-derived grants. Rebuild affected sandbox profiles before the
next command. Do not claim that changing session metadata retroactively revokes
access from an already-running subprocess; handle running processes explicitly when
immediate revocation is required.

If an existing source is deleted, becomes inaccessible, or is unavailable after a
restore, mark the attachment unavailable with a specific reason. Preserve the
reference for repair and never substitute an unrelated folder or create an empty
KB that appears valid. Agents may continue independent work, but must report a
blocked knowledge dependency when their task requires the missing source.

## Validation and rollout

Local validation passes for resolver and manifest checks, attachment API reads and
detach, session refresh and copied sessions, builder prompt discovery, and execution
KB opt-out. A native macOS sandbox test verifies shell reads, blocked shared writes,
blocked sibling reads, and blocked reads through a cached path after detach. Four
frontend tests cover attach, detach, reader controls, source browsing and errors;
TypeScript compilation passes.

The implementation has not been deployed to server A and no real workflows have been
attached automatically. After deployment, select sources in Knowledge or through
the builder configuration tool. No bulk note migration is required: consumers read
the existing source KB immediately. Reorganizing notes remains a separate operation
that preserves evidence and updates references.

## Acceptance checks

- A builder can attach two sources, inspect both indexes using shell commands, and
  read an update saved by the source without copying files.
- Eligible agent and scripted runs, plus both reviewer types, discover the same
  sources; a step with KB access disabled receives no KB-derived grants.
- Writes to a `"read"` attached KB fail even if the consumer has write access to
  its local KB. Writes to a `"write"` source succeed only in `notes/`, and only
  once the source workflow has granted this consumer in `kb_write_grants`
  (PLAT-325); before that grant exists, the same write attempt fails exactly
  like a `"read"` source.
- A source attachment never grants its sibling folders or transitive attachments.
- Invalid IDs, aliases, ownership, host placement, and symlink escapes are rejected.
- Detach and revocation refresh live sessions and remove obsolete KB-derived access
  while preserving unrelated authorized folder grants.
- Missing sources and conflicting facts remain visible instead of becoming empty,
  current, or successful knowledge.
- Source rename/move preserves references; old workflow configurations still work.
- Concurrent source updates do not cause consumers to mistake a read failure for
  an empty index or silently overwrite shared knowledge.

## Later extensions

Shared contribution (explicit contribution mode, eligible writers, notes-only
write paths, provenance) shipped as PLAT-325 — see "Write access" above.
Conflict handling and cross-workflow write serialization did not: PLAT-325
explicitly left concurrent-writer locking unbuilt, so two workflows writing
into the same target `notes/` at once can still race. That remains open.

Cross-server sharing needs an authenticated transport or shared storage with defined
freshness and access semantics. A workflow ID pointing to another server is not
enough to make its files readable by the local shell.

Standalone named KBs can be added later if knowledge should outlive its source
workflow. The first release uses existing workflow-owned KBs and does not require
a new storage service.
