[← platform / browser-automation](index.md)

# PLAT-322 — One persistent managed browser per workflow

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | platform |
| Area | browser-automation |
| Summary | is the canonical browser ownership contract: one persistent managed-headless browser per workflow, shared by all authorized users, Builder sessions, runs, schedules, triggers, steps and delegated agents, with separate p… |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `Crew live-discovery follow-up fixed locally; deployment/live verification pending` |
| Last synchronized | `2026-09-22` |

- **Priority:** P1 — browser login state is workflow data and must neither split
  across the workflow's users/runs nor leak into another workflow.
- **Canonical owner:** managed-browser identity, profile persistence, workflow
  inheritance, live discovery, recording and deployment-mode boundaries.
- **Implementation:** `a707df258` (`Scope managed browser profiles by workflow`).
- **Consolidates:** the former delegation/browser-inheritance, CDP tab-owner,
  and server-CDP tickets. Their decisions, evidence and remaining work are
  retained below; the original ticket files are removed.

## Problem

Managed headless browser ownership was described and implemented inconsistently.
Some paths treated it as one browser per signed-in user, workflow children relied
on inherited browser bindings, and CDP used a different shared-Chrome model. That
made it unclear whether Builder, scheduled runs, triggers, steps, delegated agents
and another authorized workflow user should see the same cookies and tabs.

The required product boundary is the workflow itself. A workflow's saved browser
login is workflow-specific state, not personal user state and not per-run state.

## Canonical contract

1. Managed headless mode has exactly one durable browser identity per workflow.
2. The identity is derived only from the normalized workflow path:
   `sha256("workflow" + NUL + normalizedWorkflowPath)`, represented by the first
   16 lowercase hexadecimal characters as `workflow-<hash>--browser`. User ID,
   chat ID, run ID, schedule ID, trigger ID, group and caller-supplied session
   labels are not inputs.
3. Builder conversations, manual runs, schedules, triggers, workflow steps,
   groups and delegated agents inherit that identity. All currently authorized
   workflow users intentionally share its cookies, tabs and authenticated sites.
4. Different workflows resolve to different browser identities and profile
   directories. Workflow completion and child cleanup do not erase the profile.
5. `AGENT_BROWSER_SHARED_PROFILE` is one persistent base path, not a unique value
   generated per workflow. Workflow profiles are stored at
   `<base>-workflows/<workflow-browser-identity>`.
6. The local launcher defaults the base to
   `$HOME/.agentworks/browser-profile`; an explicit environment or `.env` value
   overrides it. The resulting local workflow path is therefore
   `$HOME/.agentworks/browser-profile-workflows/workflow-<hash>--browser`.
7. Non-workflow authenticated chats retain the legacy per-user managed browser;
   anonymous chats retain a chat-scoped guest browser. These fallbacks do not
   alter the workflow contract.
8. Browser live-view discovery, input and recording remain gated by current
   workflow authorization. Sharing browser state does not bypass workflow access.
9. Each Crew project has one durable managed-headless browser scoped to both
   its signed-in owner and normalized Crew workspace. Different Crew projects
   do not share cookies or tabs, and Crew never adopts a Workflow browser merely
   because its shared project manifest is named `workflow.json`.

## Crew live-discovery follow-up — 2026-09-22

RTS production showed a Crew successfully executing `agent_browser` while the
manually opened Browser panel remained empty. The managed daemon, Chrome process,
`.stream` metadata and local HTTP stream were all healthy. The Crew executed 27
browser calls; the failure was exclusively in agent-server discovery.

`liveBrowserSessions` called `workflowAccessForWorkspacePath` before identifying
the workspace kind. Real Crew projects persist `workflow.json`, so the non-nil
manifest sent them through the real-Workflow branch. That branch accepts only the
workflow-shared session identity and requires Workflow `browser_mode` to be
`auto` or `headless`; Crew correctly uses a user-plus-workspace identity and its
manifest reported `browser_mode: none`. The valid Crew session was therefore
filtered out before the frontend could open its WebSocket stream. The existing
Crew discovery test returned HTTP 404 for `workflow.json`, modelling a fixed
workspace product rather than an actual Crew and hiding the regression.

