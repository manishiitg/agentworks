# Decisions and open issues

A running log for people and coding agents working on this repository. Read it
before changing behaviour it covers; add an entry (newest first) when you make
or reverse a decision, and move an open issue to a decision once it is settled.
Each entry says what was decided, why, and where it lives in the code.

Design references for the linked runtime decisions:

- [Crew Run/Builder roles and private project links](design/project_instruction_files.md#crew-linked-runtimes).
- [Workflow Run/Builder project links, permissions and resume](design/workflow_shared_folder_plan.md).
- [Workflow step output links and artifact lifetime](design/project_instruction_files.md#workflow-step-outputs).

## Decisions

### 2026-10-01 — Slots on the RTS host: one shared build step, opt-in mode, SSM provisioning

- **Decided.** Every deployment builds and installs slots through one shared script, `deploy/common/slots.sh`
  (build `slotctl` and `slottmux`; install the tmux front-end outside the releases), called from both
  `deploy/rootless-linux/build-and-activate.sh` and `deploy/aws-ec2/server/build-and-activate.sh`. The
  provisioning script moved to `deploy/common/provision-slots.sh` and takes `APP_DIR`, `DOCS` and
  `SERVICE_HOME`, so the same script serves `/srv/<product>` hosts and RTS. The two build scripts themselves
  are still separate (RTS has its own Docker, CloudFront and AppArmor steps); merging them is a larger
  change and is not done.
- **Decided.** `AGENTWORKS_SLOTS=optin` (new): a user who holds a slot runs shell commands as it; a user without
  one is unchanged (`on` still refuses them). RTS runs Video Studio and Workflow/Crew runs that write into
  shared folders a slot account cannot write yet (the per-slot state roots item below), so it rolls out per
  user. The RTS build writes `optin` only when `/etc/agentworks/slots.json` exists, i.e. after an administrator
  ran `deploy/aws-ec2/slots-admin.sh init`.
- **Decided.** RTS has no sudo and SSH is deploy-only, so root steps go through SSM Run Command
  (`deploy/aws-ec2/slots-admin.sh`, like `install-system-tools.sh`). `init` also installs `acl` (setfacl).
- **Open.** CLIs as the user's own account (`AGENTWORKS_SLOT_CLI*`) are not enabled on RTS; shared Workflow and
  Crew folders still need slot-writable state roots before shell-as-slot can cover those runs.

### 2026-10-01 — Foreground completion clears chat header loading despite stale status
- The latest foreground turn's completion event clears the header spinner even
  when tab flags or the active-session cache still report foreground work. The
  next user/start event resets that completion signal; actual background work
  continues to show activity. This applies through the shared hook to Code,
  Crew and workflow chats.
- Reuse ChatArea's existing completion types and child-execution scope rules in
  shared utilities, including its exclusion of restored intermediate narration.
  Regression tests exercise the real store, hook and rendered transcript with
  stale busy flags, completion, and the next message. Code:
  `foregroundTurnActivity`, `runtimeEventScope`, `useChatRuntimeActivity`.

### 2026-10-01 — Review fixes: no cross-user path resolution, server secrets out of agent environments

- **Decided.** `utils.ResolveUserPath` refuses another user's tree: a path under `_users/<id>/` must be
  the requester's own, as written (`_users/bob/...` used to resolve for anyone) and after symlinks are
  followed. `IsValidFilePath` (used by every other handler) refuses a symlink that carries a path from one
  user's tree into another's, or from a shared folder into a user's tree. Checked first on excellence: no
  existing link does either. Regression tests in `workspace/utils/path_cross_user_test.go`. Other
  resolvers' call sites inherit the hop rule; handlers that take a user-supplied path without
  `ResolveUserPath` are the next place to look.
- **Decided.** Shell commands (`security.buildNativeEnvironment`) and CLI launches
  (`llmtypes.ScopedCodingAgentEnvironmentPlan`, now also when no secret scope is declared) never inherit
  the host's server-owned secrets: `AUTH_SECRET`, `ACCESS_PASSWORD`, `AUTH_USERS`, `GLOBAL_SECRET_*`, the
  server and bridge tokens. Whether the secrets exposed earlier were actually rotated is a fact about each
  deployment, not this code: confirm it per server.
- **Deferred.** Refusing a CLI launch when Landlock is requested but unavailable (`cli_landlock.go`
  falls back to the older tool-restricted mode). The product is not installed on hosts without Landlock.
- **Open.** The generated-HTML iframes (`HtmlRenderer.tsx`, `HtmlWidgetFrame.tsx`) run with
  `allow-scripts allow-same-origin`, so a malicious report could read the viewer's session. Removing
  `allow-same-origin` needs a message-passing replacement for the report frame's direct DOM access.

### 2026-10-01 — Chat activity belongs beside the current agent turn
- Move the composer loading indicator to the shared conversation's agent header,
  covering Code, Crew and workflow chats. A pending turn gets a header before its
  first text/tool event; streamed text and tool work then share that header.
  Earlier turns keep their recorded duration and never animate for a later turn.
- Reuse the activity monitor's session classification with immediate tab-local
  start/completion signals. Running/background work spins, input waiting shows
  amber, and settled or idle retained CLIs show no activity indicator. Status is
  scoped to the displayed chat and the lifecycle refresh also works without a
  composer in read-only run views. Provider usage and stop/steer controls remain
  in the composer.
- Keep the animation isolated from token updates and honor reduced motion.
  Code: `useChatRuntimeActivity`, `chatRuntimeActivity`, and
  `TerminalEventTranscript`. Tests cover pending/streaming/tool-only turns,
  completion/error/cancel, background work, waiting and chat switches.

### 2026-10-01 — Project prompts use resolved host paths and honor listed folder grants
- The project workspace map now resolves manifest-relative paths against the
  configured docs root before describing them as absolute. Existing absolute
  paths stay unchanged. This applies to Code and Crew through their shared
  workspace section.
- Code's shared-server rules explicitly allow the signed-in user's authorized
  history reads and listed attached-folder access. Attached writes still require
  read_write access and guarded tools; unlisted server folders and other users'
  projects remain forbidden. This aligns the wording with existing authorization,
  without adding a filesystem grant. Code: `prompt_sections.go`.

### 2026-10-01 — Slot launches: git ownership check off, repository hooks off; paste and key hygiene

- **Decided.** Every command and CLI run as a user's own Linux account (slot) gets `safe.directory=*`
  (the user's folders belong to the platform account with the slot's group, so git would refuse them as
  "dubious ownership", and Muse could not resolve its project root). That removes git's guard against
  a repository whose own config runs code, so the same environment also sets `core.hooksPath=/dev/null`
  and `core.fsmonitor=false`: a sharer cannot plant a hook that runs as another user's slot. Other
  repository-config code paths (for example a configured pager) remain; an agent reading an attacker's
  files is exposed to that already. Code: `workspace/slots.GitSlotEnv` (shell tool) and
  `internal/clisandbox/landlock.go` (CLIs, provider).
- **Decided.** `slottmux` keeps paste content only in a 0600 file under the slot registry and loads it
  into a tmux server when the paste names the session, so a slot's paste is never copied into the shared
  default server. Files are removed by the deleting paste and swept after an hour.
- **Decided.** The Codex API-key login the platform saves (`auth.json`, apikey mode) is removed when no
  key is configured any more; a browser login is left alone.
- **Decided.** `server add-user` re-reads the directory after saving and redoes the add when another
  write replaced the file (no lock is shared with the running server, which also writes it).
- **Fixed (same day).** The tmux front-end routed any session in a user's folder to that user's slot, but
  only users with CLI-as-slot (`AGENTWORKS_SLOT_CLI_USERS`) have launch scripts the slot can read, so
  Muse (and Codex) died at start for everyone else ("cannot open mlp-coding-agent-launch-*.sh:
  Permission denied"). It now routes to a slot only when the command's script is in that slot's run
  folder; otherwise the session stays on the platform's tmux. A Muse session that is not a slot's
  keeps its launch error output under `<run root>/.sessions/launch-logs/`.
- **Fixed (same day).** The front-end's session records ("session X lives in slot Y") could go stale: a
  session started on the platform's tmux after a misrouted one left the old record, so `has-session`
  went to the empty slot server and the platform then failed with "duplicate session". A record is
  now dropped when its slot has no running tmux server, and a non-slot new-session clears any record
  of that name.
- **Open.** Muse still prints "local session messaging unavailable: registry root: Permission denied" in
  slot sessions (it tries to `chmod 0700` platform-owned folders). Harmless, not traced.


### 2026-10-01 — Code is always private; owner-authorized function calls do not share files
- User decision: remove human Code sharing. Normal files, links, chats, Git, bots,
  credentials and runtime access require the Code owner. Viewer/editor/co-owner
  grants in legacy `config/code-shares.json` are ignored; that file is retained
  and protected, never rewritten or deleted as a migration. Share controls and
  client APIs are removed; old GET/PUT share URLs return 410, and the Code shared
  directory returns an empty list. Code's audited read-only admin/reviewer
  inspection remains separate from normal access and cannot run or edit Code.
- A Code can call another Code it owns. The actual Code owner may also call
  explicitly declared Code functions from a Crew they own or a workflow whose
  ownership explicitly includes them, using `#code:<id>` with the existing
  function tools. Shared Crew readers and workflow readers/editors cannot inherit
  the resource owner's private Code access. No implicit Code `ask`, public Code
  function catalog, file attachment or filesystem grant is introduced. Declare
  and remove Code functions from the owner's Code chat, not from a Crew/workflow.
- Hidden internal bindings record the source path and stamp. Discovery, connection,
  dispatch, result/pending-input access and queued execution re-read source
  ownership; target execution and history stay under the Code owner's identity.
  Old Code-to-Code bindings still resolve their source ID only in that owner's
  tree. Existing owner-created authenticated webhooks and schedules remain intact.
- This fits per-user Linux accounts: there is no cross-user Code folder or Code
  credentials to mount. It does not complete the separate Crew/workflow sharing
  integration for slots. Main implementation: `code_shares.go`,
  `code_peer_functions.go`, `product_webhooks.go`, `crew_functions.go` and the
  shared Work/Code UI. Regression tests cover stale grants, typed Crew/workflow
  calls, foreign sources, shared actors and ownership loss before a queued call.

### 2026-10-01 — Per-user Linux accounts ("slots"): shell tool and provisioning, off by default
- New package `workspace/slots` and launcher `workspace/cmd/slotctl`. With `AGENTWORKS_SLOTS=on`
  the workspace service runs each folder-guarded shell command as the caller's own Linux account
  through `sudo` and `slotctl` (after the switch the Landlock policy and namespaces are created),
  and refuses commands that carry no folder guard or come from a user without a slot. A root-owned
  allow-list limits what `slotctl` will start. Off by default, so other deployments are unchanged.
- `deploy/common/provision-slots.sh` (run as root on the host) creates the accounts, the
  sudoers rule and the slot table, and assigns a person to a slot; signing in never does. `slotctl`
  is built into every rootless release; installing it root-owned is the script's job.
- Done in this change: the shell tool. Not done yet: CLI launches and their terminals, ownership of
  a user's runtime folders, the Crew and workflow sharing model. Not deployed or enabled anywhere.

### 2026-10-01 — Accounts are added by an administrator only; invitations and automatic sign-up are gone
- Removed the invitation email (`POST /api/admin/users/{id}/invite`, the Supabase invite call, the
  `invite` option on create, the Resend and Copy-invitation controls, `USER_INVITE_EMAILS`).
  Adding a person in the Users panel still creates their account (role, products); the panel now
  just says to ask them to sign in with Google using that address.
- A sign-in never creates an account any more. `externalAuthIdentityApproved` admits only an
  address already in the user directory, or one named in `ADMIN_USERS` so the first administrator
  can bootstrap their own record. `ensureDirectoryUserForExternal` creates a record only for such a
  configured administrator; the OAuth callback answers 403 "has not been added by an administrator"
  to anyone else. `AUTH_ALLOWED_EMAILS` no longer admits anybody (it used to, and the first sign-in
  then created the account).
- Why: per-user accounts ("slots", private plan) need an administrator-provisioned user before
  anyone can sign in. Existing users already have records and are unaffected. Anyone who was only
  in `AUTH_ALLOWED_EMAILS` and never signed in must now be added by an administrator.
- Left: `SUPABASE_SERVICE_ROLE_KEY` on excellence was only needed for the invite email and can be
  dropped from its `.env` later; the password and bot-route sign-in paths are unchanged. Not deployed.

### 2026-10-01 — Keep prompt contracts upfront and load procedures through skills
- System prompts retain role, access/mode limits, live workspace/grants, secret
  safety, discovery and core memory rules. Skills own operating procedures,
  examples, formats and troubleshooting; tool schemas own argument shapes.
  Skill descriptions retain explicit action triggers. No authorization is added.
- AgentWorks owns mode-specific `project-memory` procedures, Crew Builder
  history/coding guidance and Workflow chat operations references. Run gets
  retrieval/execution guidance without authoring procedures. `MEMORY.md` remains
  the only facts store; the managed memory skill is a procedure, not another store.
  Current secret names remain live context; provider details load from the
  admitted capability tool.
- mcpagent owns the on-demand `runtime-http-tools` skill for progressive CLI/code
  execution. The runtime block owns discovery; redundant available-tools
  reminders are removed. Legacy inline mechanics and that skill share one source. Native API
  schemas remain intact; Agy retains its skill-list fallback. The linked CLI may
  `cd project`; bridge shell calls use absolute paths.
- Readable Code snapshots drive the real query handler through finalized agent
  assembly and inspect its outbound composer before any model turn. External
  state is mocked; runtime policy and tool registration are production paths.
  This replaces reconstruction that missed Code's resolved native-tool mode.
  Capture also found a browser pointer to unattached builder-reference; Code/
  Crew chat pointers now name their attached agent-browser skill.
- Controlled server fixtures reduce Code by 21%, Crew Builder by 40% and workflow
  chats by roughly 39–40%. Crew Run was already compact and adds about 500 bytes
  of shared memory constraints. Live first use and changed-skill native resume
  pass on Claude and Codex. Pi currently lacks local Google auth; Cursor/Muse/Agy,
  complete business flows, total turn cost and latency remain unqualified.
  Details: [prompt discovery design](design/progressive_prompt_discovery.md).
  Existing unrelated suite failures remain open. No restart or deployment.

### 2026-10-01 — Audit-log spawned provider-child env names (disable via LOG_CHILD_ENV=0)
- `providerConnectionSetupEnvironment` and `workflowProviderSetupEnvironment`
  now emit `[CHILD_ENV]` lines showing which variable names a spawned child
  keeps, strips and injects, plus the full sorted name list. Names only,
  never values; a test pins that secret values cannot appear in the output.
- On by default so the fail-open denylist surface stays visible; set
  `LOG_CHILD_ENV=0` (or false/off/no) to disable. Lives in
  `agent_go/cmd/server/child_env_log.go`; temporary observability until the
  builders move to an allowlist.

### 2026-10-01 — A skill with an invalid YAML header stopped the server at startup
- `e05c52f23` made `RegisterEmbeddedSkillsRendered` parse every skill header as YAML, and
  `server.go` stops the process on any error. The Video Studio `google-ai` skill had an unquoted
  `: ` inside its description, so the agent exited at start (excellence, 2026-10-01, status 1,
  502 for about 30 minutes) because AGENT_PRODUCTS was unset there and every product registers.
  Every deploy of that `main` would have failed the same way.
- Quoted the description, and added `TestEverySkillFrontmatterIsValidYAML` which checks every
  `SKILL.md` in the repo, so the build fails before a deploy can. Code:
  `agent_go/pkg/agentprofiles/skill_frontmatter_test.go`. Not done: making a bad skill non-fatal.

### 2026-10-01 — Use live tool discovery and one owner for runtime guidance
- AgentWorks owns product prompts, access/mode constraints and feature skills;
  mcpagent owns dynamic tool discovery, filtering, schemas and provider routing.
  Caller instructions are preserved. AgentWorks code-execution wrappers opt into
  bounded `search_tools` discovery instead of the full upfront HTTP catalog;
  native API schemas and other library consumers' legacy inventory remain.
- Keep essential Code/Crew constraints upfront and render product-option
  procedures into session-local skills. Use rendered skill frontmatter for the
  shared feature bundle's descriptions. Agy gets routing and a skill-list
  fallback until its adapter supports native skill projection. Workflow variable
  and output rules remain AgentWorks-owned; generic HTTP guidance is mcpagent-owned.
- HTTP discovery/schema callbacks now check the current session allowlist as well
  as in-process turn policy, including cached schemas and error suggestions.
  No access is granted by a skill or discovery response.
- Same Code feature fixture shrank from 5,487 to 1,460 bytes. Live first use and
  native resume with changed tools/skills passed on Claude, Codex and Pi; Cursor
  reached its usage limit. Muse/Agy live discovery and total turn cost remain
  unqualified. Existing mcpagent cleanup/API-snapshot and workflow model/path/Agy
  gate test failures were reproduced on baseline worktrees and left open.
  Details and controlled size evidence:
  [progressive discovery design](design/progressive_prompt_discovery.md).
  No application restart or deployment.

### 2026-10-01 — Prompt reduction needs discovery and clear instruction ownership
- Investigated Code, Crew Builder/Run, workflow chat/step composition, mcpagent's
  registry/schema discovery, and native skill projection in owned worktrees.
  Skill bodies already load on demand; feature summaries and product/runtime
  procedures still overlap. The local `get_api_spec` requires an exact tool
  name, so removing its always-loaded name catalog first would break discovery.
- Record the recommended design before changing runtime behavior: canonical
  skill descriptions, one owner for transport instructions, essential mode and
  access constraints retained once, and session-authorized tool search before
  the full catalog is removed. This commit is investigation only.
- Open findings: skill frontmatter triggers are replaced by Go descriptions;
  Code connection timing and workflow hybrid-read guidance disagree across
  documents; Agy skill projection/routing delivery needs verification; existing
  option-specific restrictions must survive any move from summaries into skills.
  Evidence, scope and qualification plan:
  [progressive discovery design](design/progressive_prompt_discovery.md).

### 2026-09-30 — AUTH_SECRET rotation is a scriptable offline command
- New `server rotate-auth-secret`: re-encrypts `config/provider-api-keys.json`
  plus every workflow secrets and provider-credentials document from the old
  secret to the new one, then upserts the deployment's env file. Two-phase
  all-or-nothing (decrypt everything first; any undecryptable blob aborts
  before anything is written), timestamped 0600 backups, decrypt-with-new
  verification, `--dry-run` for pre-flight, non-zero exit on any failure so
  deploy scripts can gate the restart. Run it with agent, workspace, and
  gateway stopped; sessions invalidate on restart (stateless JWT).
- The older `rotate-provider-keys-auth-secret` keeps working unchanged and
  now shares the provider-file rekey helper. Env paths per deploy:
  rootless `$REMOTE_APP/.env`, AWS `/opt/video-studio/.env`, dominion
  `/srv/dominion/.env` (all via `--env-file`). Gmail `credentials.enc` is
  not AUTH_SECRET-derived and stays out of scope.
- Tests: `auth_rotate_cmd_test.go` (end-to-end rekey, dry-run and wrong-old
  write nothing, missing provider file tolerated).

### 2026-09-30 — Folder-guard write paths are boundary-checked before creation
- The shell handler pre-created every `FolderGuard.WritePaths` entry with a
  bare `MkdirAll`, resolving relative entries against the workspace root but
  accepting absolute paths as-is: an absolute outside path, a `..` escape, or
  a symlink redirect created directories anywhere as the service account.
  Each entry is now resolved exactly as the isolator resolves it and checked
  with the working-directory containment helper; escapes fail the request
  with 400 before anything is created.
- Scope note: `/api/execute` is token-gated and proxy-refused, so only the
  trusted agent server sends these configs (FolderGuard is never model-set);
  this closes a confused-harness hole, not a remote one. A check-to-create
  swap race remains in principle; accepted as residual (same account).
- Tests: `shell_guard_writepath_test.go` (resolution table plus a 400-and-
  nothing-created handler case).

### 2026-09-30 — Spawned children get a minimal explicit environment
- Every bare spawn site in `agent_go/cmd/server` (nil `cmd.Env`, which inherits
  everything, or a raw `os.Environ()` assignment) now builds its environment
  from `minimalChildEnv` (`child_env.go`): PATH/HOME/TMPDIR, locale, TERM and
  other non-secret vars carried over only when set. Sites that provably need
  more pass it explicitly: tmux commands keep `TMUX_TMPDIR` for socket
  discovery, CLI status/model probes keep that CLI's documented API key var,
  the provider pty keeps TERM/COLORTERM.
- Deliberate constructions are untouched: explicit caller envs (including the
  workflow credential injection in `workflowProviderSetupEnvironment`), the
  per-account env in `providerConnectionSetupEnvironment`, the trigger notify
  allowlist, and the locked-down git env. The two denylist-based builders
  remain fail-open by design and are follow-up work, as is the mcpagent-side
  tmux session creator, which this repo does not contain.
- Tests: `child_env_test.go` pins the base/passthrough behavior. Full
  `cmd/server` suite shows only 6 pre-existing failures, byte-identical with
  and without this change (verified against the pristine base with the same
  dependency worktrees): sales-crew catalog, delegation-tier defaults,
  playbook catalog x2, a real-tmux keystroke-timing assertion, and workshop
  LLM defaults. Left open; none touch process spawning.

### 2026-09-30 — AUTH_SECRET is cached at startup and cleared from the environment
- The server read `AUTH_SECRET` from the environment on every call, so the
  secret stayed in the environment of every child process (agent shells, CLIs,
  tmux). It is now read once at startup into memory (`InitAuthSecretCache`,
  `services.CacheSecretsKey`) and cleared (`ClearAuthSecretFromEnv`); readers
  use `GetAuthSecret`, which prefers a live env var (tests, CLI commands) and
  falls back to the cache. Same shape as the existing bridge-token handling.
- The workspace service never reads the secret and now unsets it at startup;
  the native shell denylist also blocks it, so shells stay clean even if a
  future path re-exports it. Docker shells were already allowlisted.
- Spawn sites that pass the full parent env through (`os.Environ`/nil-`Env`
  execs) are unchanged by this commit; they inherit a clean environment now,
  and minimal per-site envs remain follow-up work.
- Code: `agent_go/cmd/server/auth_middleware.go`, `server.go`,
  `services/workspace_config.go`, `workspace/server.go`,
  `workspace/security/environment.go`. Tests: `auth_cache_test.go`,
  `workspace_config_key_test.go`, `environment_bridge_env_test.go`.

### 2026-09-30 — Workspace ZIP backup export/import removed
- Removed `POST /api/workspace/export` and `POST /api/workspace/import`
  (`workspace/handlers/backup.go` deleted, routes dropped from
  `workspace/server.go`) after a security review found the extraction loop
  unsafe to keep; the private audit holds the details, not this log. The proxy
  bulk-route entries, the `local_zip` supported strategy, and every UI caller
  (Files tree menu, Backup popup Download ZIP, shared-folder Import) went with
  it. The public share-link folder download is a separate endpoint and stays.
  A router test pins both routes as 404 so a reintroduction fails loudly.
- To restore the capability, reimplement export/import with separator-aware
  containment, symlink resolution before create, and size/count caps, plus
  regression tests — do not revert this commit as-is.
- Code: `workspace/server.go`, `workspace/workspace_backup_removed_test.go`,
  `agent_go/cmd/server/workspace_proxy_policy.go`, `workflow_backup.go`,
  `frontend/src/services/api.ts`, `Workspace.tsx`, `PlannerFileList.tsx`,
  `WorkflowBackupView.tsx`, `BackupPopupBody.tsx`, `SharedFolder.tsx`
  (`ImportProgressDialog.tsx` deleted). Committed bundles under
  `agent_go/static/` refresh on the next frontend build/deploy.

### 2026-09-30 — Process-killing routes: browser ids checked, and admin-only through the proxy
- Audit of the other workspace routes a logged-in user reaches through `/api/wp`:
  `POST /api/browser/cleanup` ran `kill -9` on any process id in the body, so any user
  could stop the agent, gateway or workspace service (all one account) or another user's
  work; `{"all": true}` kills every user's chromium. `POST /api/processes/cleanup` sweeps
  workflow processes server-wide.
- The handler now kills only ids that are in the current browser-process list
  (`filterBrowserPIDs`, `workspace/handlers/browser_processes.go`). The proxy makes
  `api/browser/cleanup` and `api/processes/cleanup` admin-only (`workspaceProxyAdminOnlyRoutes`);
  on a single-user machine everyone counts as an admin, so the top-bar runtime-health control
  keeps working there. In multi-user mode an ordinary user's click on cleanup now gets a 403;
  hiding those buttons for non-admins is not done.
- Not changed, noted: `GET /api/browser/processes` and `GET /api/processes` list server
  processes to every user; `GET /api/cdp-check` probes local ports; `POST /api/skills/cli/install`
  runs `npx skills add <source>` by design. Not deployed.

### 2026-09-30 — Browsers can no longer call the shell-execute route through the proxy
- Found from a pasted Slack message: a logged-in user could `POST /api/wp/api/execute`
  with only `{"command": …}`. The workspace service runs a command with no `folder_guard`
  unconfined from the workspace root (`workspace/handlers/shell.go`, the "non-isolated"
  branch), the proxy adds the service token itself, and the proxy only inspects path fields,
  not command text. Any user could therefore run commands as the shared server account and
  read other users' chats and files, or delete them. Logged: his calls returned 200 at
  16:35-16:36 on excellence. Whether anyone used it to read or delete anything is not known.
- `api/execute` is now in `workspaceProxyRefusedRoutes` (server-only). The UI never called
  it and the agent server reaches the workspace service directly. The test that allowed
  shell text through the proxy now asserts it is refused. Code:
  `agent_go/cmd/server/workspace_proxy.go`.
- Still open: the workspace service itself runs an unguarded command when a request has no
  `folder_guard`; only the proxy stands in front of it. Other routes that accept a command
  were not audited. Not deployed.

### 2026-09-30 — Each rootless release keeps the source it was built from
- `deploy/rootless-linux/build-and-activate.sh` copies the three repos into
  `<release>/source/<repo>` (no `.git`, `node_modules` or `dist`) right after writing
  `SOURCE_REVISIONS`. Confida, SparkQuill and excellence get it; RTS uses
  `deploy/aws-ec2/` and is unchanged. Requested by the owner after the excellence
  release folders were deleted on 2026-09-30.
- It lives inside the release, so it is pruned with it and is **not** a backup: a wipe of
  `/srv/<product>` removes it too (only `data` and `state` survived). It is owned by the
  shared service account, so any process running as that account can read, change or
  delete it, and confined CLIs get no grant to it. Measured on this checkout: about
  380 MB per release before a server clone's smaller tracked-only tree.
- Not deployed yet.

### 2026-09-30 — Local linked-runtime qualification is partial; step searches name `output`
- Real local CLI fixtures passed artifact discovery, linked reads, authoritative
  writes through admitted tools, native resume and cleanup for Claude, Codex,
  Cursor and Pi. Claude and Cursor native writes were denied by existing policy;
  their bridge writes passed. Pi required its existing provider credential in
  the managed process. Muse completed linked artifact work but resume exceeded the 150-second budget;
  Agy stopped at sign-in. Do not call all six providers qualified.
- Add explicit `output` search-path guidance to mcpagent's step instructions,
  matching the `project` guidance: search from the private cwd can skip links.
  Keep tool modes and permissions unchanged. macOS checks are not Landlock
  certification or a production deployment gate; both-mode Linux qualification
  remains required. Evidence and reproduction: [local report](design/local_linked_runtime_qualification.md).


### 2026-09-30 — Linked runtimes tell the agent to name `project` when searching
- The `project/` link is a symlink, and search tools do not walk into a symlink they
  find: plain `rg` from the runtime folder, and Claude Code's Grep and Glob with no path,
  returned no workflow files; with `project` as the path (or `rg -L`) they did. A
  `RIPGREP_CONFIG_PATH` with `--follow` fixed `rg` but not Claude Code, so it is not a fix.
- The Crew and workflow runtime instructions and the workflow shared prompt now say to
  always pass `project` (or `project/<folder>`) as the search path. Per-folder links would
  not help (ripgrep skips those too). The real fix, if the prompt proves unreliable, is a
  bind mount that makes `project/` a real directory: the launcher would need its own
  namespace plus a host AppArmor allowance, and macOS keeps the link. Cursor, Codex, Muse,
  Pi and Agy were not tested; each needs the same live check.

### 2026-09-30 — Workflow steps link their own iteration output into private runtimes
- Extend the linked-runtime design to execution steps and step orchestrators.
  Keep the existing per-session CLI directory identity and isolated generated
  instructions/skills/configuration. Add `output/` pointing to the exact current
  step artifact directory, including group, nested Agent and message-sequence
  output overrides. Files written through it are authoritative artifacts with
  no copying or synchronization. API models and review/learning agents do not
  acquire an output link merely by inheriting shell environment.
- The dedicated step session's final folder guard admits the link target.
  Resolve existing ancestors before creating missing output directories; reject
  mismatched or unauthorized targets, including symlink escapes. Replace inherited
  chat workspace grants with that step's existing grants for native CLI security,
  retaining its admitted DB/cache/KB/owning-subtree/host capabilities. Keep the
  CLI home inside the step runtime. Links do not enable native write tools or
  change the existing tool modes or broader sandbox limitations.
- Same-session resume keeps cwd and link stable. An obstructing file/directory,
  wrong target, unavailable runtime or cross-iteration rebinding fails rather
  than overwriting content or falling back to the real workflow cwd. Session
  cleanup removes the private link, never its output target. Closing an isolated
  turn no longer attempts projection cleanup in its real bridge working directory.
- Code: `pkg/orchestrator/coding_agent_output_runtime.go`, step factories and
  mcpagent `WorkspaceRuntimeConfig.OutputDir` / `agent/isolated_output.go`.
  Tests cover all six providers including Agy, resume and cleanup, iteration/group
  separation, target failures and policy narrowing; a Linux Landlock test verifies
  direct atomic output writes and denial of sibling/other-iteration mutations.
  Not deployed; live CLI qualification remains required before deployment.

### 2026-09-30 — No public default secrets key left in the decrypt path
- `services/workspace_config.go` still derived its decryption key from the public
  `dev-secret-change-in-production` when `AUTH_SECRET` was empty. The server already
  refuses to start with that value, so it was dead-but-confusing. `deriveSecretsKey` now
  returns nil without `AUTH_SECRET` and decryption fails with a clear error; the value is
  trimmed like the server's own reader. No real secret was ever in the code (`env.example`
  holds a placeholder).

### 2026-09-30 — Workflow Run and Builder also link the real workflow into private runtimes
- Apply Crew's linked-directory design to workflow conversational CLI runtimes.
  Keep the existing user/workflow/chat/provider/mode identity and distinct Run/
  Builder prompts and skill bundles. Add `project/` pointing to the authoritative
  workflow folder; native tools use that prefix or `cd project`, while bridge
  paths remain workflow-relative. Prompts, skills, configs and private CLI homes
  stay outside workflow documents. The earlier shared-folder plan is superseded.
- Keep the existing private directory digest, so adding the link does not move
  a saved session or discard compatible native resume. The updated shared prompt
  carries the path contract in both mode definition fingerprints, ensuring an
  old retained CLI reloads instructions. Mode/chat/provider boundaries and
  Codex's project-directory check remain enforced. Restored workflow terminals
  defer to fresh query admission even during rollback instead of attaching a
  saved pane with potentially stale access and working-directory permissions.
- Read-only workflow turns no longer retain the initial real-folder write grant
  in CLI security or inherit folder-guard workspace write grants in the final
  Landlock policy. The CLI can write its private runtime and read granted data;
  authorized workflow execution still writes its results through the backend.
  Builder retains its authorized project/folder writes. Existing blocked-path
  and host-grant limitations are not resolved by a symlink.
- `AGENTWORKS_ISOLATE_WORKFLOW_CLI=false` remains a transitional rollback for
  writable Builder turns. Run always uses a private linked cwd, even with that
  flag, because the launcher grants cwd writes automatically. Link/state errors
  fail launch instead of falling back to the real workflow folder. API models
  and workflow step agents keep their existing working-directory behavior.
- Code: `workflow_cli_isolation.go`, `cli_landlock.go`, `server.go`, and
  `internal/agentworksproduct/prompts/workflow-shared.md`. Tests cover all six
  providers (including Agy), mode contracts, existing-session resume, link
  failures and final read/write policies. Linux launcher tests exercise the
  linked-file boundary. Not deployed; authenticated CLI qualification in both
  modes remains required before deployment.

### 2026-09-30 — Crew Run and Builder use private runtimes linked to the real project
- Reverse the shared Crew prompt/folder decision in PLAT-371. Every Crew coding
  CLI uses a private directory keyed by user, project, chat, provider and mode.
  Its `project/` directory link points to the authoritative Crew files; generated
  instructions, projected skills, CLI configuration and private homes stay beside
  the link. Directory linking preserves new files, atomic saves, renames and
  deletes without copying or synchronizing project data. Code and workflows keep
  their existing working-directory policies.
- Access selects the mode: owners use Builder; readers, guest function calls and
  downgrade-only pinned turns use Run. Run has a separate prompt and `crew-run`
  skill, without the built-in authoring bundle or feature/setup/memory-write
  instructions. Builder retains its feature bundle and adds `crew-builder`.
  The per-message reader notice, denied tools, folder guards and terminal access
  checks still enforce the current turn's restrictions.
- The link grants no permission. Landlock grants the real project separately:
  read/write for Builder, read-only for Run. A reader's final CLI policy drops
  initial/attached-folder workspace write grants and allows writing only the
  private runtime. Linux launcher tests verify read, create, edit, rename, delete
  and denial of a nested link to an ungranted folder. This does not resolve the
  existing confinement/host-grant issues listed below.
- Resume requires the same private directory (including Codex's project-directory
  override). Old native sessions launched in the shared Crew folder start fresh
  once, using saved application history; same-mode private sessions resume across
  restarts and day-folder rollover even after a compatible definition refresh.
- Code: `crew_cli_runtime.go`, `pkg/cliruntime/project.go`, `cli_landlock.go`,
  `internal/workproduct/prompts/run.md`, and mode skills. Provider skill projection
  is tested for Claude, Codex, Cursor, Pi and Muse; all six Crew CLI runtime
  identities are tested. Agy uses launch-context prompts and the bridge skill
  reader; its private-home, tool-hook and prompt-input tests pass too.
  Not deployed: authenticated live CLI qualification in
  both modes remains required by the deployment decision below.

### 2026-09-30 — Chat jumpiness: no clear during a running turn, no doubled reply at the end
- The live streamed text auto-cleared after 60s of silence (the code comment said 3s; it
  never was) even while a long tool call was still running, so the reply vanished and
  jumped back. It now re-arms while a tab of that session is streaming
  (`armStreamingInactivityClear`, `useChatStore.ts`).
- The finished reply is added as a normal row while the live row stays up to 500ms, so a
  long answer showed twice and then collapsed. The live row is dropped as soon as the
  finished reply says the same thing (`liveTextAlreadyCommitted`,
  `TerminalEventTranscript.tsx`). Found by reading the code, not reproduced live.

### 2026-09-30 — Agy tool calls show while the turn runs, not only at the end
- Agy has no live tool stream; its calls are read from the conversation database.
  They were published only once the whole turn settled, so a 4-minute Confida turn
  showed no tools until the end. `ReadRetainedTurnStructuredProgressMessages` now
  also publishes calls a later step follows (`agyCompletedToolCallsSince`): tools
  run one after another, so those have finished. The newest step waits for the next
  poll. Code: provider repo `pkg/adapters/agycli`. Not verified live yet.

### 2026-09-30 — Costs shows prompt character counts only
- Replace the saved prompt text preview with character counts for the latest
  saved system and developer instructions, as requested. The Costs history GET
  opts in with `include_saved_prompt_sizes=1`; it returns size metadata without
  prompt text. The previous prompt-text opt-in is removed.
- Count the full saved instructions as Unicode code points, including leading
  and trailing whitespace; counts are not byte lengths or truncated previews.
  This measures the latest saved version, not a per-run prompt archive. Missing
  prompts remain explicit. Existing conversation access checks still apply.
- Reverses the prompt-text display below. Code: `chat_history_routes.go` and
  `frontend/src/components/providers/CostConversations.tsx`.

### 2026-09-30 — Switching provider mid-chat no longer inherits the old provider's account
- Confida: Pi -> Agy in one chat failed 403 "provider connection does not match
  selected provider" (a hard refresh cleared it). `queryRequestForAgentProfileChat`
  filled a missing account from the conversation's saved one, which belongs to the
  old provider. It is now inherited only while the provider is unchanged; the new
  provider gets its default account. An explicit account is always kept.

### 2026-09-30 — The raw terminal drops keys that close the agent; a closed terminal says how to resume
- Ctrl-C (exits on a second press), Ctrl-D, Ctrl-\\ and Ctrl-Z typed in the raw
  terminal are dropped (`stripCLIExitKeys`, `terminal_live_attach.go`) with a
  one-line note. Esc still interrupts and the chat has a Stop button. Bracketed
  pastes pass whole. tmux prefix keys were never at risk: input goes to the pane
  by `send-keys -H`.
- After the 1h idle reaper closes a terminal the view is read-only. The restore
  endpoint deliberately does not relaunch (tool-registration race); the next chat
  message does. `MainAgentTerminal` now says so with a "Back to chat to resume" button.
- Muse under the lock could not write its endpoint lease (`<data>/muse/runtime`);
  granted write on that folder only (`musecli_landlock.go`).

### 2026-09-30 — Costs chat previews expose the latest saved instructions
- View chat in Costs has a collapsed System prompt section showing the latest
  saved system and developer message separately from the paginated chat turns.
  The authorized history GET opts in with `include_saved_prompts=1` and reads
  the canonical archive instead of a resume snapshot that excludes prompts.
- These are saved instructions, not regenerated prompts or an exact request
  archive: native continuation can replace previous system messages, and
  provider internal instructions are unavailable unless the transcript stores
  them. Missing prompts are explicit. Each role has a 64 KiB UTF-8-safe preview
  with truncation labelled; ordinary chat restore remains compact.
- Existing conversation access checks apply before extraction. No agent starts
  or deployment is performed. Code: `chat_history_routes.go`,
  `frontend/src/components/providers/CostConversations.tsx`.

### 2026-09-30 — People can disconnect their own legacy Gmail account
- The shared-account admin gate also blocked removal of personal connections
  created before owner IDs were recorded. Confida has an ownerless Gmail entry
  whose Google-discovered email matches an enabled, non-admin directory user.
- DELETE now permits the recorded owner, or for an ownerless legacy entry an
  exact email match with the authenticated user's current server-side directory
  record. A recorded owner overrides email; private Code accounts stay strictly
  owner-only. Unknown identities, missing emails and disabled users cannot use
  the legacy fallback. Shared account changes and OAuth client management stay
  admin-only.
- Connection responses include `can_remove`. Both account lists enable removal
  separately from management controls; non-admin removal preserves the shared
  OAuth client registration. Workflow read-only access still disables the UI.
- Code: `gmail_connection_routes.go`, `useWorkflowBots.ts`,
  `GmailNotifications.tsx`, `GoogleAccountList.tsx`. Not deployed: deploys remain
  on hold until the next batch of major fixes.

### 2026-09-30 — Costs links usage to saved conversations
- Providers Costs and each expanded workflow/Crew/Code cost date show fresh
  input, cache reads, cache writes when present, output and the percentage of
  total input served from cache. Total input includes the cache buckets once.
  Headline input cards explicitly say fresh input.
- Conversation aggregates retain actor/workspace/session identity, date,
  execution, model and recorded USD. Providers publishes only conversations
  whose work root passed the existing access filter. Opening chat uses the
  existing authorized, bounded history GET; it does not start an agent.
- Conversation details show recorded turns/agent runs rather than equating
  ledger rows with native model requests. Daily details use that date's subset.
  Old servers/records without conversation attribution keep the totals and
  cannot provide a conversation link.
- Excellence investigation: the two Muse cron conversations recorded
  43,671,464 input (33,867,163 cache reads), 32,226 output, and $1.054609626
  on September 30 UTC. Contributor rates already discounted cache reads:
  $0.9804301 fresh + $0.067734326 cached + $0.0064452 output.
- Open issue: native Muse 1.4.1 changed its developer-context temporary
  sandbox path between scheduled runs. Many first requests reported zero
  cache while later requests in that run reused almost all input. Growing
  history raises input per run. These logs suggest prefix invalidation; the
  provider does not report its cache-miss reason. No sandbox weakening or
  fabricated discount is applied to hide the recorded fresh usage.
- Code: `agent_go/pkg/costledger/ledger.go`, `cost_overview.go`,
  `frontend/src/components/providers/CostConversations.tsx` and
  `CostTokenBreakdown.tsx`.

### 2026-09-30 — Daily cost dates expand independently
- Workflow, Crew and Code cost dialogs keep a set of expanded dates, so
  opening another day preserves already visible details for comparison.
  Collapsing a day affects only that day.
- Code: `frontend/src/components/workflow/costs/useCostsData.ts` and
  `CostsDailySection.tsx`.

### 2026-09-30 — Every ledger cost surface uses normalized input
- Extend the Providers token correction to workflow, Code and Crew cost
  dialogs, activity/execution/phase/model details, daily history and embedded
  report widgets. Input and output replace model-call headline counts there.
- Preserve `input_tokens` and pricing coverage when the client merges scopes
  and executions. SQL all-time workflow totals use the same inclusion flag
  and historical Muse fallback as per-event summaries, so opening a cost
  dialog does not turn normalized input into zero or drop missing-usage data.
- Existing run/phase artifacts already carry input/output separately; their
  USD estimates and immutable raw event/token records are preserved.
- Code: `agent_go/pkg/costledger/sqlite.go`, `frontend/src/utils/costTokens.ts`,
  `frontend/src/utils/costActivityBreakdown.ts`, workflow `costs` components
  and `reportWidgets/reportOperationalMetrics.ts`.

### 2026-09-30 — Costs shows input and output, with cache counted once
- The dedicated Costs summary and its user/work/project/account breakdowns
  show input tokens, output tokens and cached input. Cache is part of input;
  model-call counts remain accounting data rather than headline metrics.
- Ledger aggregates expose canonical `input_tokens`. New observer entries
  retain the provider's `prompt_tokens_include_cache` flag; existing Muse
  entries are inclusive. Raw token fields and recorded USD remain unchanged.
  Other historical entries retain the legacy prompt-plus-cache calculation
  because the original inclusion flag was not stored and cannot be recovered safely.
- The Code tab groups Code workspaces separately from other products.
  MCP service breakdowns show the recorded actor, email and tool-call count
  after work access filtering; absent actor IDs remain unattributed.
- Missing token/cost reports are distinguished from usage without a model
  rate. `missing_usage_call_count` is a subset of unpriced calls; neither
  category contributes an invented zero-dollar estimate.
- Visible project rows include their owner's directory email, when available,
  in the list, summary and search. Ownership is distinct from contributors in
  the existing per-user breakdown; access filtering still precedes lookup.
- Excellence's September 28–30 Muse ledger had $3.78333333 in token estimates:
  184,406,599 input, 531,213 output, 150,648,665 cached input. The old UI added
  cache again (335.60M overall tokens). Contributor pricing and cache discounts
  explain the small recorded dollar estimate; it is not an invoice.
- Code: `agent_go/pkg/costledger`, `agent_go/pkg/costobserver`,
  `agent_go/cmd/server/cost_overview.go`, `frontend/src/components/providers`.


### 2026-09-30 — Code agents are told to keep to their own project on the shared server
- A Code project's agent can install and run things on a server that other people's projects
  share. One project's chat installed a browser IDE, exposed it to the internet through a
  forwarder and browsed the server's folders (excellence). A prompt section, `code-host-safety`
  (`prompt_sections.go`, Code product only), now tells the agent to: work inside its working
  folder and never in `~` (a hidden private folder, so files created there are invisible to the
  user); not look at folders or files outside it; not read environment variables or credentials
  it was not given; not install, start or expose remote-access or hosting tools or bind to public
  interfaces (a local dev server on `127.0.0.1` is fine); and not run harmful or unrelated tools.
- This is a prompt-level guard only. It lowers the chance, it does not enforce anything: the lock,
  a default-deny inbound firewall, an environment allowlist for shell commands and resource
  limits are what enforce it, and none of those is done yet (see Open issues).

### 2026-09-30 — Typed slash commands in the browser terminal are limited to an allowlist
- Slash commands change the CLI's own settings or run large commands, both of which the app
  offers itself. The session-switching ones (`/new`, `/clear`, `/resume`, `/fork`) leave the chat
  reading a session the CLI has left, and any slash command leaves a draft the CLI never records
  (Vaibhav, Confida: `/new`, then every chat send refused). So typing them is blocked, except
  `/usage`, which people need.
- The CLI's slash menu can be driven without typing a name ("/", arrow keys, Enter), so
  `terminal_slash_guard.go` follows the line as typed: once a line starts with `/` it forwards
  what is typed, drops arrow keys and Tab, and lets Enter through only for a full allowlisted
  name. Otherwise it drops the Enter, erases the typed line and shows a one-line note.
  Slash text pasted on an empty line is treated the same way.
- The chat's own commands are pasted by the platform on a different path and are unaffected.
- `AGENTWORKS_TERMINAL_SLASH_COMMANDS`: unset = `usage`; a comma list (`usage,status`); `none`;
  `allow` turns the guard off. Other CLIs may name their usage command differently (Codex:
  `/status`); add them to the list per server once checked.
- Limits: the guard only sees the line as typed in the browser terminal. History recall (Up arrow)
  fills the line without it noticing, so a `/` typed after a recall is treated as a command start.
- Also on main, not deployed: a typed terminal draft no longer blocks a chat send.

### 2026-09-30 — A reply to a live message is held until the message row (all CLIs)
- When a message is sent into a running CLI, its chat row is written when the
  CLI confirms it took the message (`watchLiveInputDurableRecording`), and the
  event store holds the reply's rows until then (`steer_ordering.go`) so the
  reply never lands above the question.
- The hold was skipped whenever the session looked "mid-answer". Answer rows
  that arrive after their turn ended (Cursor's late transcript chunks, native
  transcript catch-up) marked it mid-answer with nothing left to end it, so
  Cursor's first reply line to a message landed above that message (RTS
  rts-pr-reviewer: the line looked missing; it was above the message).
- Now a session is mid-answer only if an answer row arrived in the last 15 s
  (`deferredSteerQuietWindow`) or a tool call is still running. Past that it is
  idle and the reply is held. Trade-off: a CLI silent for over 15 s mid-answer
  with no tool running (very long thinking) can still sort a message ahead of
  the rest of that answer. Tune the window if that shows up.
- NOT DEPLOYED yet (on main, e8340db68). Deploys are on hold until the next
  batch of major fixes.

### 2026-09-30 — Every CLI is qualified under the lock in both chat modes before a deploy
- Turning the lock on for everyone exposed that only Claude and Codex had been
  run under it. In one day Muse, Cursor and Pi each failed at start or in the
  middle of a turn. What to check per CLI, on a real server, with the real
  launcher (`video-studio-landlock-runner`) and `strace` when a pane dies:
  1. starts and reaches its prompt; a prompt goes through (hooks, extensions);
  2. resumes a chat that started before the lock (native session adoption);
  3. mid-turn text streams and the finished reply lands (which native store the
     server reads, and in which order);
  4. every per-launch file it writes (markers, sockets, configs) is granted.
- A dead pane's own output is in the agent log ("dead pane output before
  cleanup"); npm's own log is in the private home (`.npm/_logs`).

### 2026-09-30 — Resumed chats adopt their native session into the private home
- A chat started before the lock keeps its native session in the server's or
  account's own home; the confined CLI reads only its private home, so
  `resume <id>` found nothing and the pane died (Muse: "Reading session log",
  then exit). One shared step in `clisandbox.LandlockArgs`
  (`internal/clisandbox/adopt.go`) copies only that session's files, never the
  rest of the home, and keeps a copy that already exists. Muse, Codex and
  Cursor use it; Claude has its own (`claudeAdoptResumedConversation`).
- Cursor chats live under the account's `XDG_CONFIG_HOME` (the server sets one
  on RTS) or `~/.cursor/chats`, and go to `.config/cursor/chats` in the private
  home, where the confined Cursor reads them.

