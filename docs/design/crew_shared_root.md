# Crews at a shared root (`Crew/<id>`)

Status: design, 2026-09-26. Not started.

## Problem

A crew lives in its owner's private tree, `_users/<owner>/Chats/Work/projects/<slug>-<id8>`,
and so has two names:

- **The owner's UI** sends the user-relative `Chats/Work/projects/<id>`. The workspace service
  silently expands `Chats/…` under the caller (`workspace/utils/path.go:136`).
- **Readers** (Crew Run mode) must send the physical `_users/<owner>/…` form, because the short
  one would expand into their own tree.

Every endpoint has to translate between the two, and each does it by hand. There are about 17
helpers and 15 hand-written `_users/` checks. Each new feature breaks on whichever form its author
did not test. On 2026-09-26, `window.report.run` returned 400 for the owner, and the live feed
drops every crew notice.

Workflows never had this problem. They live at a shared `Workflow/<name>` with owners and readers
in `workflow.json`, so there is one name and access comes from the manifest rather than the
location.

## Decision

Crews move to a shared root, `Crew/<slug>-<id8>`, keeping the folder name they have today.

- **Owner:** comes from the crew manifest (`product.json` gains `owner_id`), never from the path.
- **Access** stays as it is today (Crew Run mode):
  - the owner gets full access;
  - any other user with the Crew product gets Run mode;
  - users without the Crew product get nothing.
- **Old paths**, both logical and physical, keep resolving through one alias resolver
  indefinitely. Old links, stored references we missed, and open tabs keep working, and a
  warning is logged.
- Other products (SparkQuill `Chats/SparkQuill/activities`, Video Studio) are **not** moved. They
  have no cross-user readers.

## One resolver

```go
// crewref.Resolve(callerID, anyPath) -> (root "Crew/<id>", ownerID, ok)
```

This accepts `Crew/<id>`, `Chats/Work/projects/<id>` (resolved under the caller) and
`_users/<o>/Chats/Work/projects/<id>`. Every API entry point and every stored-reference reader
calls it. After the migration, the old helpers are deleted:
- `crewProjectOwnerID`, `isCrewProjectPath`, `canonicalCrewWorkspaceRoot`;
- `reportRunPhysicalCrewRoot`, `isOtherOwnerCrewPath`, `crewReaderSharedAsset`'s segment checks;
- `pkg/common` `ClassifySessionWorkspace`'s crew branch, and the frontend's prefix regexes.

A guard test fails if any new non-test code in `cmd/server` matches `Chats/Work/projects` or
`"_users/"` for crews.

## Access gates (must land before or with the move)

Today the only thing protecting crews is that foreign `_users/*` is blocked. A shared `Crew/` has
no such protection, so these gates are needed:

1. **`cleanAgentProfileWorkspace`** (`agent_profile_runtime.go:97`) must check `Crew/<id>` against
   the manifest owner or the reader policy. Otherwise any user could chat in someone else's crew
   in full mode.
2. **The raw workspace proxy** (`workspace_proxy.go:96,184,481`) must gate `Crew/` the way it gates
   `Workflow/`: the owner reads and writes; readers get no raw access and go through the
   `/shared-projects` endpoints as today.
3. **The live feed's `liveFeedAccess.visible`** must use crew visibility for `Crew/` notices.

## Owner from the manifest, not the path

These currently take the owner from the path and must switch to the manifest:
- `services/bot_scope.go:20` `ValidateBotScope`;
- `pkg/workflowtypes/crew_attachments.go:74` `ValidateCrewAttachmentBinding` (it also requires a
  `projects` segment, so it rejects `Crew/<id>` today);
- `crew_access.go:41` `crewProjectOwnerID` and its about 10 callers (schedules, triggers,
  webhooks, reader chat mirror);
- `services/bot_connector.go:3705` `routeWorkspaceUserID`.

## Migration (one-time, at startup, under a lock, idempotent, with a marker)

Model it on `crew_bot_scope_migration.go` + `RewriteSlackScopes`: keep a record, and log crews
where it cannot find exactly one owner.

For each `_users/<owner>/Chats/Work/projects/<p>` with a valid work `product.json`:

1. **Stop live coding-CLI panes for the crew** (tmux `mlp-*` for that workspace). A deploy restarts
   everything anyway.
2. **Write `owner_id`** into `product.json`.
3. **Rename** the folder to `Crew/<p>`. It is on the same filesystem, so this is instant. If `Crew/<p>`
   already exists, stop that crew and log it; never merge.
4. **Record the alias** `old logical`, `old physical` → `Crew/<p>` in `_system/crew-path-aliases.json`
   (read by the resolver).