The correction classifies `Chats/Work/projects/...` before Workflow manifest
handling, admits only the exact owner-scoped Crew browser identity with a tracked
active-session binding, applies the same distinction to Take control, and
canonicalizes public versus `_users/<owner>/...` paths during ownership checks.
The regression now uses a real manifest-backed Crew with `browser_mode: none`.

## Crew capture follow-up — 2026-09-22 (issue #210)

Confida QA showed the same bug class at the next checkpoint: bundled
`capture` denied every Crew session with `CAPTURE_ACCESS_DENIED`
because `handleCapture` required exactly `Workflow/<name>`. The durable
fix is a canonical classifier both sides share —
`pkg/common/session_workspace.go` (`CanonicalSessionWorkspace` +
`ClassifySessionWorkspace`) — instead of another hand-rolled prefix
check. Capture now binds the owning root for workflows
(`Workflow/<name>`) and Crews (`Chats/Work/projects/<id>`, physical
`_users/<owner>/...` canonicalized with the ctx user); anything else
still denies with the same error. A cmd/server equivalence test pins
the mirror to `canonicalChatHistoryWorkspacePath` and
`isActiveWorkProjectWorkspace`, and the capture regression uses a real
Crew-shaped session (physical per-user path, owning user in ctx) plus a
no-user denial case.

Scoped out with evidence: the artifact-destination denials are correct
enforcement — public `Chats/` holds no projects on disk (all Crew data
lives under `_users/<id>/...`), the session guard already carries the
physical project root, and the skill mandates workspace-relative
destinations, so bare `code/`-style paths are agent error. The
"Missing workspace or token" preview failure cannot come from the
minted URL (the tool always includes both); it needs the session
transcript to pin down. The upstream `browser_artifact.go` check is
fully generic and needs no change.

## CDP boundary

CDP is not the managed-headless persistence mechanism above. It attaches to an
already-running real Chrome and retains its shared-port/tab ownership model.

- Desktop/local installations may use CDP when the deployment enables it.
- Headless server deployments declare CDP unsupported and use managed headless
  mode.
- The unresolved historical tab-quota incident remains a CDP-specific
  diagnostic record. It does not redefine managed-headless ownership.

## Merged history: delegation and browser inheritance

The former delegation ticket found that `call_sub_agent` made parents repeat
contracts that predefined children already receive, replayed the combined
standing contract during stateful continuation, and used ambiguous `todo_id`
lookups even though each invocation already returned a unique `execution_id`.

Its retained decisions are:

- predefined routes receive their saved description, validation schema,
  declared dependencies, selected skills, managed stores and route learnings;
  callers pass only current-run facts absent from that standing contract;
- stateful continuation sends only the new dynamic instruction;
- `route_id` selects a configured specialist, `task_id` is a stable artifact
  label, and `execution_id` identifies one exact invocation for query, stop and
  conversation inspection;
- the obsolete `share_browser` argument is removed. Browser-capable children
  inherit this ticket's workflow browser identity, and callers serialize
  browser work when their actions would conflict.

These delegation decisions remain part of PLAT-322 because browser inheritance
depends on the same stable workflow identity, even though most of the original
defect concerned handoff size and execution diagnostics.

## Merged history: CDP tab ownership incident

Instagram's `route-generate-illustrations` was once rejected as already owning
four CDP tabs while the visible tabs belonged to Upwork, Apollo and WhatsApp.
The initial theory was that `cdpOwnerID` fell back to the shared connection name
`shared-cdp-<port>`, pooling unrelated workflows under one quota owner.

Code review confirmed that fallback was structurally possible, but a same-day
trace disproved it for the reported message-sequence route: its exact execution
session was bound before the browser call, so ownership resolved to the bound
workflow/group identity and never reached the shared-name fallback. The actual
mechanism behind that live incident remains unproven because the relevant logs
were lost across a server restart.

The retained defensive fixes are:

- `cdpOwnerID` never accepts the shared CDP connection identity or the old
  collision-prone `default` literal as a workflow owner;
