# Crew workflow step

**Status:** Implemented; follow-up review findings addressed.

## Summary

A workflow can use a Crew project as an executable step. The step invokes one
of the Crew's public or platform-internal triggers, waits for that trigger run
to finish, and saves the Crew's final text response in the calling workflow's
current iteration folder. Later workflow steps can read the Crew's persistent
project files through the platform's existing read-only attached-folder
mechanism.

This feature makes a maintained Crew available as a reusable specialist inside
many workflows. It does not turn Crew projects into workflows or add iteration
folders to Crew.

## Why use a Crew instead of an ordinary step?

An ordinary workflow step belongs to one pipeline. Its inputs, execution, and
outputs are scoped to one workflow run. A Crew is a long-lived project that a
person and multiple automations can cultivate over time.

A Crew can retain:

- human-curated memory and instructions;
- selected and configured skills;
- persistent files and attached folders;
- a project database and dashboard;
- main-chat conversation history;
- separate persistent trigger-conversation history;
- schedules, triggers, and bot routes.

Use an ordinary step when the work belongs to the current run and should be
reproducible from that run's inputs. Use a Crew when the responsibility,
knowledge, or human stewardship continues across runs and workflows.

Calling a Crew does not provide a more capable model by itself. Its advantage
is the persistent, human-maintained specialist context.

## Product model

The workflow Builder adds a step with `type: "crew"`. The step identifies an
accessible Crew project and one of that Crew's triggers. The trigger owns its
saved base instruction and conversation destination. A platform-internal
trigger can be created while the step is configured and is scoped to the
calling workflow. The step supplies the workflow-specific instruction and
runtime input.

The Builder must understand that a Crew is a persistent project, not merely an
LLM call. Builder guidance and tools must cover Crew identity, memory, skills,
files, attached folders, database, dashboard, triggers, schedules, bots, and
conversation destinations.

## Plan representation

An illustrative `plan.json` entry is:

```json
{
  "type": "crew",
  "id": "review-with-rts",
  "title": "Review with RTS Crew",
  "description": "Ask the maintained RTS reviewer to review this pull request.",
  "crew_profile_id": "work",
  "crew_project_id": "rts-pr-reviewer",
  "trigger_id": "5f16e1fa-7ddd-4ee5-8311-63b34745ad46",
  "instruction": "Read your memory and review skills. Review the pull request supplied in the workflow input. Update your dashboard with the outcome. Return a concise result; if the supporting report is large, save it under reports/ and include its Crew-relative path in the response.",
  "context_dependencies": [
    "prepare-review/pr.json"
  ],
  "context_output": "response.md",
  "timeout_seconds": 1800,
  "next_step_id": "publish-review"
}
```

The exact schema should use stable project and trigger IDs. Names, icons, and
paths are display metadata and must not be execution identities.

### Required fields

| Field | Purpose |
| --- | --- |
| `id` | Stable workflow step identity. |
| `title` | Human-readable node title. |
| `crew_profile_id` | Product profile; initially `work`. |
| `crew_project_id` | Stable target Crew project ID. |
| `trigger_id` | Existing trigger to invoke. |
| `instruction` | Workflow-specific instruction rendered at run time. |

### Optional fields

| Field | Purpose |
| --- | --- |
| `context_dependencies` | Workflow outputs made available as trigger input. |
| `context_output` | Response filename; defaults to `response.md`. |
| `timeout_seconds` | Maximum wait for the Crew run. |
| `next_step_id` | Explicit successor using normal workflow navigation. |

## Conversation destinations

Workflow steps, Crew calls and Code calls use the same project owner rule:

- **Any shared owner:** use the receiving project's main chat.
- **No shared owner:** use a separate continuing chat.
- **Unknown ownership:** use a separate chat.

Ownership comes from the source workflow's `access.owners` (or `created_by`
for older manifests) and the receiving Crew/Code project's owner record. A
workflow with several owners only needs one owner in common with the target.
The logged-in or executing person does not replace these project owners.
Existing access checks and guest permissions remain in effect.

On single-user installs only, a legacy workflow with no `access` block or
`created_by` stamp uses the configured local account (`DEFAULT_USER_ID`, or
`default`) as its owner for routing. SSO servers keep unknown owners isolated.

This rule overrides the stored `run_destination` of internal project bindings.
External webhooks and schedules still choose `crew_chat` or `isolated` in their
settings. An isolated chat retains earlier deliveries through the same trigger;
it does **not** mean a fresh chat per invocation. Its workspace stays the target
project's workspace, while its conversation history remains separate.

Cross-owner workflow calls use `<crew-project-id>:trigger:<trigger-id>` as their
conversation key, with a calling-person suffix for guest calls. Same-owner calls
use the receiving project's normal conversation key. Both destinations retain
context and return the exact run's final response to the workflow step's
`context_output`. The delivery idempotency key includes the effective destination
so a changed owner relationship cannot adopt a result from the old destination.
Queued calls also reject an ownership change before entering the conversation.

The local Trading → News Monitor runs observed on September 28 and October 5,
2026 used a separate trigger chat under the previous workflow-only rule. New
calls use the common owner rule above; existing saved history is preserved.

## Runtime contract

Executing a Crew step consists of four core operations:

1. Invoke the selected Crew trigger with the rendered instruction, declared
   workflow inputs, and a stable idempotency key.
2. Poll or await the trigger run until it succeeds, fails, is stopped, or
   reaches the configured timeout.
3. Read the exact final assistant response recorded for that trigger run.
4. Save the text response in the calling workflow's current iteration folder.

For example, with variable groups enabled:

```text
Workflow/example/runs/iteration-0/production/execution/review-with-rts/response.md
```

Active runs execute in `iteration-0` (older numbered folders are archives);
without groups the step folder sits directly under `runs/iteration-0/execution/`.

The execution record should also retain operational metadata—Crew project ID,
trigger ID, Crew run ID, session ID, status, timestamps, and error details—in
the workflow's existing progress/run metadata. The Crew does not have to emit
JSON for this metadata.

Token spend is attributed to both sides: the Crew run records its caller
(workflow ID, run ID, step ID) so Crew-side cost history shows what each
caller spent, and the workflow run's cost breakdown includes the crew step's
tokens so the run shows its true cost. It is one spend visible in two places,
each side labeled with the other — never silently absorbed by either side.

### Trigger request