### 2026-09-30 — Cursor reads the confined chat's own store first
- `cursorChatsRoots(home)` puts the given home's `.config/cursor/chats` before
  the server's XDG folder. The other order read a stale copy of the same chat
  (started before the lock) and so nothing streamed mid-turn on RTS; the reply
  only landed at turn end.
- Code: `multi-llm-provider-go/pkg/adapters/cursorcli/cursorcli_paths.go`.

### 2026-09-30 — What each CLI is granted beyond the common rules
- **Muse:** read access to the folder of its `managed_hooks_path` (the Orca hook
  scripts it runs on every prompt); without it every prompt failed with
  "Prompt blocked by hook ... Permission denied".
- **Pi:** write access to its per-launch folder (`launch-pi.sh` and its
  siblings). Pi's injected `mlp-marker.ts` extension appends every event to
  `markers.jsonl` there. The platform reads that log to know a turn has ended
  and that a live message was really received, so the extension is required,
  not optional. Read-only access made Pi exit at start with "Failed to load
  extension ... EACCES ... markers.jsonl".
- **Pi bridge:** read/exec on the bridge program named in Pi's private
  `mcp.json` (`piLandlockReads` reads `PI_CODING_AGENT_DIR` from the launch
  environment). Without it Pi started but its platform tools failed with
  "api-bridge: failed: spawn .../mcpbridge EACCES". The launch folder, the
  extension cache and the bridge were three separate grants; a confined Pi
  needs all three.