5. **Rewrite stored references.** Store by store:

   | Store | Field |
   |---|---|
   | `<crew>/builder/conversation/session-*.json` and readers' `_users/<r>/chat_history/…` | `workspace_path`, `runtime.workspace_path` |
   | same | `runtime.agent_session_handle.provider.working_dir`, `project_dir_id` (**absolute paths**) |
   | `_users/<u>/chat_history/product-conversations.json` | `workspace_path` |
   | `_users/<u>/chat_history/submissions/*.json` | `project` |
   | `_users/<u>/chat_history/crew-creation/*.json` | `workspace_path` |
   | every `Workflow/*/workflow.json` | `crew_attachments[].crew_workspace_path`, `workflow_context_paths` |
   | every crew `workflow.json` | `workflow_context_paths` |
   | Slack config (connections + channel routes), bot `allowed_channels` | `workspace_path` |
   | WhatsApp channel routes (meta table) | `WorkspacePath` |
   | cost ledger sqlite | `workflow_id` (UPDATE old → new; logical owner rows gain the owner) |

   Path-independent, so no change is needed: triggers and callers (keyed by manifest ID), Slack
   thread bindings, structured-chat-events sqlite, `product-schedules.json`, work-folder access.
   Webhook and schedule discovery rescan the root; they must scan `Crew/` instead.
6. **Coding-CLI native context.** This is the highest risk. mcpagent replaces the working directory
   with the saved `handle.WorkingDir` on resume, and `claude --resume` only searches
   `~/.claude/projects/<slug(cwd)>/`. For every account home (`$HOME` and each
   `CLAUDE_CONFIG_DIR` from `provider_connections.go:150`), copy
   `projects/<slug(old)>/<sid>.jsonl` (and its sidecar dir) to `projects/<slug(new)>/`. Codex's
   `--cd` / `project_dir_id` is fixed by step 5. Cursor, Pi and Muse only need `working_dir`
   (step 5). Without this, AgentWorks history survives but each crew silently starts a fresh native
   session.
7. **Browser profile**: rename the profile dir keyed by `hash(old physical)` to `hash(Crew/<p>)`, so
   saved browser logins survive (`pkg/common/types.go:659`).
8. **Projected files** (`.claude/skills`, `.agents/skills`, `.pi/skills`, `CLAUDE.md`, `AGENTS.md`)
   move with the folder and are regenerated on the next turn. No action is needed.

## Code changes outside the migration

- **`internal/workproduct/product.yaml:162`**: `projects_root: Crew`. The generic product-project
  store (`product_conversation_registry.go:750,774`) scans `<root>/*/product.json`. It must treat
  `Crew` as shared (not per-user) and filter by manifest owner for "my crews".
- **Owner transcripts**: allowed conversation paths (`chat_history_persistence.go:3399,3691`) and
  submission discovery roots (`chat_submission_journal.go:594`) should use `Crew/`.
- **Cost overview** (`cost_overview.go:86`): the crew branch keys on `Crew/<id>` and the manifest
  owner.
- **Live feed**: `livefeed.WorkflowRoot` and the frontend `liveFeedWorkflowRoot` accept `Crew/<id>`.
  Crew runs publish report and human-input notices. This fixes crew dashboards never auto-refreshing.
- **`window.report.run`**: drop the crew path special cases; resolve through the resolver.
- **Frontend**:
  - `WORK_PROJECTS_ROOT` becomes `Crew`.
  - `productProjects.ts:242` must stop creating crew folders client-side; creation goes through the
    server only (`crew_creation.go`), which also writes `owner_id`.
  - `sharedCrewFiles` and `ChatInput`'s `_users/` shared-client switch become "owned by me?" from the
    crew summary.
  - Report page, `ReportDocumentSwitcher`, `CostsOverview` and `slackWorkflowConnection` stop parsing
    prefixes.
  - `activitySessions.ts` regex covers `Crew/`.
- **Folder Guard**: crew roots are `Crew/<id>`. Reader read-only is unchanged, and so are workflow
  crew attachments (`WORKFLOW_CREW_*`).

## Rollout

1. **Access gates and the resolver** first, accepting all three forms. This ships with no behaviour
   change.
2. **Switch readers and writers** to the resolver and `Crew/`, and add the migration. Put the guard
   test in.
3. **Verify on an isolated server** (per the P0-gate recipe) with a copy of RTS's 4 crews (18 GB, all
   one owner) and their `~/.claude` stores:
   - owner chat resumes the **same** native CLI session, for Claude, Cursor and Codex;
   - a reader gets Run mode and cannot write;
   - Slack DM and channel for a crew bot;
   - a crew trigger and schedule;
   - a workflow crew step and the attachment env;
   - dashboard `window.report.run` and live refresh;
   - Costs for the crew;
   - browser logins kept;
   - an old `Chats/Work/projects/…` link still opens.
4. **Deploy in a quiet window.** The migration runs once on startup.
5. **After a stable period**, remove the reader copies of old-path handling. The alias resolver
   stays.

## Size

About 17 Go files and 8 frontend files of real changes, plus the migration. 74 Go + 25 frontend test
files hardcode the old path, mostly mechanically. Roughly 2–4 days including isolated verification.

## Open questions

- Folder naming: keep `<slug>-<id8>` (proposed; renames are cheap) or use the bare manifest ID.
- Should `Crew/` be listable by everyone through the raw proxy (like `Workflow/`), or stay behind
  `/shared-projects`? Proposed: stay behind `/shared-projects` for readers, so Crew Run mode does
  not change.