The delivery payload should include at least:

```json
{
  "source": "workflow_step",
  "workflow_id": "release-pipeline",
  "workflow_run_id": "9f3c2e1a-7b4d-4c8e-a1f2-3d5b7c9e1a4f",
  "workflow_step_id": "review-with-rts",
  "group": "production",
  "instruction": "Rendered workflow-specific instruction",
  "inputs": {
    "pr": {
      "number": 87,
      "repository": "course_designer"
    }
  }
}
```

`group` is the calling workflow's variable group, for correlation only.
`workflow_run_id` is the calling execution's immutable execution identity, not
its run folder: run folders are reused between executions, so only the
execution identity keeps one execution from adopting another's Crew delivery.
Payload data cannot override Crew permissions, tools, trigger ownership,
conversation destination, or folder guards.

### Internal and external dispatch

An external caller invokes a public Crew trigger through its authenticated HTTP
endpoint. A workflow running inside the same deployment should use a
platform-internal trigger and reuse the same trigger dispatcher directly,
rather than sending an HTTP request to itself or placing trigger secrets in
`plan.json`.

Internal dispatch must produce the same trigger delivery, conversation,
history, run status, final response, and idempotency behavior as the hardened
public endpoint (see Crew trigger hardening).

### Platform-internal triggers

A platform-internal trigger is a first-class trigger binding for communication
between a Workflow and a Crew on the same deployment. It has no public URL and
issues no bearer or GitHub secret. Its authority comes from an explicit caller
binding checked by the platform on every invocation.

A binding is not a separate object: it is `kind: "internal"` plus a caller on
an ordinary trigger, stored in the same trigger list as public triggers. The
target is implicit — whichever manifest or schedule list holds the trigger —
so deleting or disabling the trigger automatically retires the binding and
orphaned permissions are impossible.

An illustrative Crew-side binding is:

```json
{
  "kind": "internal",
  "id": "5f16e1fa-7ddd-4ee5-8311-63b34745ad46",
  "name": "Release workflow reviewer",
  "enabled": true,
  "message": "Review the pull request supplied in the delivery payload.",
  "caller": {
    "type": "workflow",
    "id": "release-pipeline"
  },
  "run_destination": "isolated"
}
```

The reverse binding is a workflow schedule entry with `kind: "internal"` and
`caller.type="crew"`. Bindings are directional and grant invocation only. They
do not grant write access to the target's files or management access to its
configuration.

Both sides manage bindings through their existing trigger tools, extended
with `kind: "internal"` (no secret issued, no public path, caller required):

- Workflow Builder creates an internal Crew trigger while adding a Crew step.
- Crew creates an internal workflow trigger when asked to invoke an attached
  workflow.
- Either side can list bindings; owners can disable, re-enable, or remove them.
  Existing trigger UIs show internal triggers with an internal badge and no
  URL.

Normally, creating a binding requires trigger-edit rights on the target side
plus read access on the caller side. Crew-to-Workflow has one narrow exception:
an authorized read-only workflow attachment may create the secretless internal
binding scoped to that exact Crew when it first invokes the workflow. It cannot
create public triggers or manage unrelated triggers. Invocation rechecks both
resources, their attachment relationship, and the binding's enabled state. The
public HTTP trigger endpoints skip internal triggers entirely: they are
invokable only through internal dispatch. Copying either resource never
silently copies cross-project authority.

## Files and attached folders

Crew has one persistent project workspace. It does not have workflow iteration
folders, and this feature must not add them.

Before a Crew step can run, the target Crew project must be attached to the
workflow using the existing attached-folder mechanism. The attachment is
read-only. The attachment is the stable namespace through which downstream
steps read Crew files; it is not itself the permission. Folder grants store a
fixed path and never recheck project access, so the workflow must verify Crew
access before the run starts and again before each crew step executes, using
the same permission check as trigger invocation. If access is gone — revoked,
unshared, moved, or deleted — the run fails fast with an actionable error
before any step reads through the stale grant.

If the Crew returns:

```text
The detailed review is in reports/pr-87.md.
```

and the attachment alias is `rts-reviewer`, a downstream step can read:

```text
rts-reviewer/reports/pr-87.md
```

The workflow can read the file but cannot modify it. The file remains owned by
the Crew and may change later because Crew storage is persistent and mutable.

The first version does not infer an `output_files` list from natural-language
responses and does not automatically copy Crew files into the workflow run.
Small results may exist entirely in `response.md`. For large results, the
Builder can instruct the Crew to save files at a known Crew-relative location
and include those paths in its response.

If immutable file snapshots become necessary later, they should be an explicit
workflow feature with declared paths. They must not be introduced by parsing
arbitrary response prose.

## Crew invoking a workflow

The integration is bidirectional. A Crew can invoke an attached AgentWorks
workflow through a selected workflow trigger. This is useful when a persistent,
human-maintained specialist needs a deterministic pipeline for testing,
rendering, deployment, extraction, or another bounded operation.

The runtime flow is:

1. The Crew selects an attached workflow and an enabled internal trigger.
2. It creates or uses a platform-internal trigger binding scoped to that Crew.
3. It sends a JSON payload with a stable idempotency key.
4. The workflow executes in its own `iteration-<n>-hook` folder.
5. The Crew polls or awaits the trigger run until it becomes terminal.
6. The Crew reads the returned status, inline step outputs, progress, and
   artifact references.
7. The Crew can summarize the result, update its persistent files or database,
   and update its dashboard.

The workflow trigger already defines its route selections, optional single-step
target, variable groups, input mode, and execution contract. The Crew payload
cannot override configuration that the trigger did not explicitly expose.
The read-only attachment is sufficient to discover triggers and to create or
reuse one secretless internal binding for the exact Crew, matching the inverse
Workflow-to-Crew mechanism. Public trigger secrets remain protected, public
triggers cannot be invoked through this path, and the attachment grants no
general workflow editing or trigger-management access.

An illustrative invocation is:

```json
{
  "workflow_id": "course-designer-tests",
  "trigger_id": "smoke-test-trigger",
  "payload": {
    "pull_request": 87,
    "environment": "dev"
  },
  "wait": true,
  "timeout_seconds": 1800
}
```

The completed result can contain the workflow's normal structured outputs:

```json
{
  "status": "completed",
  "run_id": "iteration-68-hook",
  "terminal": true,
  "steps": [
    {
      "step_id": "test",
      "outputs": {
        "result.json": {
          "passed": 42,
          "failed": 0
        }
      }
    }
  ],
  "artifacts": []
}
```

Small JSON and text outputs can be read from the polling response. Large files
remain workflow run artifacts and use the workflow trigger's existing signed
download contract. When the workflow is attached read-only to the Crew, the
Crew can also inspect the completed hook iteration through that attachment.

The conceptual distinction is:

- Workflow to Crew calls a persistent specialist and receives a natural text
  response.
- Crew to Workflow calls a deterministic pipeline and receives structured run
  status, step outputs, and artifacts.

## Builder behavior

The Builder should be able to:

1. List Crews accessible to the current workflow owner, including icon, name,
   identity, and stable ID.
2. Inspect a Crew's triggers, saved instruction summary, enabled state, and
   conversation destination.
3. Attach the selected Crew to the workflow as read-only after an explicit
   configuration action.
4. Create, add, update, and remove Crew steps.
5. Explain whether the selected trigger uses main Crew history or its own
   persistent trigger history.
6. Validate that the Crew attachment and trigger still exist before saving the
   plan.

The Builder tools are:

- `list_accessible_workflows` (existing discovery: workflows and Crew
  projects with identity)
- `manage_crew_trigger` (create/list/update/delete Crew triggers, extended
  with `kind: internal`; internal bindings default to the calling workflow)
- `manage_crew_attachment` (attach/list/detach Crews read-only)
- `add_step` / `update_step` / `delete_plan_steps` with `type: crew` (plan
  editing, with crew field schemas)

The Crew runtime has the corresponding workflow tools:

- `list_attached_workflows`
- `list_workflow_triggers`
- `manage_workflow_webhook` (the existing workflow trigger tool, extended
  with `kind: internal`)
- `run_workflow_trigger`
- `get_workflow_trigger_run`

The Builder should help write instructions that state:

- which Crew memory and skills to consult;
- what runtime input to process;
- whether to update Crew files, database, or dashboard;
- when a short text response is sufficient;
- where to save large outputs and how to reference them in the response.

## Canvas and configuration UI

The Crew node should show the Crew icon and name, the selected trigger, its
conversation destination, and the response output.

Example:

```text
🚀 RTS PR Reviewer
Trigger: Review pull request
Conversation: Trigger history
Output: response.md
```

The configuration view should contain:

- Crew selector with icon, name, identity, and ID;
- trigger selector;
- clear retained-context explanation;
- workflow-specific instruction editor;
- context dependency selection;
- response filename;
- timeout;
- next step;
- read-only attachment status and action.

The UI should not imply that Crew files are copied into the workflow run.

Run-time observability lives in the execution views, not the plan. The crew
step's run entry must link its recorded Crew run ID to the exact Crew turn —
transcript and timing one click away. The execution logs gain a crew category
with the polling timeline (queued, turn started, answer recorded) and the
final response, like other step types have.

## Access and security

- Never store a plaintext trigger secret in the workflow plan.
- Platform-internal triggers never issue a public endpoint or plaintext secret.
- Every internal invocation must match the binding's exact caller and target.
- Check Crew and trigger access when the step is configured and again when it
  executes.
- Require the target Crew to be attached read-only before execution.
- Re-verify Crew access before the run starts and before each crew step
  executes; a revoked, moved, or deleted Crew fails the run fast, before any
  step reads through its attachment.
- Keep the workflow's write paths separate from the Crew attachment.
- Reject deleted or disabled triggers with an actionable step error.
- A copied workflow must not inherit authority to call a Crew its new owner
  cannot access.
- Resolve all referenced Crew paths inside the attachment root; traversal and
  absolute-path escape are forbidden.

## Reliability

### Idempotency

Derive the trigger delivery identity from the workflow execution identity,
group, step ID, and step attempt. Retrying the same attempt must recover the
existing Crew delivery instead of creating duplicate work. A deliberate new
step attempt receives a new delivery identity.

### Concurrency

A persistent Crew conversation must process one turn at a time. When the
selected conversation is occupied, the delivery is accepted and queued as the
next turn; the step shows a queued state until the turn starts. It must not
interleave two turns in the same conversation. Mid-turn steering is for
corrections to the current job, not for new deliveries: every delivery gets
its own turn and its own recorded final response.

### Cancellation

Out of scope for the first version. Stopping the workflow ends the step's
wait; the accepted Crew delivery runs to completion on its own. Crew-side
cancellation may be added later.

### Failure

The workflow step fails when the trigger is inaccessible, disabled, deleted,
or completes with an execution error, or when the delivery is still queued or
running beyond the timeout. The step error should include the Crew and trigger
names plus the recorded run ID when one exists.

## Trigger status API

The public Crew trigger currently accepts a delivery and records the final
response. A complete external call-and-poll contract should expose a status
resource equivalent to:

```http
POST /api/hooks/product/{trigger_id}
GET  /api/hooks/product/{trigger_id}/runs/{run_id}
```

```json
{
  "run_id": "...",
  "status": "completed",
  "terminal": true,
  "final_response": "Review complete. Details are in reports/pr-87.md.",
  "error": ""
}
```

The internal workflow dispatcher may await the same service directly, but the
observable run contract should remain consistent with the public API.

## Crew trigger hardening

Crew triggers were built as fire-and-forget deliveries into a chat, while
workflow triggers were built for programmatic callers (idempotent recovery,
pollable status). A workflow stepper is a programmatic caller: it must
distinguish *queued* from *running* from *lost*, and it must reattach after
its own retries. The gaps below must be closed in the shared trigger layer,
so both the public endpoint and internal dispatch offer the same
accept → poll → terminal contract. Restart survival is explicitly out of
scope: the system does not promise it anywhere else either.

### Idempotent redelivery

Delivery dedupe is currently a process-local map that returns no run status,
and run appends have no duplicate check, so a repeat can fork history under
one run ID. Dedupe must consult the run store (check-then-append under the
existing run-file lock), and a duplicate delivery must return the existing run
ID with its current status, as workflow triggers do. No restart promises: the
guarantee holds while the server is alive.

### Accept-and-queue instead of busy