- **Pi extension cache:** a confined Pi gets its own `tmp/extensions` folder
  instead of the shared one in the server home (`linkSharedPiExtensionCache`).
  The shared cache is executable code every user's Pi would load, so a writable
  shared copy would let one user plant code in another's; and the confined Pi
  could not write there anyway (npm exit 243). npm reinstalls into the private
  folder in about half a second.
- Rule of thumb: a per-launch file the CLI itself writes needs an explicit
  write grant; a file it only reads needs a read grant. Moving `TMPDIR` off
  `/tmp` removed the accidental grant that made some of these work.

### 2026-09-30 — Terminal mode has no app message composer
- All typing stays in the native CLI; remove the optional composer and its
  expand/collapse control. Keep the saved chat draft for Return to chat.
- Commands use their standalone picker. Attach uploads to the scoped folder
  and pastes absolute file references into the current chat's live tmux input
  without pressing Enter. A failed paste retains the files as chat attachments.
- The upload API returns an additional absolute path so native file references
  remain valid when the CLI's working directory is a project subfolder.
- Code: `frontend/src/components/ChatInput.tsx`,
  `frontend/src/components/NativeTerminalToolbar.tsx`,
  `workspace/handlers/documents.go`.

### 2026-09-30 — Automatic walkthroughs wait for startup to resolve
- Wait for provider onboarding to be cleared and for the initial automation
  manifests to load before opening a tour. Crew and Code use their existing
  project-loading readiness flag. A saved automation initially looks empty
  until its manifest arrives; opening that temporary screen's tour caused a
  flash on reload before the restored screen or Providers replaced it.
