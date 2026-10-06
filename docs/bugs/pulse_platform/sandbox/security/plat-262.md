[← platform / security-sandbox](index.md)

# PLAT-262 — Read-only workflow user: restore the read permission tier with real enforcement (not the removed dead gate)

| Field | Value |
|---|---|
| State | closed |
| Priority | - |
| Product | sandbox |
| Area | security |
| Summary | platform feature (design only at filing time, no code changed |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented, live-verified locally` — mechanism keyed on `WorkshopMode` (single source of truth for tools/prompt/skills), with `WorkflowAccessLevel` doing exactly one thing: pinning a read-only identity's session to `WorkshopMode="run"` server-side, permanently, with no client-side toggle. See RCA #1 (why access-level, not mode), RCA #2 (why that was corrected back toward mode), and the Live reverify section (two real folder-guard bugs found and fixed during local live testing, one pre-existing dead security check removed) |
| Last synchronized | `2026-08-31` |

- **Type:** platform feature (design only at filing time, no code changed
  yet). Filed at the user's explicit request immediately after the design
  converged, to record the full negotiated shape before implementation
  starts.
- **Origin:** the user asked directly whether AgentWorks has an admin-vs-
  read-only user model. It does not: authentication supports multiple users
  (`AUTH_USERS=user:pass,...`, or full multi-user mode via Cognito/Supabase),
  but there is no authorization boundary once someone is logged in — every
  authenticated identity can do everything today. The user wants a second,
  read-only user: same chat/UI experience, but everything is read-only.

## 2026-09-12 — Attached workflow chat context authorization

Commit `5db05c9af`, deployed in RTS release `bb7ac6d-20260912135437`:
workflow selection now refreshes from the authorized manifest API on opening and
identity changes, without falling back to stale cached presets. Each query
validates source workflow paths, current access and workflow allowlists before
session/tool initialization. Missing, malformed, revoked or unreadable sources
fail closed; accepted attachments remain read-only.

Backend authorization and picker reopen/failure regressions passed. This is a
check on each new request, not a claim of immediate revocation inside an already
running turn or an audit of every unrelated filesystem tool. Admin-only global
secret promotion is a separate explicit capability: [PLAT-308](plat-308.md).

## Design constraints discovered during the conversation

1. **A read-only permission tier already existed once and was deliberately
   removed** (`cmd/server/workflow_permissions.go` lines 14-24, removed
   2026-08-17): it was never actually configured in any deployment (every
   caller resolved to `owner`, since `WORKFLOW_READ_USERS` /
   `WORKFLOW_WRITE_USERS` / `WORKFLOW_OWNER_USERS` /
   `WORKFLOW_USER_PERMISSIONS` were never set), and its removal also cleaned
   up "a query-access gate full of unreachable checks against workshop modes
   that no longer exist," which cost real debugging time (PLAT-125). This
   plan restores the tier but wires it to concrete, currently-real
   enforcement points instead of the old dead workshop-mode gate — the
   mistake to avoid repeating is adding a permission check that nothing
   actually reads.
2. **"Run mode" is currently prompt-only, not enforced.**
   `WorkflowInteractiveWorkshopAgent.Execute`
   (`pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go`)
   calls `registerFullWorkshopAgentTools` unconditionally regardless of the
   `WorkshopMode` template variable — a Run-mode session today has every
   mutating tool available; the system prompt just asks the agent not to use
   them. The read-only user's whole safety property depends on Run mode
   becoming a real, enforced boundary, so this ticket makes that real as
   part of building the role, not as a side effect.
3. **Shell (`execute_shell_command`) is already folder-guarded, not
   unrestricted** — `createInteractiveWorkshopAgent` already scopes it via
   `SetWorkspacePathForFolderGuard`/`WrapWorkspaceToolsWithFolderGuard`.
   Today's write paths (`workshopWritePaths`) include `variables/`,
   `evaluation/`, `db/`, which already lets shell bypass several
   typed-tool-level restrictions (`update_variable`, `update_evaluation_plan`,
   `mutate_workflow_db`) by writing the underlying files/DB directly — so
   gating the typed tools alone would not actually close the loop. Run mode
   must also get its own narrower (here: empty) shell write-path list.
4. **`request_workflow_folder_access` never grants anything itself** — it
   only creates a pending request a human must explicitly approve via the
   desktop UI toolbar. Confirmed inert without that approval step, so it
   stays available in Run mode.

## Agreed design

**Superseded in one respect by the RCA below — kept here for the historical
record of what was first agreed, corrected there, not deleted.** The
corrected version: tool/shell restriction is keyed on `WorkflowAccessLevel`
(read vs write/owner), not on `WorkshopMode` (workshop vs run stays
prompt-only focus, exactly as today, freely available to every user
regardless of access level). Read that section for why.

- Read-only user **can chat** (not locked out of the chat surface — chosen
  explicitly over blocking chat entirely).
- ~~Read-only user is server-side forced into Run mode~~ — superseded; see
  RCA. `WorkshopMode` is not touched by this ticket at all.
- The mutating tool set becomes **genuinely restricted for a read-access
  identity**: everything that mutates plan structure, step config,
  variables, schedules, secrets, LLM config, or skills is excluded.
  Execution monitoring/triggering, read-only inspection, and human
  communication stay available regardless of access level. Full
  tool-by-tool classification recorded in the plan file used to build this
  (see Implementation below).
- Shell gets **zero write access for a read-access identity** (reads stay
  full — same broad read set as everyone else). Actual step execution runs
  through its own separately folder-guarded process regardless of the chat
  session's own shell grant.
- `request_workflow_folder_access` stays available regardless of access
  level (inert without human approval).
- `get_workflow_command_guidance` stays available; its existing per-`kind`
  `Modes` gating in `cmd/server/guidance/guidance.go`'s `allKinds` governs
  the focus axis only (workshop vs run) and is unrelated to this ticket's
  access-level axis — left as-is, not audited as part of this ticket.
- **No new user-creation mechanism needed.** Creating a read-only user is:
  add them via `AUTH_USERS` (or their normal login provider), then an owner
  grants them `read` access via the existing `WorkflowAccessPopup.tsx` UI
  (gets a third `'read'` option) or `WORKFLOW_USER_PERMISSIONS` /
  `PUT /workflow/user-permissions` — infrastructure that already exists and
  is already wired end to end for `write`/`owner`.

## Implementation (planned, not yet built) — corrected per RCA below

- `cmd/server/workflow_permissions.go`: restore `WorkflowAccessRead`, stop
  collapsing `read`/`reader`/`run`/`runner`/`view`/`viewer` aliases into
  `write`. (Done — landed ahead of this ticket's filing.)
- `cmd/server/server.go` / `cmd/server/workflow_phase_tools.go`: inside
  `installWorkflowPhaseTools` (already re-run every turn from
  `handleQuery`'s live request), resolve `workflowAccessForClaims(GetUserFromContext(ctx))`
  and pass the resulting access level down into tool registration.
  `WorkshopMode`/`SetWorkshopModeOverride` are untouched.
- `pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go`:
  gate the mutating subset of `registerFullWorkshopAgentTools`/
  `registerInteractiveWorkshopTools` (plan-mod tools + the ~41-tool
  Workshop-only subset) on access level, modeled on the existing
  `prepareReadOnlyBackgroundAgentTools`/`FilterCustomToolsByCategory`
  pattern rather than a new mode-branch; pass empty shell write paths to
  `SetWorkspacePathForFolderGuard` for a read-access identity, independent
  of `WorkshopMode`.
- `cmd/server/secrets_tools.go`: skip the four secret-mutation tools for a
  read-access identity; `list_secrets` stays.
- `frontend/src/components/workflow/WorkflowAccessPopup.tsx`: add the
  `'read'` option to the access-level dropdown.
- `cmd/server/guidance/guidance.go`: out of scope for this ticket (governs
  the focus axis, not access level — see RCA).

Full tool-by-tool RUN-safe/WORKSHOP-only classification, exact line numbers,
and the verification plan are recorded in the approved implementation plan
this ticket was filed alongside (not duplicated here — see the commits that
follow this filing for the executed version).

## RCA: why tool registration is gated by access level, not by WorkshopMode

Mid-implementation, re-examination found `docs/design/agent_tool_surface_single_source.md`
— a dedicated design doc recording a deliberate, largely-executed decision to
remove mode-based tool filtering from AgentWorks entirely, with three
documented production incidents it fixed (Video Studio secrets tools,
`list_llm_capabilities`, a Builder background-child mismatch). Its core
argument: a coding CLI caches its tool catalog once at launch; *removing* a
tool the agent already knows about degrades gracefully (it calls, gets an
error, adapts), but *adding* one after launch is invisible (the agent can't
ask for something it was never told exists, so it silently shells out
instead). `GetToolsForWorkshopMode` filtered the registered-tool catalog
itself by the `WorkshopMode` request parameter — a value the *same* live,
continuously-cached chat session could freely toggle turn to turn — and that
mismatch between what the CLI's cache believed existed and what was actually
registered on a later turn was the recurring bug. `installWorkflowPhaseTools`'s
own doc comment already states the fix landed: *"registers one complete tool
surface and applies no workshop-mode narrowing... Mode is a focus rule now
and lives in the Builder prompt."* Confirmed live: `GetToolsForWorkshopMode`
has zero callers today — dead code, not wired to anything.

This looked, at first read, like it invalidated the whole "restrict Run
mode's tool set" mechanism this ticket depends on. Two follow-up questions
resolved it instead of scrapping it:

1. **Does the doc's objection apply to a role-based split, not just a
   mode-toggle split?** No — traced to `prepareReadOnlyBackgroundAgentTools`
   / `runReviewPlanAgent` (`interactive_workshop_manager.go`), an
   already-shipped precedent that creates a *separate, dedicated* agent
   instance (its own `sessionID`, its own `configureWorkshopToolAgentSession`
   call) with a permanently reduced tool set decided once at that agent's
   creation (`orchestrator.FilterCustomToolsByCategory`, only
   `execute_shell_command` read-only-folder-guarded + `human_tools:*` +
   `query_workflow_db`). The design doc's own table names exactly this
   pattern as the one to **keep**: *"tool-agent registration...paired with
   writePaths — authority — Reader vs Writer — keep"*, distinct from the
   deleted *"focus — BUILD vs DEBUG"* gate. A read-only user's chat session
   and an admin's chat session never share a `sessionID` — the specific bug
   (one session's catalog drifting across its own turns) cannot occur
   between two people who never share a session in the first place.
2. **Could a user's access level change mid-session and leave a stale,
   over-privileged registration in place?** Traced the actual call path:
   `handleQuery` (`server.go:3045`, the `/api/query` HTTP handler, invoked
   once per message) derives `isWorkflowPhase := req.AgentMode ==
   "workflow_phase"` from *that request's own payload*
   (`server.go:3460`), and the tool-registration block containing
   `installWorkflowPhaseTools` runs inside that same per-request condition —
   confirmed by reading the handler top to bottom, not inferred from a
   comment. So `installWorkflowPhaseTools` re-runs, and re-resolves the
   caller's access level fresh from that turn's own JWT claims via
   `GetUserFromContext(ctx)`, on **every conversational turn**, not once at
   session creation. A demotion takes effect on the person's very next
   message; if the agent still believes an excluded tool exists from an
   earlier turn and tries to call it, the MCP bridge returns a normal
   "unknown tool" error — exactly the degrade-gracefully path the design doc
   accepts as safe, not the silent-shell-fallback failure mode it warns
   against. This closed the one gap that would have justified an extra
   per-executor live-recheck layer; that layer was dropped as unnecessary
   once this was confirmed, not merely simplified away.

**Net design correction from the original filing above:** tool-registration
exclusion is keyed on `WorkflowAccessLevel` (the authority axis — read vs
write vs owner), resolved fresh per turn from the live request's own claims —
**not** on `WorkshopMode` (the focus axis — workshop vs run), which stays
exactly as it is today: prompt-only, freely togglable by any user regardless
of role, per the design doc's own rule that conflating focus and authority
in code is "the same one-decision-two-places failure" the doc exists to
remove. Forcing `WorkshopMode="run"` server-side for read-only users — part
of the original design above — is dropped for the same reason: it would
smuggle an authority decision through a variable the codebase has already
deliberately committed to treating as advisory-only. The shell folder-guard
write-path restriction is gated the same way: on access level, not on
`WorkshopMode`.

## RCA #2: corrected back toward WorkshopMode as the single gate

After the implementation above shipped and was verified live, the user
reviewed a real read-only session's chat response and pushed back hard on
one specific consequence of RCA #1's split: the agent's *system prompt* was
identical for a read-only user and a full admin (RCA #1 deliberately left
`WorkshopMode`/the prompt untouched), so a read-only user asking "what can
you do?" got a generic Builder self-description — "I can design the
workflow..." — that was true of the prompt but false of the actual tool
catalog. That is a real UX defect RCA #1's reasoning did not address (RCA #1
was entirely about *tool-catalog* correctness, not prompt correctness).

The fix path went through several iterations, each one narrowing on a wrong
premise before landing on the final design:

1. First fix: add a `ReadOnlyAccess` template variable, separate from
   `WorkshopMode`, so the prompt could say "you're read-only" without
   touching the mode axis. This worked but produced two overlapping
   variables in the same prompt (`WorkshopMode` and `ReadOnlyAccess`),
   which the user immediately flagged as confusing on sight.
2. The user asked to key the prompt check on `WorkshopMode == "run"`
   instead of the separate `ReadOnlyAccess` variable. This was correctly
   flagged back as a *correctness* risk, not just a style question: RCA
   #1 established that a normal write/owner user can legitimately be in
   Run mode too (Bot Connector WhatsApp/Slack routes explicitly set
   `workshop_mode: "run"` per route, with full account access) — keying
   the "your tools are absent" claim on mode alone would have made that
   claim false for those sessions.
3. Investigating that objection surfaced the actual resolving fact: **there
   is no live UI path for a normal human user to toggle `WorkshopMode`
   at all.** Grepping the entire frontend for every call site of the
   store's `setWorkshopMode` action found exactly one, gated behind a
   slash command's `requiredWorkshopMode` — and no built-in command
   requires `"run"` alone. The only real sources of `WorkshopMode="run"`
   are: Bot Connector's per-route config, the scheduler, and the
   agent-profile runtime — none of them a human toggling mode mid-chat.
   This meant RCA #1's own justification (protecting against the
   documented same-session tool-catalog-cache bug from
   `docs/design/agent_tool_surface_single_source.md`) does not actually
   apply to a human chat session pinned to Run mode at creation and never
   switched — there is no live toggle to reopen that bug through.
4. With that confirmed, the user made the final call explicitly and
   repeated it after seeing the Bot Connector tradeoff spelled out with a
   concrete before/after preview: **`WorkshopMode` becomes the single gate
   for tools, prompt, and skills — full stop, for every caller, not just
   read-only ones.** A Bot Connector/scheduled/agent-profile session sitting
   in Run mode now gets the exact same reduced tool set a read-only human
   does. This is treated as intentional, not a regression: Run mode's
   prompt guidance already told every such caller not to use these tools
   ("Workshop-Owned Tools — Visible But Not Yours"); this change makes that
   guidance literally true (tool absent) instead of merely requested (tool
   present, please don't call it).

**What `WorkflowAccessLevel` still does, and nothing more:** it decides, once
per turn, whether this session's `WorkshopMode` gets force-pinned to `"run"`
(server-side, in `server.go`, before both the prompt render and
`installWorkflowPhaseTools`) — and nothing downstream ever reads
`WorkflowAccessLevel`/`readOnlyAccess` again. `WorkshopChatSession.readOnlyAccess`,
`InteractiveWorkshopManager.readOnlyAccess`, `SetReadOnlyAccess`, the
`ReadOnlyAccess` prompt template variable, and the now-fully-unused
`pkg/common/WorkflowReadOnlyAccessKey` context-key plumbing were all removed
as part of this correction — not deprecated, deleted, since nothing consumed
them once `WorkshopMode` became the sole gate.

**Explicitly out of scope for this correction** (confirmed with the user):
the shell/file folder-guard write-path restriction in `server.go` (the
workflow-phase chat session's own write grant) stays keyed on
`WorkflowAccessLevel` directly, not `WorkshopMode` — switching that too would
also cut file-write access for live Bot Connector sessions sitting in Run
mode, a materially different and larger blast radius than the tool-catalog
change above, and the user's instruction was scoped to "tool and prompt
skills," not folder guard.

## Verification

Implementation complete (RCA #2 is the final, current state). Results:

- `go build ./...`: clean (only the pre-existing, unrelated
  `libonnxruntime.dylib` linker version warning).
- `gofmt -l` on every touched Go file: clean.
- `go test ./cmd/server/ ./pkg/orchestrator/agents/workflow/step_based_workflow/...`:
  all pass. `cmd/server/virtual-tools` fails at `go vet`/`go test` setup on a
  pre-existing missing-module import (`mcpagent/agentreview`), unrelated to
  this ticket, present before this work started.
- Go tests:
  - `cmd/server/workflow_permissions_test.go`: `read`/`reader`/`run`/
    `runner`/`view`/`viewer` aliases resolve to `WorkflowAccessRead` (not
    `WorkflowAccessWrite`); `write`/`owner` aliases unaffected;
    `workflowPermissionInfo(WorkflowAccessRead)` yields
    `CanRunWorkflows=true`, `CanWriteWorkflows=false`,
    `CanManageWorkflowAccess=false`. Still valid post-RCA#2 — this tier's
    parsing is unrelated to which mechanism gates tools.
  - `pkg/orchestrator/agents/workflow/step_based_workflow/workshop_readonly_gate_test.go`:
    `TestMutatingWorkshopToolsAreGatedByRunMode` — an AST-based test
    (matching this file's existing `workshop_tool_allowlist_test.go`
    pattern, since `registerInteractiveWorkshopTools` has too many
    construction-time dependencies to invoke with fakes) proving all 18
    mutating tools (`debug_step`, `update_step_config`, `review_plan`,
    `review_step_code`, `update_variable`, `add_group`, `update_group`,
    `delete_group`, `update_workflow_config`,
    `set_workflow_contract_version`, `create_schedule`,
    `create_calendar_schedule`, `update_schedule`, `delete_schedule`,
    `import_skill`, `uninstall_skill`, `install_skill`,
    `set_workflow_llm_config`) are registered only inside an
    `if iwm.isRunModeRestricted() {...} else if err := mcpAgent.RegisterCustomTool(...)`
    guard, and that `execute_step`/`query_step`/`run_full_workflow`/
    `list_secrets` are never gated. `TestRunModeZeroesBackgroundAgentWritePaths`
    proves `runBackgroundTaskAgentSequence`'s write-path narrowing zeroes
    shell write grants when `workshopModeOverride == "run"`, while
    `workshopWritePaths` itself still returns a non-empty list for a normal
    (Workshop-mode) caller (guards against the test passing vacuously).
- Frontend: `npx tsc --noEmit -p .` clean; `npm run build` clean (pre-existing
  bundle-size warning only, not a failure).

Enforcement points, all keyed on `WorkshopMode` (per RCA #2 — `WorkflowAccessLevel`
now does exactly one thing: force-pins a read-only identity's `WorkshopMode` to
`"run"`, server-side, every turn):
- `RegisterPlanModificationTools` (both call sites in
  `installWorkflowPhaseTools`, plus inside `registerFullWorkshopAgentTools`
  for background sub-agents) — skipped entirely in Run mode.
- The 18 Workshop-only tools inside `registerInteractiveWorkshopTools`, plus
  `mark_changelog_artifact_reviewed` — skipped in Run mode, via
  `iwm.isRunModeRestricted()` (`canonicalWorkshopMode(iwm.workshopModeOverride) == "run"`).
- `registerSecretManagementTools` — `set_user_secret`, `delete_user_secret`,
  `set_workflow_secret`, `delete_workflow_secret` skipped in Run mode;
  `list_secrets` always registered. (The separate multi-agent-chat call site
  in `server.go` — an unrelated surface with no `WorkshopMode` concept —
  still keys on `WorkflowAccessLevel` directly, unchanged.)
- `reorganize_knowledgebase`/`consolidate_knowledgebase` — skipped in Run mode.
- `phaseTemplateVars["WorkshopMode"]` is force-set to `"run"` in `server.go`
  for a read-only identity, on every turn, before both the system-prompt
  render and `installWorkflowPhaseTools` — the single point of truth both
  read. `workshopSession.SetWorkshopModeOverride(phaseTemplateVars["WorkshopMode"])`
  is then called unconditionally (not just on session reuse) in
  `installWorkflowPhaseTools`, closing a gap where a freshly-created session's
  first turn could have missed the pin.
- The system prompt's `## Run Mode — Execute and Monitor Only` section
  (merged from two previously-separate, now-deleted sections) states plainly
  that mutating tools are genuinely absent, not just discouraged — keyed
  only on `{{if eq .WorkshopMode "run"}}`, with no separate `ReadOnlyAccess`
  template variable.
- Shell/file write paths: `server.go`'s main chat-session folder guard grant
  and `runBackgroundTaskAgentSequence`'s background-agent folder guard both
  still key on `WorkflowAccessLevel` directly (deliberately **not** moved to
  `WorkshopMode` — see the out-of-scope note above RCA #2); read paths stay
  full and unchanged in both cases.
- `agent_profile_workflow.go`'s agent-profile-driven workflow runtime
  (already hardcoded `WorkshopMode="run"`) now gets its Workshop-only-tool
  restriction automatically, with no separate access-level thread needed —
  `SetReadOnlyAccess`/`isReadOnlyAccess` were removed from this file entirely.
- `WorkshopChatSession.readOnlyAccess`, `InteractiveWorkshopManager.readOnlyAccess`,
  `SetReadOnlyAccess`, the `ReadOnlyAccess` prompt template variable, and
  `pkg/common/workflow_access_context.go` (`WorkflowReadOnlyAccessKey`,
  `IsWorkflowReadOnlyAccess`, `WithWorkflowReadOnlyAccess`) were all deleted
  as part of RCA #2 — none had any remaining consumer once `WorkshopMode`
  became the sole gate.

Also removed as dead code at the user's explicit request, found while tracing
this ticket's live tool-registration paths: `InteractiveWorkshopOnly`
(zero callers anywhere in the codebase), and everything reachable only from
it — `createInteractiveWorkshopAgent`, `enableWorkflowMainCodingAgentKeepAlive`,
`newWorkflowInteractiveWorkshopAgent`, the `WorkflowInteractiveWorkshopAgent`
struct and its `Execute` method, and the two now-orphaned unit tests that
covered `enableWorkflowMainCodingAgentKeepAlive` directly
(`controller_agent_factory_test.go`). `interactiveWorkshopSystemTemplate`/
`interactiveWorkshopUserTemplate` were kept — confirmed still live via
`planning_exports.go`'s `RegisterWorkshopChatTools`, the real path that
builds this same system prompt today. Initially misclassified
`registerFullWorkshopAgentTools`/`workshopWritePaths`/
`prepareBackgroundWorkshopToolDefinitions` as part of the same dead chain;
corrected before deleting anything after tracing them to the live
`run_in_background` background-sub-agent path
(`runBackgroundTaskAgentSequence`).

## Live reverify (local)

Ran a real local server (`WORKFLOW_USER_PERMISSIONS="default=read"`, the
default single-user local-dev identity flipped to read-only) against a live
workflow (`confida-login`) and confirmed via server logs the read-only
identity's `RegisterPlanModificationTools`/`reorganize_knowledgebase` calls
were actually skipped for that exact session/turn, and the folder guard
logged no whole-workflow write grant. The agent's own greeting correctly
self-identified as "in Run mode" (the RCA #2 prompt fix working as intended,
replacing the earlier misleading generic Builder self-description).

This surfaced two real bugs, both fixed and reverified live before this
commit:

1. **Shell reads of `Workflow/...` paths were incorrectly denied**, not just
   writes. Root cause: `wrapExecutorsWithWorkflowPhaseFolderGuard`
   (`tool_setup.go`) had its own separate, earlier-stage guard — a shell-command
   *text* scan (`hasWorkflowAccess`) that decided "may this command reference
   `Workflow/` at all" from the same write-folder parameter used to grant
   writes. Emptying that parameter to correctly deny writes for a read-only
   identity also emptied `hasWorkflowAccess`, incorrectly blocking reads too —
   a bug the real, path-based folder guard (`SetSessionFolderGuard`'s
   `readPaths`, unaffected) never had.
2. **Browser tools bypassed the read-only restriction entirely** — a separate
   folder-guard closure (feeding `registerCodingBrowserTools`) was never
   gated by `currentUserIsReadOnly` at all, and would have granted a
   read-only session's browser tools full write access. Found while tracing
   bug 1; unrelated to it, but same fix shape.

Investigating bug 1 led to removing its root cause rather than working
around it: the `hasWorkflowAccess` text-scan check was traced via `git log
-S` to the commit that introduced `tool_setup.go` (`c2c7cba47`, 2026-03-09,
not a targeted fix for a prior incident) and confirmed to have zero test
coverage anywhere in the codebase. Its only sensible purpose — blocking a
general multi-agent chat session from touching `Workflow/` unless the user
explicitly attached one via the `#workflow` picker — belongs to
`wrapExecutorsWithChatModeFolderGuard`, a sibling wrapper over the same
shared low-level function that, per a full grep, has **zero live callers**
in the actual request-handling flow today (only exported for a test file).
The only caller that ever reaches this check in production is
`wrapExecutorsWithWorkflowPhaseFolderGuard`, where it is structurally
pointless — a workflow-builder session is by definition always working
inside its own `Workflow/<name>` folder — so it silently passed 100% of the
time for every session type that existed before read-only access did, and
broke the instant a legitimately-empty write-folder parameter appeared.
Deleted the check (`hasWorkflowAccess` computation + its guard block) from
`wrapExecutorsWithFolderGuard` entirely, and the now-dead `workflowAccessDenyMode`
parameter from all three functions in its call chain, rather than leave a
workaround patched around a check that protected nothing live. `go test
./cmd/server/` (including `tool_setup_test.go`'s `wrapExecutorsWithChatModeFolderGuard`
write-blocking tests) stayed green throughout.

Confirmed via live retest after the fix: the same `cat`/`sed` commands
against `Workflow/confida-login/...` paths that previously failed with
`access denied: shell commands cannot reference 'Workflow/' folder in
workflow builder` now succeed for the read-only session.

Not yet done: verifying this same read-only flow against a real second
account in actual multi-user mode (`MULTI_USER_MODE=true` + distinct
`AUTH_USERS` identities) rather than the single local-dev identity flipped
to read-only — the local dev script hardcodes single-user mode, so this
session's live testing exercised the permission/gating logic paths
correctly but not the separate per-account chat-history isolation path
(verified instead by code inspection: `chatHistoryRoot(userID)` is
genuinely per-`userID`-keyed, confirmed no shared state).

## Frontend UX polish (cosmetic-only, no new server-side enforcement)

Live testing surfaced several UI controls that either promised a capability
the read-only account doesn't have (misleading, since the click would just
fail or silently do nothing after the fact), or exposed operational actions
the user decided a read-only account specifically shouldn't see even though
the underlying tool (`stop_step`, `trigger_schedule`) stays server-side
available in Run mode for everyone by design (see the Run-mode tool-safe
bucket above). All of these are pure `isWorkflowReadOnly(user, isMultiUserMode)`
UI hides — none change server-side enforcement, which was already correct
either way:

- `ModePresetBar.tsx`: the "Edit automation" gear icon hidden, both next to
  the active automation name and inside the preset-picker dropdown list.
- `WorkflowChatTabs.tsx`: the in-tab Stop button (busy-tab control) and the
  "Interact in Automation Builder" icon (converts a view-only scheduled/bot-run
  tab into an interactive Builder chat) both hidden.
- `WorkflowScheduleRunsPanel.tsx` (the Schedules popup): per-schedule
  Stop/Run now/Resume buttons hidden (both the compact and expanded list
  layouts); in the "⋮" action menu, "Pause schedule" and "Delete schedule"
  items hidden individually ("Show details" stays visible for everyone);
  the global scheduler pause/resume control and the per-workflow-group bulk
  pause/resume control both hidden.
- `WorkflowCapabilitiesPanel.tsx` (opened via the canvas toolbar's
  skills/MCP/secrets/browser/LLM-config icons — a separate entry point from
  the `PresetModal` the gear icon opens): the Save footer button hidden:
  nothing in that panel can actually persist for a read-only account.

Verified: `npx tsc --noEmit -p .` and `npm run build` clean after each
change (pre-existing bundle-size warning only, not a failure).

## 2026-09-12 — Workflow picker arrow keys skipped the middle row

The search input and document keydown listener both handled the same arrow event,
advancing selection twice. With rts-latency, rts-aws and automation-testing, one
ArrowDown selected automation-testing. Input arrow/Escape handling now stops
propagation, preserving document handling when focus has not moved into search.
A DOM regression reproduced the skip before the fix and covers down/up selection
and single Escape dismissal. This changes navigation only, not list permissions.
Implementation verified locally; RTS deployment verification pending.

## 2026-09-12 deployment confirmation

The latest follow-up above is included in RTS release `3a37a1c-20260912143119`
(app `3a37a1c75`). Source revisions and all three services were verified after
activation. The configured-environment Linux sandbox regression passed.
Earlier pending-deployment notes are superseded; feature-specific live acceptance
limits remain as documented. No user accounts/sharing or notification recipients
were changed during verification.

## 2026-09-18 — Revalidate chat policy before retained delivery

State: implemented and regression-tested locally; deployment and Confida live reverify pending.

Confida testing session `be5087d3-b2c4-4fe1-a114-007a3fe12dcd` was built in Run mode. Logs at 14:19:14 server time show `mode=run`; calls to `update_workflow_config` at 14:21:10 and 14:31:21 failed because the tool was not registered. The account subsequently has owner access, but warm delivery bypassed definition reconstruction. The attempted `mcp_servers:[]` payload was also unsupported: workflow detachment uses `remove_servers` with configured server names. Neither failed call removed an integration.

The existing ChatPolicyKey already hashes effective mode, origin, admitted capabilities, product chat definition and MCP configuration. Do not add a parallel permission fingerprint or change the durable chat ID. `workflow_retained_policy.go` revalidates current workflow access and compares that key before eligible `/api/query` retained delivery and before `/live-input` delivery. Unknown or mismatched old admission falls back to construction; revoked access fails closed. The early comparison does not publish the new key, so the existing reconnect/handoff path can still recognize the old policy.

A mismatch cancels the old foreground turn, closes its actual native provider and routes the message through normal serialized setup. Explicit new turns and auto-notifications do not perform early interruption merely to attempt retained delivery. Existing tool execution context checks still revalidate permissions per invocation; the fingerprint is lifecycle coordination, not replacement authorization.

Verification covers promotion, demotion, revoked access, unchanged warm admission, unchanged previous-key visibility to reconnect, live-input next-turn dispatch and preservation of the current message/workflow. Explicit new-turn admission regression protects the foreground turn. This is revalidation at the next message/invocation, not a claim of instant revocation of every already-started external action.

The separate bounded suggestion exception is [PLAT-330](plat-330.md).

### Follow-up: restored Run mode after owner promotion (2026-09-18)

Confida release `confida-f75a304b-20260918150257` correctly admitted Builder tools, but a restored request still carried `execution_options.workshop_mode=run`. Session metadata, reconnect notices and the private native CLI directory continued using that stale value. Normalize the workflow-builder conversational request from resolved current access before those consumers run, preserving other execution options and leaving Crew profiles and headless execution unchanged. Regression checks cover owner promotion, reader demotion, missing options, private directory mode selection and the exclusive authoring guard.