When the trigger conversation is occupied, the run is currently rejected
before any run record is written, while the public endpoint has already
returned 202 — a poller waits on a run ID that will never exist. There must
be no busy rejection at all: the delivery is accepted, recorded with a
`queued` status, and executed as the next turn in that conversation. The
runtime's mid-turn steering is for corrections to the running job; queued
deliveries each get their own turn and their own recorded final response.

### Conversation-level mutual exclusion

The run lock is keyed per trigger, but every `crew_chat` trigger of a project
shares one main conversation with the other triggers and the user.
One-turn-at-a-time holds for `isolated` triggers only. Main-chat dispatch
needs a conversation-level lock so two turns can never interleave in the same
conversation.

### Status vocabulary

Crew runs record `running`, `success`, `error`, and `stopped` (plus `partial`
and `interrupted`); accept-and-queue adds a real `queued` state for accepted
deliveries still waiting their turn. The step's displayed states map onto
this vocabulary, with `timed-out` owned by the waiting stepper rather than
the Crew run.

## Implementation outline

Steps 1–10 are implemented. Stopping runs is out of scope: there is no
stop tool, and an accepted Crew delivery runs to completion on its own.

1. Harden the shared Crew trigger layer: idempotent redelivery that returns
   the existing run with its current status, accept-and-queue with a real
   `queued` run state, and conversation-level mutual exclusion for main-chat
   triggers.
2. Extend both sides' trigger models with `kind: internal` plus a caller,
   reusing existing trigger storage, ownership checks, and management tools;
   public HTTP trigger endpoints skip internal triggers.
3. Add the Crew trigger status response and durable final-response lookup.
4. Extract internal Crew-trigger and workflow-trigger dispatch interfaces from
   their HTTP handlers so callers can use them without secrets or loopback
   HTTP. Internal dispatch must surface disabled and deleted outcomes
   synchronously, with the same delivery, conversation, history, run status,
   final response, and idempotency behavior as the hardened public endpoint.
5. Add `CrewPlanStep` parsing, validation, marshaling, plan editing tools, and
   workflow execution dispatch.
6. Save the final text to the step's current iteration execution directory and
   connect it to normal context-output handling.
7. Add Builder discovery and mutation tools for Crews, triggers, and read-only
   attachment setup.
8. Add Crew tools to discover, create, invoke, and poll internal workflow
   trigger runs.
9. Add the Crew canvas node and configuration UI.
10. Update Builder and Crew reference material and product guidance so each
    side understands internal triggers, and so the Builder can choose between
    a Crew and an ordinary step and between the two retained conversation
    histories.

## Acceptance criteria

- A Builder can select an accessible Crew and one of its enabled triggers and
  add it as a workflow step.
- Workflow Builder can create and manage a Workflow-to-Crew internal trigger;
  no public URL or secret is created.
- Crew can create and manage a Crew-to-Workflow internal trigger for an
  attached workflow, invoke it, and poll it.
- The Builder establishes or verifies a read-only Crew attachment.
- A workflow run invokes the trigger exactly once for one step attempt and can
  recover safely after a retry.
- A duplicate Crew delivery returns the existing run ID and its current
  status; no second run is launched.
- An occupied Crew conversation accepts the delivery and queues it as the
  next turn; the step shows queued until the turn starts.
- Two turns never interleave in one Crew conversation, including the shared
  main chat.
- The step displays queued, running, completed, failed, and timed-out
  states, mapped onto the recorded Crew run statuses (a stopped run fails
  the step; its status stays visible in the execution logs).
- The Crew receives the rendered instruction and declared workflow input.
- Main-chat triggers retain the main Crew conversation; isolated triggers
  retain their own prior trigger-run conversation.
- The exact final Crew text is saved as `response.md` (or the configured
  `context_output`) inside the calling workflow iteration.
- Downstream steps can read Crew-relative files through the read-only
  attachment and cannot write to them.
- No trigger secret appears in `plan.json`, logs, frontend state, or step
  outputs.
- Internal trigger invocation is rejected when caller identity, target
  identity, attachment, permissions, or enabled state no longer matches.
- A workflow run whose Crew access was lost after configuration fails fast
  before any step executes; no step reads through the stale attachment.
- Crew-step token spend appears in both the workflow run's cost breakdown and
  the Crew project's cost history, each side labeled with the other.
- A crew step's run entry links to its exact Crew turn, and the execution logs
  include a crew category with the polling timeline and final response.
- Crew projects remain persistent projects and do not gain iteration folders.

## Implementation review — 2026-09-20

The implementation was reviewed against this design after steps 1–10 landed.
It is not ready to deploy until the blocking findings below are resolved.

### Blocking findings

1. **P0 — Current `main` does not compile.**
   `server.go` and `live_input_durable.go` reference `DurableAck`,
   `SupportsDurableAck`, and provider durable-await functions that are absent
   from the pinned and locally replaced `mcpagent` dependencies. This blocks
   the server test package and deployment. Either land and pin the dependency
   API first or remove the incomplete integration from this release.

2. **P1 — Crew-step idempotency collides across workflow executions.**
   The delivery key is currently built from workflow ID, selected run folder,
   and step ID. Normal executions reuse `iteration-0`, so a later execution can
   adopt the successful Crew delivery from an earlier execution and return its
   stale response without running the Crew. Include the workflow's immutable
   execution ID in the delivery identity, while keeping that identity stable
   across retries of the same step attempt.

3. **P1 — Terminal success is visible before the response and usage.**
   Crew-run persistence writes terminal `success` first, then writes the final
   response and token usage in separate operations. A poll between those
   writes can return a successful run with an empty response and no cost data.
   Persist the terminal status, final response, usage, completion time, and
   error as one atomic run-record update, or make terminal status the final
   write after every required result field is durable.

4. **P1 — A stale Crew attachment can remain readable after access is
   revoked.** Session folder grants are created directly from the attachment's
   stored workspace path. The run preflight validates only plans containing a
   Crew step, and access is not rechecked when an ordinary downstream step
   reads through the alias. Removing the Crew step while retaining the
   attachment, or revoking access during a run, can therefore leave the Crew
   workspace readable. Resolve and authorize attachment reads dynamically, or
   validate every attachment before granting its root and revoke the grant as
   soon as access changes.

5. **P2 — The shared Crew-trigger mutation path does not authorize the caller
   workflow.** The Builder tool performs this authorization before calling the
   shared save function, but the product-webhook REST endpoint reaches the same
   function directly. The shared mutation boundary checks only that the caller
   workflow ID exists. It must also require the requesting user to have read
   access to that workflow.