- Manual Help & walkthrough remains available during startup, and dismissal
  preferences remain scoped to each screen in browser or Electron storage.
- Code: `frontend/src/components/ModePresetBar.tsx`.

### 2026-09-30 — Coding CLIs run under the Landlock lock for everyone, everywhere
- `AGENTWORKS_CLI_LANDLOCK=on` and `AGENTWORKS_CLI_FULL=on` on RTS, excellence,
  Confida and SparkQuill (service unit / `product.env`). No staged rollout, no
  per-user lists.
- A CLI that fails under the lock is fixed, never exempted (an exemption for
  Muse was built and reverted the same day).
- Code: `agent_go/cmd/server/cli_landlock.go`, launcher
  `workspace/cmd/landlock-runner` + `workspace/security/landlock_runner_linux.go`,
  CLI side `multi-llm-provider-go/internal/clisandbox/landlock.go`.

### 2026-09-30 — What a confined CLI may touch
- Read/write: its working folder, its private home
  (`<workdir>/.sandbox-cache/cli-home/<cli>`), the folders its chat's folder
  guard grants, and explicitly granted runtime files. Only **Cursor** also
  receives the shared `/tmp` grant (tested per provider in
  `internal/clisandbox/landlock_tmp_test.go`).
- `/tmp` is writable for Cursor because it keeps sockets at fixed `/tmp` paths
  (`cursor-askpass-*.sock`, and `/tmp/.cursor/<project>` when its home path is
  too long for a socket). Every other CLI uses its private `TMPDIR`: Muse, Pi and
  Agy were started under the real launcher without `/tmp` and made no `/tmp`
  access; Claude and Codex ran confined on RTS before the grant existed.
  Verified 2026-09-30; a CLI that turns out to need `/tmp` is added by name.