- an unidentified owner is unique for diagnostics but is rejected by tab
  creation, because a fresh identity would otherwise always appear to own zero
  tabs and silently bypass `MaxCDPTabsPerOwner`;
- tests cover the stable bound-owner path, rejection of unidentified owners,
  non-collision fallbacks and preservation of non-CDP artifact ownership.

Still open: reproduce the original cross-workflow quota symptom with current
instrumentation and report which aliases are counted. CDP foreground races also
remain structurally possible when concurrent workflows attach to one real Chrome;
a labeled tab gives identity, not exclusive foreground rendering.

## Merged history: unsupported server CDP

The former server-CDP ticket began with a cross-Unix-user lock failure. CDP uses
an in-process mutex plus a `0600` file lock at
`$TMPDIR/mcp-agent-builder-cdp-<port>.lock`, keyed only by port. Separate product
accounts on one host could therefore see a reachable Chrome metadata endpoint
yet fail to open a lock inode created by another Unix user.

Investigation showed the deeper issue: the server had no operator-owned Chrome
listening as a durable CDP service. A transient debugging port belonged to an
internal headless launch, not a browser other sessions should attach to. Scoping
the lock filename would have legitimized an unsupported deployment mode.

The retained platform decision and implementation are:

- `AGENT_BROWSER_CDP_ENABLED=false` declares CDP unsupported on remote servers;
- `agent_browser status` returns `cdp_supported=false`, skips probing and makes
  workflow `auto` resolve to managed headless;
- explicit CDP requests and stale CDP configuration are rejected at execution,
  query, workflow-tool and manifest-update boundaries;
- Builder schemas, browser guidance and frontend settings expose the same
  capability, and server deployment paths set or validate it;
- desktop/local installations retain CDP support;
- the old cross-user lock implementation remains unchanged because no supported
  server CDP service exists. A future shared-browser service requires a fresh
  ownership and cross-product locking design before CDP is re-enabled.

## Verification

- [x] Namespace tests prove two users of one workflow share an identity.
- [x] Namespace tests prove different workflows do not share an identity.
- [x] Session labels, Builder/child sessions and delegated users converge on the
      workflow identity.
- [x] Persistent profile tests resolve workflow identities below
      `<base>-workflows/` and retain legacy user profiles below `<base>-users/`.
- [x] Live browser discovery returns only the authorized workflow browser.
- [x] A manifest-backed Crew exposes only its owner's dedicated Crew browser.
- [x] Take control uses Crew ownership rather than Workflow ACL classification.
- [x] Trigger/schedule preset fallback resolves the workflow path before binding.
- [x] Browser, common, orchestrator, workspace and focused server tests pass.
- [ ] Restart/deploy each environment with a persistent base and verify login
      survives a second run by another authorized user of the same workflow.
- [ ] Verify a second workflow on the same deployment cannot observe those
      cookies, tabs or authenticated sessions.

## Supersession rule

New findings about managed-headless ownership, persistence, cleanup, workflow
inheritance or cross-workflow cookie isolation link to PLAT-322 instead of opening
another platform ticket. CDP-only defects continue under their specific CDP
ticket when the mechanism is independent of this contract.

## Register notes

[PLAT-322](plat-322.md) is the canonical browser ownership
contract: one persistent managed-headless browser per workflow, shared by all
authorized users, Builder sessions, runs, schedules, triggers, steps and
delegated agents, with separate profile directories across workflows. Crew
projects separately receive one owner-and-workspace-scoped persistent browser.
The 2026-09-22 follow-up fixes live discovery misclassifying Crew's shared
`workflow.json` as a real Workflow manifest and filtering its healthy stream
from the Browser panel. It
also contains the merged delegation-inheritance, CDP owner/quota and server-CDP
incident history; the three superseded source tickets were removed. The
implementation is pushed as `a707df258`; deployment and live cross-user/
cross-workflow verification remain pending. The second 2026-09-22 follow-up
(issue #210) routes bundled capture through a shared canonical
session-workspace classifier so Crew sessions capture into their project
root instead of `CAPTURE_ACCESS_DENIED`; artifact-destination denials were
verified as correct enforcement, not a policy gap.