### Required regression coverage

- Run the same Crew step in two separate executions that both use
  `iteration-0`; assert that two Crew deliveries run and return their own
  responses.
- Retry one step attempt with the same immutable execution identity; assert
  that it adopts the original delivery instead of starting another.
- Poll continuously while a Crew run finishes; assert that no terminal
  response is observable without its final response and token usage.
- Revoke Crew access before a run, between a Crew step and a downstream read,
  and after removing the Crew step while retaining its attachment; every read
  must fail closed.
- Attempt to create an internal trigger bound to an inaccessible workflow
  through both Builder tools and the REST endpoint; both paths must reject it.

### Validation performed during review

- Crew workflow, orchestrator, attachment, and workflow-type Go tests passed.
- Frontend Crew tests passed: 11 tests across the Crew node, canvas
  presentation, and execution-log helpers.
- TypeScript compilation passed.
- The server test package could not build because of the P0 dependency/API
  mismatch above.

### Resolution — 2026-09-20

The original findings received the changes and regression tests listed below.
The follow-up review found that attachment validation and delivery isolation
still have uncovered cases, plus an additional failure-reporting bug; this
resolution section does not constitute current sign-off.

- **P0 — resolved.** The `main` compile breakage was fixed separately and
  the server package builds again.
- **P1 idempotency — resolved.** The delivery identity keys on the
  workflow's immutable execution ID (`ExecutionID`, falling back to the run
  folder only for bridgeless callers), stable across retries of one step
  attempt and unique across executions. Tests:
  `TestRunCrewStepIsolatesExecutionsSharingRunFolder` (two executions in
  `iteration-0` get separate deliveries) and
  `TestRunCrewStepRetryAdoptsSameExecutionDelivery` (a retry adopts the
  original delivery).
- **P1 atomic write — resolved.** Run completion persists through one
  `UpdateScheduleRunResult` read-modify-write holding the run-file lock, so
  terminal status, final response, usage, and completion time land together.
  Tests: `TestUpdateScheduleRunResultSingleWrite` (one store write) and
  `TestUpdateScheduleRunResultAtomicity` (concurrent pollers never observe
  a torn terminal record).
- **P1 stale attachment — resolved.** Attachment reads are validated
  dynamically at every boundary: the run preflight checks every attachment
  (binding shape plus live crew access) even when the plan holds no crew
  step; session grants carry only validated roots; detach/attach reconcile
  live sessions immediately; session refresh re-derives crew grants from
  the live manifest; and the orchestrator re-validates the matched
  attachment on every read. Tests: `TestPreflightCrewAttachmentsWithoutCrewSteps`,
  `TestDetachCrewAttachmentRevokesLiveSessionGrant`,
  `TestCrewAttachmentReadRootsSkipInvalidBindings`,
  `TestLiveCrewAttachmentGrantsSkipInvalidAttachments`,
  `TestRefreshWorkflowFolderAccessSessionReconcilesCrewGrants`,
  `TestCrewAttachmentReadFailsClosedAfterDetach`,
  `TestCrewAttachmentReadFailsClosedOnRetargetOrDelete`, and
  `TestValidateCrewAttachmentBinding`.
- **P2 REST auth — resolved.** The shared trigger-save boundary requires
  the requesting user to have read access to the named caller workflow, so
  the Builder tool and the REST endpoint enforce the same rule. Tests:
  `TestManageCrewTriggerRejectsInaccessibleCallerWorkflow` and
  `TestSaveProductWebhookRejectsInaccessibleCallerWorkflow`.

Known residual: a crew that is unshared mid-run while its workspace
directory still exists on disk remains filesystem-readable to ordinary
downstream steps until the run ends; crew steps themselves re-check access
before every invocation, and the next run's preflight fails fast.

## Follow-up code review — 2026-09-20

Reviewed against `mcp-agent-builder-go` `f6ee03b72`, including the earlier
Crew review fixes in `45f9e72d0`.

The implementation is connected end-to-end: `CrewPlanStep` is parsed and
validated, the controller renders instructions and inputs, a server-owned
runner invokes the shared internal trigger dispatcher and polls its run, and
the controller saves the final response, operational metadata, and token
usage. Reusing the trigger dispatcher and atomic completion record is sound.
However, the three reproduced findings below prevent sign-off. They remain
open; this documentation update does not fix production code. Line references
refer to the reviewed revision.

### P1: attachment validation can grant another owner's Crew directory

Sources:

- [crew_step_runner.go](../../agent_go/cmd/server/crew_step_runner.go), lines
  245–252: attachment preflight.
- [crew_attachments.go](../../agent_go/pkg/workflowtypes/crew_attachments.go),
  lines 77–91: stored binding validation.
- [crew_builder_tools.go](../../agent_go/cmd/server/crew_builder_tools.go):
  `crewAttachmentReadRoots` and `liveCrewAttachmentBindings`.

Binding validation checks that the stored path contains a `projects` segment
and ends with the declared project ID. Preflight separately resolves and
authorizes that project, but discards its resolved workspace binding instead
of comparing it with the stored root. A path belonging to another owner can
therefore pass validation when its final project-name segment matches.

Reproduction: start with an accessible `rts` Crew belonging to `owner`, then
change only the saved attachment root from
`_users/owner/Chats/Work/projects/rts` to
`_users/other/Chats/Work/projects/rts`. Preflight succeeds and
`crewAttachmentReadRoots` returns the other owner's path as a read grant.
`TestReviewCrewPreflightRejectsWrongOwnerRoot` reproduced both failures.
This violates the intended separation between a stored attachment path and
actual authorization to read that workspace.

Required fix: derive the granted root from the freshly authorized project
binding, or require exact canonical equality with that binding before granting
or resolving the stored path. Apply this at grant creation and refresh/read
boundaries. Retain a regression covering two owners with the same project-name
suffix; checking only a different suffix or a non-project path is insufficient.

### P1: variable groups share the same Crew delivery identity

Sources:

- [crew_step_runner.go](../../agent_go/cmd/server/crew_step_runner.go), line 74
  and `crewStepDeliveryBase`.
- [controller_crew.go](../../agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/controller_crew.go):
  request construction passes both execution identity and group.
- [context_aware_bridge.go](../../agent_go/pkg/orchestrator/context_aware_bridge.go):
  `ExecutionID` remains fixed while `SetBatchContext` changes groups.