- Muse's private `TMPDIR` was first narrowed by a separate patch (provider
  `3428203`, review PRs provider #39 / builder #258); the Cursor-only rule
  above replaced it and covers Muse the same way. A canary test with the real
  launcher shows another CLI's `/tmp` file can be neither read nor overwritten.
  Live echo startup and native resume pass under this narrower policy; real
  Meta authentication and native-tool calls still need an authenticated smoke
  test. See `docs/bugs/muse_landlock_directory_startup.md` for the regression
  probes and their limits.
- `/` is list-only (file and folder names, no file-content reads): Muse opens every folder from
  `/` down to its workspace at start ("Agent Definition filesystem source
  failed: IoError"). Landlock cannot grant one folder without everything below
  it, so names throughout the host are listable wherever Unix permissions
  permit; this rule does not grant access to file contents.
  Agents are also told in their system prompt to stay in their own folder.
- A private mount namespace per CLI (a minimal `/`, a private `/tmp`) was
  considered and deferred as heavier than needed.

### 2026-09-30 — The server keeps its temp files out of `/tmp`
- The agent service runs with `TMPDIR=<state>/agent-tmp` (created at start in
  `agent_go/cmd/root.go`), so launch scripts (which export secrets), per-launch CLI
  configs (Muse login copy, MCP settings with bridge tokens) and hook scripts are
  not in the `/tmp` that CLIs can read.
- `TMUX_TMPDIR=/tmp` keeps the tmux socket where it was, so running chats stay
  reachable across the deploy.

### 2026-09-30 — CLI logins are linked per account, from where each account keeps them
- The private home gets a link to the account's login file, never a copy
  (refresh tokens rotate). The source follows the account's own
  `XDG_CONFIG_HOME` / `CODEX_HOME` / `CLAUDE_CONFIG_DIR`; for the server account,
  the server process's environment (RTS sets `XDG_CONFIG_HOME`, so its Cursor
  login was never found before). `CLISecurityPolicy.CredentialEnv`.
- On RTS Cursor runs on an API key, so it does not depend on the login file.

### 2026-09-30 — Accounts on Crew and Code
- Your own signed-in account is used by default for your own interactive chats
  only; bots, schedules and auto-notifications keep the project's account.
- Creating a Crew or Code never saves a private account on it (others it is
  shared with could not use it). Workflows save an account only when no shared
  one is usable.
- An explicit account change keeps the same chat and restarts the CLI on the
  new account.
- A denied private account fails the turn with a message saying whose it is and
  how to share it; it never falls back to another account.

### Earlier — One chat per Crew and Code
- Each Crew and Code has one canonical chat; there is no "New chat" button
  (Video Studio keeps its own). Reconsidered 2026-09-30 and kept.

### Earlier — Full CLI without the lock is local-only
- `AGENTWORKS_CLI_FULL_UNCONFINED=on` (on by default in
  `run_server_with_logging.sh`) gives the CLI its own shell and file edits on a
  single-user machine; refused in multi-user mode. macOS Seatbelt is deferred
  (PLAT-364 doc).

## Deploy state (2026-09-30, deploys on hold)

- **Live:** RTS `fa635b8` (Cursor store order); Confida (Pi launch folder,
  extension cache, bridge; Pi confirmed working); excellence (Muse hooks,
  resume adoption).