The delivery key contains workflow ID, immutable execution ID, and step ID,
but omits `Group`. Multiple variable groups within one execution consequently
share a delivery identity for the same Crew step. Once the first group
completes, a later group adopts its response instead of invoking the Crew with
its own rendered instruction and inputs.

Reproduction: seed a successful production-group delivery, then invoke the same
step and execution with `Group=staging`, a different run folder, and a different
instruction. The runner returns the production delivery ID and response.
`TestReviewCrewGroupsDoNotShareDelivery` reproduced the failure. The earlier
cross-execution regression varies execution IDs and does not cover this case.

Required fix: include the variable group in the stable delivery identity while
preserving idempotency for retries of the same group and step attempt. Add a
test in which two groups share one execution ID and must receive independent
Crew deliveries and responses.

### P2: conversation setup errors leave accepted deliveries queued

Source: [product_schedules.go](../../agent_go/cmd/server/product_schedules.go),
`executeAutomationRun`, lines 988–993; acceptance occurs in
[product_webhooks.go](../../agent_go/cmd/server/product_webhooks.go),
`deliverProductTrigger`.

The dispatcher persists a queued run before starting the detached worker.
If the worker cannot resolve its conversation binding or open the conversation
registry, it returns before writing a terminal result. Its defer releases the
conversation reservation, but the accepted run remains `queued` with no worker.
The workflow therefore waits until timeout instead of receiving the actual
failure, and redelivery adopts the same stranded record.

Reproduction: pre-claim a queued run, force a conversation-policy setup error,
run `executeAutomationRun`, and read the stored status after it returns the
error. The record remains `queued`. `TestReviewCrewSetupFailureBecomesTerminal`
reproduced this early-exit behavior. Other failures at the same boundary, such
as failed conversation-store access, take the same return path.

Required fix: persist a terminal error against the original claimed run on
every setup failure, even before a conversation/session ID is available. Add
coverage proving that setup errors become pollable terminal failures and that
retry recovery does not adopt a permanently queued record.

### Follow-up validation

- Focused server tests covering Crew runners, preflight, Builder tools,
  attachments, internal dispatch, and atomic run results passed.
- Crew-focused tests passed in `pkg/workflowtypes`, `pkg/orchestrator`, and
  `pkg/orchestrator/agents/workflow/step_based_workflow`.
- Eight frontend tests passed across `CrewNode.test.tsx` and execution-log
  `helpers.test.tsx`.
- Three temporary regression tests failed on the intended invariants,
  confirming the findings above. They were removed after reproduction and are
  not committed regression coverage.
- No production code was changed. Live Crew execution, the full repository
  suites, and TypeScript compilation were not exercised in this follow-up.

Current disposition: **changes required**. Fix these findings and retain their
regression coverage before sign-off. The previously documented mid-run access
revocation limitation also remains open; the existing passing tests do not
establish that every attachment access is dynamically authorized.

### Follow-up resolution — 2026-09-20

All three findings were confirmed against the code and are fixed below, each
with permanent regression coverage.

- **P1 wrong-owner grant — fixed.** The run preflight now compares the stored
  attachment root against the freshly authorized project binding and fails
  fast on mismatch; session grants derive each root from that binding and
  grant nothing on mismatch, revoked access, or missing user. Tests:
  `TestPreflightCrewAttachmentsWithoutCrewSteps` (wrong-owner case),
  `TestCrewAttachmentReadRootsSkipInvalidBindings` (wrong-owner omission),
  and `TestCanonicalCrewAttachmentRoot`.
- **P1 shared group identity — fixed.** The delivery identity is now
  workflow + execution + group + step, matching the design's idempotency
  contract; retries of the same group and step attempt stay idempotent.
  Test: `TestRunCrewStepGroupsDoNotShareDelivery`.
- **P2 stranded queued run — fixed.** `executeAutomationRun` records a
  terminal error against the pre-claimed run on every conversation setup
  failure (recreating the record if retention trimmed the pre-claim), so
  pollers receive the failure and redelivery adopts a terminal record
  instead of a stranded queue entry. Cron runs claim nothing up front and
  keep their existing behavior. Test:
  `TestExecuteAutomationRunSetupFailureBecomesTerminal`.

Residual: cross-owner equality is enforced server-side, where the
user-scoped binding can be resolved. The orchestrator's local read path
still validates shape and existence only; a mid-run hand-edit of the
manifest to another owner's path is not re-authorized there.

## Builder-created Crews

**Implemented 2026-09-20.** Workflow Builder creates a Crew while the user is
building a workflow through the `create_crew` tool (`CreateCrewProject`
service). This removes the multi-screen setup in which the user must create a
Crew separately, return to the workflow, attach it, create a trigger, and
select several IDs.

The simple user flow is:

1. Builder recognizes that a step would benefit from a dedicated specialist
   and asks, for example, "Create a Release Reviewer Crew with GitHub access?"
2. The user approves the Crew and the requested connections.
3. Builder creates a useful starter Crew, connects it to the workflow, and adds
   the configured Crew step.
4. The user can immediately test the Crew manually or run the workflow, then
   improve the Crew's instructions, skills, memory, and integrations over time.

This makes a Crew a more capable, user-testable specialist than an ordinary
message sequence or execution-only subagent. It has a dedicated workspace,
memory, skills, integrations, and conversation that the user can open and
refine independently of the workflow.

### Creation contract

One Builder action should create the usable integration, including:

- the Crew name, purpose, starter instructions, and selected skills;
- the MCP connections the Crew uses by default;
- references to the authorized secrets required by those connections;
- one enabled platform-internal trigger restricted to the creating workflow;
- a read-only Crew attachment on the workflow;
- the creating workflow in the Crew's `workflow_context_paths`, so the new
  Crew automatically gets read-only context on the workflow it serves; and
- a Crew step already configured with the new Crew, trigger, and attachment
  (returned as an exact `add_step` configuration; the Builder issues the call
  through the gated plan path).

Secrets are passed only as references to existing authorized secret or
connection records. Raw secret values must not be written into the workflow
plan, Crew files, tool response, or Builder conversation. Builder should show
the requested integrations in one approval step and ask the user to connect
only anything that is missing.