- **On main, not deployed:** the steer-ordering fix (`e8340db68`).
- Deploys wait for the next batch of major fixes. Deploy from a clean
  worktree; the server clones main of all three repos.

## Open issues

### 2026-10-01 — Model selection is pending while typing directly into a retained terminal
- Excellence/Ashutosh's Code saved `gpt-6.1-sol` in its project manifest, while
  its retained Codex runtime still had `gpt-6-sol`. The model was saved after
  that runtime's last chat turn. Saving is working; model/account reconciliation
  and relaunch happen in the next project chat request, and native terminal
  input bypasses that request path.
- The Models panel says "next message", which can imply terminal input applies
  the change too. Pending-versus-running feedback and native-terminal handling
  remain open; the current way to apply the saved choice is to send the next
  message through the chat. Code: `WorkModelsPanel`, `changeWorkRuntime` in
  `WorkSurface`, and `prepareProductConversationTurn` in `agent_profile_routes.go`.

- **Remaining local linked-runtime CLI qualification (2026-09-30).** Muse's
  integrated resume exceeded the fixture budget after linked artifact work passed; Agy's
  private launch needs sign-in. Claude/Cursor native writes remain subject to
  their existing restrictions. The [local report](design/local_linked_runtime_qualification.md)
  records what actually passed; do not treat bridge writes as native-write proof.