The default trigger stores `isolated` as a fallback; the common project owner
rule selects the actual conversation at dispatch. It is internal, accepts the step
instruction and workflow inputs, and is automatically selected by the new Crew
step. The creation result returns the Crew ID, trigger ID, attachment alias, and
step configuration so Builder can finish without asking the user to copy IDs.

The two links are symmetric and both read-only: the workflow reads the Crew
through its attachment, and the Crew reads the workflow through
`workflow_context_paths` (always mounted read-only and authorized through
the same boundary as transient references). Neither side can write to the
other. The user can remove the workflow from the Crew's context paths later
through the existing picker; the trigger binding and attachment are unaffected.

Creation should initially happen only while building the workflow and after the
user approves it. Automatically creating permanent Crews during workflow
execution is deferred; retries, loops, and parallel groups could otherwise fill
the user's Crew list with resources they did not explicitly choose to keep.

### Current state

Implemented. `create_crew` mints the Crew (UI-identical manifests, starter
`MEMORY.md` brief, skills/servers/secret selections, default LLM config),
creates the enabled internal trigger bound to the workflow, attaches the Crew
read-only, seeds the creating workflow into the Crew's context paths, and
returns the exact step configuration; the Builder then issues `add_step`
through the normal gated path. Retries under the same idempotency key adopt
every record instead of duplicating. Only the `work` profile is supported.

One deliberate deviation from the plan below: the service does not write the
plan step itself. Direct executor invocation would bypass the schedule
collision guard and phase policies that wrap `add_step`; returning the step
configuration keeps plan mutations on the gated tool path. The user still
approves once and copies no IDs.

If a later use case requires fresh runtime instances or parallel Crew fan-out,
add that as a separate lifecycle feature now that Builder-created Crews exist.

### Implementation plan

**Goal.** Let Workflow Builder create a new Crew while the user builds a
workflow: one user approval produces a usable, testable specialist — Crew
project, skills/servers/secret references, an enabled internal trigger
restricted to the workflow, a read-only attachment, and a configured crew
step.

**Success criteria.** From Builder chat, a user goes from proposal to an
approved, working crew step without copying IDs or leaving the workflow. The
created Crew is immediately openable and testable in normal Crew chat, and a
workflow run using it passes preflight and executes. Secrets travel as
references only. Retrying the creation never mints duplicates. Missing OAuth
connections surface as a pending list.

**Current facts.** Creation today is client-side only
(`createProductProject` in `frontend/src/platform/chat/productProjects.ts`
plus `createWorkSession`): it writes `workflow.json`, `product.json`, and a
`code/` folder through generic planner-file APIs, with no server validation,
idempotency, or wiring. Reusable server pieces: `manage_crew_trigger`
(internal + caller binding), `manage_crew_attachment`, `add_step(type="crew")`,
`list_accessible_workflows`, `list_secrets` / `manage_global_secret`,
`list_mcp_servers`, `list_skills` / `search_skills`, and the
`updateProductSelected*` writers. Builder approval is conversational
(blocking `human_feedback` is forbidden in Builder mode).

**Constraints and non-goals.** Runtime creation stays out of scope. No new
frontend in v1 (chat-only flow). No deletion cascade: a created Crew survives
workflow deletion. Non-goals: ephemeral crews, run-scoped conversations,
migration of existing Crews, frontend creation dialog.

**Key decisions.**

- One server action, not composed tool calls: a single `create_crew` Builder
  tool backed by one service method (create, selections, trigger, attachment,
  step). Only server coordination gives atomicity and idempotency.
- Conversational approval plus idempotency key: Builder proposes in chat,
  calls the tool after the user's approving reply with a client-generated key.
- Idempotency via durable receipt: the key claims a receipt freezing crew
  ID, path, alias, and step ID (supersedes the earlier deterministic-path
  sketch, which could not survive a changed title); re-entry returns the
  existing record, a changed payload is a conflict. Duplicate display names
  stay allowed.
- Secrets as validated references only: the schema accepts names, the service
  verifies existence and authorization, and no value-bearing fields exist.
- Selections land through the existing `updateProductSelected*` writers.
- Trigger defaults: internal, `isolated`, enabled, caller-bound to the
  creating workflow; alias defaults to the slugified Crew name.

**Work plan (strictly ordered).**

1. Creation core (backend): user-scoped projects root, slug+id path,
   `product.json`, `workflow.json` runtime manifest, `code/` folder, session
   id; input validation; idempotent re-entry. Mirror the UI file layout
   byte-for-byte, with the creating workflow pre-seeded in the Crew's
   `workflow_context_paths`.
2. Starter content plus selections: seed starter instructions/purpose; apply
   skill/server/secret selections; reject unknown secret names; resolve
   default LLM config server-side (fallback: omit, profile default applies).
3. One-shot wiring: internal trigger and read-only attachment via the
   internal implementations behind the existing tools; return all IDs plus the
   exact step configuration (the Builder writes the step via `add_step`, so
   plan policies apply — see Current state).
4. `create_crew` Builder tool plus guidance: schema per the creation contract
   plus `idempotency_key`; Builder-mode gating; proposal format and
   pending-connection reporting; the sequence < orchestrator < crew proposal
   ladder.
5. Docs: flip this section to implemented; record the orphan rule and
   pending-connections behavior.

**Validation.** Creation/wiring/preflight Go suites green; `gofmt` and server
build clean; manual Builder-chat E2E (propose, approve, verify Crew, test in
chat, run workflow, retry approval with zero duplicates, missing-OAuth
pending list).

**Risks.** Partial failure across six writes (mitigate with ordered writes
plus idempotent re-entry; rollback is deregistering the tool — created crews
are ordinary crews). Over-eager proposals (guidance ladder). Secret-name
confusion (fail closed).

## Builder-created Crew implementation review — 2026-09-20

Reviewed AgentWorks commit `85514dc15`. Status: **changes required before the
Builder-created Crew flow is ready for users**. The product direction is good:
one approval should create the specialist, internal trigger, read-only link,
and ready-to-add workflow step without making the user copy IDs. The happy-path
production package builds, but the current implementation does not yet satisfy
that simple flow reliably.

### P1: the committed Crew tests cannot run

`crew_creation_test.go` calls `mock.hasFolder`, but `mockWorkspaceAPI` has no
such method, so `go test ./cmd/server` fails to compile. Supplying that helper
exposes a second test-fixture gap: `workspace_mock_test.go` does not implement
`POST /api/folders`, so every successful creation case fails while creating the
Crew's `code/` folder. Fix both mock behaviors before treating the creation,
wiring, or idempotency tests as evidence.