- **Two mcpagent cleanup tests still expect deletion of unmarked provider folders
  (confirmed 2026-09-30).** `TestAppendCodingAgentWorkingDirOptionCleansInactiveGeneratedArtifacts`
  and `TestAppendCodingAgentWorkingDirOptionRemovesInactiveProviderDirsInWorkflow`
  fail unchanged on `origin/main` (`fa6a186`) as well as the linked-output branch.
  Their fixtures/whole-directory deletion expectations conflict with the current
  cleanup safety contract. Correct that contract separately; this task does not
  reintroduce broad deletion. Output, resume, provider-policy and safe-cleanup
  regression tests pass.

- **A Code chat's shell command started processes outside the lock (found 2026-09-30).** They ran
  as the server account with its full environment and reached the internet; shell commands also
  inherit server secrets that are not on the environment denylist (`AUTH_SECRET`,
  `ACCESS_PASSWORD`, `ADMIN_USERS`). Needs: close the way out of the lock, an environment
  allowlist in `buildNativeEnvironment`, a default-deny inbound firewall, per-CLI resource limits,
  and rotation of the exposed secrets. Longer term, a separate Linux account per user.

- **Confinement can be skipped when the launcher is unavailable.**
  `applyCLILandlock` logs and runs the CLI unconfined when `CLILandlockRunner`
  fails its capability check, even with `AGENTWORKS_CLI_LANDLOCK=on`, so
  "confine every CLI" is not fail-closed. Decide: refuse to start the CLI, or
  keep failing open with a loud log.
- **Shared browser paths remain launcher grants.** CLI policies leave
  `BrowserScoped` false, so `landlockSystemWritePaths` grants the shared
  browser socket/temp folders and, when set, shared-profile roots for users,
  workflows and projects. Cursor-only `/tmp` does not remove these.
- **Muse per-launch login copy is not returned.** `musePrepareIsolatedConfig`
  copies `auth.json` into the per-launch config folder and deletes it after the
  launch, so a token Muse refreshes is lost. The "logins are linked, never
  copied" rule above does not cover this Muse path. Likely the next failure
  for Meta-authenticated users.
- **Shared `/tmp` for Cursor.** Cursor's confined sessions can read and write
  `/tmp`: other CLIs' fixed-path temp files, the
  workspace command scratch (`/tmp/aws-<uid>`) and the browser sockets
  (`/tmp/.agent-browser`). Landlock also does not govern `connect()` to
  Unix sockets, so the tmux socket (`/tmp/tmux-<uid>/default`) is reachable
  from a confined CLI whatever its grants. Fixing both needs the private mount
  namespace above.
- **Confida Slack replies about 90 s late.** The gap is before the session is
  saved; `[BOT_TIMING]` and `[BUILDER_RESTORE_TIMING]` logs are in place to
  name the slow step (suspect: `restoreLatestBuilderConversation` reading every
  saved Builder conversation).
- **Deploy check can fail on a stale `claude`.** `deploy/common/install-coding-clis.sh`
  refuses when a CLI resolves outside the managed install. Confida had a stray
  `tools/node/bin/claude` from an interrupted install (removed by hand
  2026-09-30). The check names only the CLI, not the path it found.
- **Muse authenticated smoke test.** Startup and resume pass under the lock;
  a real Meta-authenticated turn with native tools is still to be run.
- **Personal accounts per provider.** The Providers screen says "The
  installation does not allow personal accounts for this provider" when the
  provider is locked to the server's account (RTS Cursor runs on the server's
  API key) and `ALLOW_PERSONAL_PROVIDER_CONNECTIONS` is not set
  (`personalProviderConnectionsLocked`, `provider_connections.go`). It is a
  server setting with no UI switch, and the screen does not say who can change it.