### P1: MCP and secret selections are recorded without availability checks

`CreateCrewProject` validates installed skills, but MCP server, project secret,
and global secret inputs receive shape validation only. Unknown, disconnected,
or unauthorized names are therefore written into the Crew manifest. This
conflicts with the creation contract above, which requires references to
existing authorized records and a pending list for missing connections.

This is also a scope problem for ordinary secrets: a workflow-scoped secret in
the creating workflow does not automatically exist in the new Crew workspace.
The creation result may look complete while the Crew receives an empty value at
runtime. Validate each MCP and secret reference in its real scope before
writing selections. Return missing or disconnected integrations explicitly so
the Builder can ask the user to connect only those items. Do not pass secret
values through Builder chat.

### P1: changing the title on a retry breaks idempotency

The Crew ID is derived only from `idempotency_key`, while the workspace path is
derived from both the title and that key. Reusing a key with a changed title can
therefore create a second directory carrying the same Crew ID. Attachment
wiring then fails and leaves the second project stranded. Resolve an
idempotency key to one stored creation receipt or one path independent of
mutable proposal fields; reject a changed payload or return the original Crew.

The occupied-path fallback has the same principle problem because it chooses a
new random ID and path on every retry. Persist that decision so re-entry adopts
the same Crew.

### P2: duplicate Crew titles collide after partial creation

Duplicate display names are allowed, but the default attachment alias and step
ID are direct title slugs. Creating a second Crew with the same title writes its
project and internal trigger, then fails because the first Crew already owns
the alias; its default step ID would collide as well. Select available,
deterministic suffixes such as `release-reviewer-2` and
`crew-release-reviewer-2` before creating any resources, and return those exact
values to the Builder.

### Verification performed

- `go build ./cmd/server` passed.
- `gofmt -d` and `git diff --check` passed for the reviewed change.
- Crew-related `pkg/workflowtypes`, `pkg/orchestrator`, and
  `pkg/orchestrator/agents/workflow/step_based_workflow` tests passed.
- The new `cmd/server` Crew tests failed as described above, first at compile
  time and then at the missing folder-create mock after temporarily supplying
  the absent helper.

Recommended acceptance is intentionally small and user-focused: the server
tests run; unavailable MCPs and secrets are reported clearly; the same retry
key cannot mint another Crew; and two Crews with the same display name both
finish with usable aliases and step IDs.

## Builder-created Crew review fixes — 2026-09-20

All four findings above are fixed; the acceptance checklist now holds:

- The folder-create mock and `hasFolder` helper ship with the change, so the
  committed `cmd/server` Crew tests compile and run.
- MCP servers resolve against the platform catalog (unknown names fail;
  configured-but-disconnected servers are selected and returned as pending),
  project secrets must resolve to stored user secrets or globals (workflow-
  scoped names fail with a carry-over hint), and unknown globals fail. All
  availability checks run before any crew resource exists.
- The idempotency key claims a durable receipt freezing crew ID, path, alias,
  and step ID. A changed proposal under a claimed key is a conflict (new key
  required); the occupied-path fallback persists in the receipt, so re-entry
  adopts the same crew.
- Attachment alias and step ID are resolved to free deterministic values
  (`slug-2`, `crew-slug-2`) before anything is created, with the default step
  paired to the selected alias; explicit collisions fail before writing.

## Builder session folder-guard fix — 2026-09-20

First production run failed deterministically: `create_crew` wrote the crew
manifests, then `code/` folder creation was denied with `ACCESS DENIED`
because it went through the guarded workspace client, which enforces the
Builder session's sandbox (`Workflow/<folder>` only). The fix uses the raw
`createWorkspaceFolder` helper instead — the session guard confines the
model's direct file access, not the server's own authorized tool writes, and
every other creation write already used raw helpers. A regression test runs
creation under a Builder-style guard. Retrying with the same idempotency key
adopts the receipt and converges the half-written crew instead of duplicating
it.

## Model inheritance + runner binding — 2026-09-20

Two production follow-ups. New crews inherit the creating workflow's model
(provider profiles resolve through the same defaults the engine uses;
explicit builder choices carry over verbatim), falling back to the profile
default only when the workflow names nothing usable. And the server-owned
crew runner now binds on every authenticated run path — UI runs, Builder
chat runs, live input, schedules, webhooks — instead of only runs carrying
execution options, so crew steps test identically from Builder; the old
error text wrongly claimed scheduler-only execution.

Workshop follow-up the same day: Builder's own execute_step and
run_full_workflow build their own execution options and never saw that
binding, so live logs showed the same "does not bind a Crew runner"
failure. The server now injects the runner into WorkshopConfig;
session init carries it onto the controller (execute_step reads the
controller's options untouched) and run_full_workflow copies it onto its
fresh controller. Binding also falls back to the single-user default
identity like every other per-user lookup, instead of requiring JWT
claims.

Caller-stamp follow-up: triggers are bound to the workflow manifest ID
at creation, but dispatch presented the controller's random
per-construction workflow ID, so every invoke failed with caller
mismatch. Crew steps now stamp the manifest ID read from workflow.json
(same ReadWorkspaceFile pattern as the code-layout loader); the random
ID stays for human-feedback correlation and as the no-manifest
fallback. Side benefit: the delivery idempotency key is now stable
across sessions, so retried runs adopt the live crew run instead of
invoking twice.

Completion-capture follow-up: the crew ran 4m14s and wrote its alert
file, but the run recorded success with a null final response, so the
workflow step crashed saving the empty file. Turn completion already
funnels through one reader (finalResponseForExecution, serving
schedules, triggers, and bots). Live event-store dump showed the real
defect: the reader matched only the query execution ID, but main-agent
turns scope their events to main:<session> — the unified_completion
with the full summary sat in the store unmatched. The reader now
matches both scopes, newest first, and falls back to the newest
llm_generation_end content — the same fallback
scheduledTurnProducedResponse uses.

Delivery-key follow-up: the idempotency key covered workflow/run/
group/step only, so retesting a step after switching its trigger to
crew_chat silently adopted the old isolated run (same-second
"success", nothing in chat). The trigger ID and normalized
destination now join the key: identical retries still adopt,
retargets always fire.
