# Work Product Design

> **Update 2026-09-30:** Crew (Work) chats run in the project folder. A project's own `AGENTS.md`, `.claude/`, `.cursor/` and other CLI files are read, never overwritten or deleted; the session prompt is added as a marked, session-counted block in `AGENTS.md`. A Crew has two roles, owner (Builder) and reader (Run, including guest calls); every session shares one prompt and the reader role is sent as a block in front of each message. See [project instruction files](project_instruction_files.md) and [PLAT-371](../bugs/pulse_platform/security-sandbox/plat-371.md).

## Main Goal

**Work is a secure, multi-user web interface for running coding agents such as Claude Code and Codex directly inside automatically created project folders on managed servers.**

It combines:

- The approachable conversation, attachments, reusable skills, and artifact experience of ChatGPT and Cowork.
- The repository awareness, terminal access, file editing, tools, instruction files, and long-running task behavior of native coding agents such as Claude Code and Codex.
- User-level access controls, limits, and auditability suitable for a shared deployment.

Work is not a workflow builder. Its primary entities are users, servers, projects, and agent sessions—not workflows, plans, phases, or steps.

## Core Model

`User -> permitted server -> project folder -> agent session`

- **User:** An authenticated person with assigned permissions, limits, preferences, and private session history.
- **Server:** A managed machine or execution environment with supported coding-agent runtimes installed.
- **Project:** A user-owned folder created automatically under the Work projects root, using the same folder-creation model as AgentWorks workflows but without a workflow manifest.
- **Agent session:** The project's canonical persistent conversation and execution context using a selected runtime, model, project folder, and permitted tools. Older conversations remain available as read-only history; users do not create parallel active chats inside one Crew project.
- **Project agent identity:** An optional compact icon, name, role, and instruction set that keeps the project agent consistent across chat, schedules, bots, and background work. Users set or clear it conversationally; it changes behavior, not access.
- **Attached folder:** An optional administrator-authorized external server folder that a project may access in addition to its own folder.
- **Artifact:** A file, preview, diff, image, or other output produced during a session.

## Primary Experience

1. The user signs in and sees their Work projects on permitted servers.
2. The user clicks **New project**, and Work creates the project folder automatically; no folder selection is required.
3. The user selects an available runtime, such as Claude Code or Codex, when more than one is permitted. Its platform Builder model is selected by default; the user can choose another supported model before or after the first message.
4. The user describes the task conversationally, attaches relevant files, or invokes a skill.
5. The native coding agent works inside the created project folder using terminal, file, browser, and tool capabilities. Optional external folders remain subject to server authorization.
6. Work streams messages, commands, file changes, approvals, usage, and artifacts.
7. The user can interrupt, steer, approve, and resume the same persistent project conversation without creating a workflow, plan, or parallel active chat.

The default layout is a conversation on the left and contextual work on the right. The right side can show files, diffs, terminal output, browser activity, artifacts, logs, and usage without exposing workflow concepts.

## Agent Runtimes

Work runs the real coding-agent CLIs installed globally on the server. The first targets are Claude Code and Codex; additional CLIs can be added later. Work does not maintain forked copies and never owns a supported CLI's installation or update mechanism. Every CLI uses its own native updater or the server's package-management process. Work discovers and uses the currently installed version on subsequent launches.

Work uses a thin launch-and-control integration around each CLI. This layer is not a replacement agent runtime: it starts the installed executable in the project folder, supplies an isolated user environment, streams native events, and manages the session lifecycle.

Each adapter must:

- Start, resume, steer, interrupt, and terminate a native agent session.
- Map native streaming events into Work's common event model while retaining useful runtime-specific information.
- Supply the project folder, model, reasoning configuration, attachments, instructions, skills, and tool policy.
- Expose runtime capabilities so the UI shows only supported controls.
- Preserve native behavior, including system instructions, repository discovery, instruction files, tools, approvals, and subagent support.
- Report commands, file changes, usage, cost, completion, errors, and requests for user input.

Work must not flatten every runtime into a lowest-common-denominator chatbot or replace native agent behavior with one generic prompt.

### Global CLI Lifecycle And Per-User Isolation

The executable and its certified launch policy are server-global; user data is not. This rule applies to every supported CLI, not only Claude Code and Codex.

- Each CLI's native updater or the server's package manager remains authoritative for installation, upgrade, pinning, and rollback. Work must not run a separate product-specific updater.
- Work detects installed CLI versions and capabilities and reports them in administration and diagnostics UI.
- Work records version changes and runs a runtime-specific compatibility check before launching new sessions. If an update is incompatible, Work disables new launches for that CLI with a clear administrator-facing error instead of silently using broken behavior.
- Each session receives an isolated home/config directory, credentials, instruction context, skills, caches, transcript state, and process identity.
- One user's CLI login, history, memory, skills, MCP configuration, or settings must never become another user's defaults.
- Project instruction files remain in the project folder and continue to follow the selected CLI's native discovery rules.
- Work must not rewrite a shared global `CLAUDE.md`, `AGENTS.md`, CLI home, or MCP configuration to customize one user's session.

This provides centrally maintained native CLIs without creating shared mutable agent state across users.

## Prompts And Instructions

Work should feel as easy to use as ChatGPT or Cowork while retaining the power and conventions of the selected coding agent.

Work is not limited to software development. Its default interaction is a
general-purpose, chat-first assistant for research, analysis, writing,
planning, file-based deliverables, and connected-service work. Direct coding
CLI access is an additional first-class capability for tasks that need it.

Work reuses AgentWorks' `#` automation picker so the user can attach another
workflow as read-only context. The server authorizes every selected workflow;
selection does not let Work edit or execute that workflow.

For ongoing relationships, the shared Attached folders view can persist
another workflow as a read-only link. Workflow-owned links are stored in
`workflow.json.workflow_context_paths`; product-owned links are stored in
`product.json.capabilities.workflow_context_paths`. These are stable references,
not copied files or raw host-folder grants. Every turn resolves them through
the same authorization and read-only folder guard as a temporary `#` reference,
so revoked access takes effect without rewriting the source manifest.

A session is assembled from these instruction layers:

1. **Platform policy:** Security boundaries, access rules, audit requirements, and product behavior users cannot override.
2. **Deployment instructions:** Administrator-defined conventions and policies.
3. **User instructions:** The user's saved communication and working preferences.
4. **Workspace instructions:** Repository guidance, including native files such as `AGENTS.md` and `CLAUDE.md`, interpreted according to the selected runtime.
5. **Selected skills:** Reusable task-specific instructions and resources available in the current scope.
6. **Session request:** The user's current message, attachments, and follow-up steering.

The adapter must preserve each runtime's native precedence and instruction-discovery rules. Work adds authorized context and policy around the runtime; it must not silently rewrite or weaken native instructions.

The UI should show which runtime, model, workspace, user instructions, and skills apply to a session. Protected platform policy may remain hidden, but users should be told that managed policy is active.

## Skills

Skills provide reusable capabilities and guidance similar to the skill experience in ChatGPT, Cowork, and coding-agent products.

Skills may be scoped to:

- **Platform:** Built in and maintained by Work.
- **Deployment:** Installed and managed by an administrator for multiple users.
- **User:** Created or installed by one user and private unless explicitly shared.
- **Workspace:** Available only in a specific authorized folder or repository.
- **Session:** Temporarily attached to one conversation.

A skill may contain instructions, templates, scripts, reference material, and declared tool requirements. Work must show what a skill requires before it is enabled. A skill never grants more filesystem, server, secret, network, or tool access than the user and workspace already allow.

Runtime adapters translate selected skills into formats supported by the native agent. Runtime-specific skills remain limited to compatible runtimes rather than being altered until their behavior changes.

Users should be able to discover available skills, enable or disable them, create personal skills when permitted, see their scope and requirements, and review which skills were active during a completed task.

## User-Level Access And Security

Authorization must be enforced by backend APIs and execution boundaries, not only by hiding frontend controls.

Suggested roles:

- **Viewer:** Can inspect permitted sessions and artifacts but cannot run commands or modify files.
- **Operator:** Can create projects and sessions on assigned servers using allowed tools.
- **Administrator:** Can manage users, assignments, servers, runtimes, shared skills, integrations, policies, and limits.

Permissions may independently control:

- Which servers a user can use and which optional external folders they can attach.
- File read, file write, and terminal execution rights.
- Allowed runtimes, providers, models, and reasoning levels.
- Browser, network, MCP, skill, subagent, and external integration access.
- Which secrets a session may use without revealing their values.
- Which sensitive actions the user may approve.
- Session concurrency, token, time, and spending limits.

Session history is private to its owner by default. Sharing and administrator audit access must be explicit, visible, and recorded.

Server credentials and secret values remain server-side. They must not be sent to the browser merely because a user may use them.

Audit logs should record sign-ins, session lifecycle events, commands, file changes, approvals, configuration changes, secret references, and access-control changes with the actor, timestamp, server, workspace, and session.

## MVP Scope

### Included

- Authentication and user profiles.
- Administrator-managed user-to-server assignments and optional external-folder permissions.
- Automatic project-folder creation under the per-user Work projects root.
- One canonical persistent, resumable chat per project. Existing historical
  conversations remain visible as read-only reference material, but Workshop
  does not create another active chat and the canonical Chat cannot be closed.
- The shared AgentWorks conversation engine and presentation: the same
  streaming, retained-session, queued-message steering, transcript, composer,
  terminal switch, persistence, resume, interruption, and approval behavior.
- Globally installed Claude Code and Codex CLIs with thin launch/control integrations and runtime selection.
- Streaming conversation with attachments, steering, interruption, approvals, and requests for user input.
- Background coding tasks reuse AgentWorks' `run_in_background` lifecycle: the
  child inherits the project guard and attached skills, the chat stays usable,
  and completion or failure returns through the same automatic-notification path.
- File explorer, preview/editor, and diff review inside the project folder.
- A retained native CLI terminal with the same conversation/terminal switch as
  AgentWorks, plus command visibility subject to permissions.
- Browser visibility and control when supported and permitted.
- A project-owned Dashboard for visually managing tasks, notes, plans, status,
  research, project information, or any other user-defined view. It reuses the
  AgentWorks live HTML report contract at `db/reports/index.html` and may use
  the same project-scoped managed SQLite database at `db/db.sqlite`.
- The shared Database view and guarded query, mutation, migration, validation,
  preview, and snapshot tools. Raw access to SQLite files stays blocked.
- Runtime and model selection limited by administrator policy.
- Platform, user, workspace, and session skills.
- Native instruction discovery such as `AGENTS.md` and `CLAUDE.md`.
- MCP and secret assignment without exposing secret values.
- Conversational setup management using the existing AgentWorks skill, secret,
  MCP, and Work folder-grant tools. The UI and the agent operate on the same
  backing stores; installing or attaching something must not create a second
  Work-only catalog.
- Work-native schedules that send exactly one message into a selected project
  chat. They reuse the AgentWorks scheduler lifecycle without exposing workflow
  runs, execution routes, or workflow manifests.
- Slack and WhatsApp bots routed to a selected Work project chat, using the same
  shared bot configuration and delivery infrastructure as AgentWorks.
- Artifact previews.
- Session logs, usage, cost, status, and audit history.
- Per-user limits and administrator management.

### Excluded From The Initial Product

- Workflow creation or workflow overview.
- Plan canvases, phases, steps, step editors, or workflow presets.
- Workflow manifests as Work's identity or storage model.
- Workflow schedules, execution routes, evaluation, Pulse, workflow bots,
  publishing, backup, or access panels reused as-is. Work instead exposes only
  its message-only project schedules and project-chat bot routes.
- AgentWorks workflow-reporting semantics. Work reuses its rendering and data
  infrastructure, but the Dashboard remains general-purpose project content.
- Automatic reuse of every AgentWorks view merely because its component exists.

An excluded capability may return later in a Work-native form—for example, workspace backup or scheduled agent tasks—but it must be designed around users, servers, workspaces, and sessions instead of renamed workflow state.

## Reuse From AgentWorks

Work should be a thin product surface over AgentWorks, not a parallel implementation. The default is to reuse existing AgentWorks behavior and add only the product definition, system prompt, curated skills, branding, runtime configuration, and authorization rules that are genuinely specific to Work.

Treat **99% shared code** as the engineering direction, not as a literal line
count target. Product-specific code may select capabilities, translate project
identity and authorization, persist project settings, and compose shared
surfaces. The behavior-bearing implementation of a platform capability belongs
in shared AgentWorks code. A feature correction made for chat, browser,
automation, files, database, Dashboard, costs, bots, skills, secrets, MCP, or
split-pane behavior should normally fix AgentWorks and Crew together.

**Split pane preview devices** (shared by AgentWorks and Crew). The divider
between chat and the workspace panel has three preview buttons:

- **Mobile:** the workspace panel is a phone-width column (the 480px report
  frame plus padding) and chat takes the rest. The divider does not drag.
- **Tablet:** an even split by default; drag to resize.
- **Laptop:** a compact chat beside the full-width workspace; drag to resize.

Tablet and Laptop remember the dragged width per workflow and device. Mobile
ignores any saved width so the phone frame never floats in a wide panel
(`workspaceLayoutResolver.ts`, `workSurfaceLayoutResolver.ts`).

Before adding Work-specific code, check whether AgentWorks already provides the capability. Prefer configuration, composition, small adapters, and additional props over copied components or forked services. If a reusable primitive is missing, extract it into the shared platform rather than placing a generic implementation under `products/work`.

Reuse existing AgentWorks infrastructure wherever its data model fits Work:

- Chat streaming, cancellation, steering, persistence, and event rendering.
- Conversation and session creation, history, resume, and transcript rendering.
- File browsing and content preview primitives.
- Terminal, browser, artifact, diff, log, and usage presentation.
- Provider, model, MCP, skill, and secret catalog primitives when they support user/workspace scope.
- Product runtime selection through the existing provider-option mechanism.
- Folder-access presentation and storage helpers through shared components and types.
- Authentication and product-surface infrastructure where it enforces the required backend authorization.

Work should not introduce its own transcript renderer, file viewer, terminal, browser, provider selector, skill manager, or project/session format when the platform already has one that can be configured or generalized. New Work-specific code is justified only where the product has different authorization or data semantics.

The frontend reuse contract is checked by
`frontend/src/products/work/WorkSharedPlatformContract.test.ts`. Keep that test
focused on architectural ownership: Crew imports shared components, while its
own folder contains only the shell, identity/creation UI, project persistence,
runtime-selection adapters, and tests. Do not satisfy the contract by copying a
shared component under a different name.

### Configuration Ownership

Keep product defaults separate from session identity:

- `product.yaml` owns Work-wide runtime choices, tool and MCP allowlists, built-in skills, capabilities, sandbox policy, and UI flags.
- Each project's `product.json` owns its durable project/session identity plus
  stable selections for its LLM, MCP servers, skills, and read-only workflow
  references. It does not copy MCP definitions, skill contents, prompts,
  server paths, runtime installations, or credentials.
- User-installed MCPs and skills continue to use the platform's existing user/deployment stores. A future session-specific selection should store only stable references to those records, not duplicate their definitions.
- Secret values always remain in the existing encrypted server-side secret store; neither YAML nor `product.json` contains them.

Therefore, adding an MCP or built-in skill to the Work product normally changes `product.yaml` and the shared MCP/skill registry. It does not expand every session JSON document.

Do not reuse a component as-is when it requires an active workflow, preset, manifest, plan, phase, or step. Extract the generic primitive and provide only the thinnest Work-specific container backed by user, server, workspace, and session IDs.

### Plug-In Feature Bundles

Optional platform capabilities are declared once as feature bundles. A product
profile selects a feature by ID; the shared resolver contributes the existing
tools, default skills, prompt extension, runtime capability flags, dependency
features, and UI panel IDs together. Work must not separately maintain a tool
list, a skill list, capability flags, prompt prose, and toolbar buttons for the
same feature.

The initial shared feature catalog includes `files`, `browser`, `secrets`,
`mcp`, `skills`, `attached-folders`, `workflow-references`, `terminal`,
`models`, `schedules`, `bots`, `database`, `dashboard`, `costs`,
`background-work`, `live-chat`, `voice`, and the base `coding` bundle. A feature
may depend on another feature; for example, Dashboard resolves Database and
Files before adding its viewer, tools, skill, and instructions. Explicitly
disabling a required dependency is a manifest error rather than a partially
working UI.

`product.yaml` is the source of truth:

```yaml
profile:
  features:
    - files
    - mcp
    - id: schedules
      options: {mode: message_only}
```

For backward compatibility, the resolver projects each bundle into the
existing `skills`, `tool_policy.enabled`, `runtime.capabilities`, and legacy
`ui_panels` fields. Existing backend gates therefore keep enforcing the same
authorization while products migrate. The profile API also returns
`resolved_features`; product surfaces render its `ui_panels` declarations with
the shared AgentWorks components. AgentWorks' mode-specific bundles should
migrate through the same resolver without flattening the difference between
Workshop and Run.

Feature instructions are extensions of the main product system prompt. The
runtime must first apply the product prompt as the base, then append resolved
feature instructions through the additive instruction path. MCP discovery,
tool manifests, selected-skill listings, browser guidance, folder grants, and
provider routing follow the same rule. `mcpagent` may enrich the effective
prompt at send time, but it must never replace, reorder ahead of, or silently
overwrite the caller's product prompt.

All visible copy in Work should use server, workspace, session, task, tool, or skill terminology. Workflow terminology must not leak into the Work interface.

## Success Criteria

- An administrator can assign a user to specific servers and optional external folders.
- A user cannot discover or access an unassigned server, another user's project, external folder, session, file, tool, or secret through either the UI or direct API calls.
- An authorized user can create a project with one click, select Claude Code or Codex, complete a real coding task, review changes, and resume later.
- A user can keep separate task sessions and start a new conversation without losing previous history.
- Native workspace instructions and selected skills are applied according to the chosen runtime's rules.
- Commands, file changes, approvals, usage, and artifacts are observable and attributable to the correct user and session.
- Switching runtimes does not silently reuse incompatible prompts, skills, or session state.
- No workflow, plan, phase, step, preset, or workflow-manifest UI is reachable from Work.
- AgentWorks continues to work unchanged.

## Implementation Review — 2026-09-14

### Status

**The Work foundation is implemented, but the complete MVP described above is not finished yet.** The current slice provides automatically created per-user project folders, optional secure folder grants, real global Claude Code/Codex runtime choices, and a shared project workspace with little duplicated product code.

### Completed In The Current Slice

- Work now declares shared feature bundle IDs instead of repeating its tools,
  skills, runtime flags, prompt guidance, and workspace toolbar inventory. The
  backend resolves those bundles into the existing enforcement fields, returns
  `resolved_features` from the profile API, and the Work toolbar reads the
  resolved UI panels. This is additive compatibility infrastructure: existing
  AgentWorks tools and components remain the implementations.
- Resolved feature prompt sections are appended after Work's product prompt.
  The shared `mcpagent` effective-prompt composer now has an explicit tested
  invariant that MCP, runtime, tool, and skill additions extend the product
  base rather than replacing it.

- Work is registered as its own product surface and composes the actual AgentWorks top bar, chat, streaming, restoration, model selector, split workspace pane, and product-profile components. It does not maintain a parallel Sessions/Session Files UI; Work-specific controls are injected into the existing AgentWorks component slots and automation-only controls are capability-hidden.
- The product profile exposes Claude Code, Codex, Cursor, Pi, and Muse through the existing `provider_options` mechanism. It launches the globally installed CLIs and does not install or update them.
- Native CLI tools run in hybrid mode behind the strict server sandbox. User chats retain the CLI in tmux so the shared live-terminal switch and steering remain available for every provider; background children still use the server-owned structured lifecycle. The existing structured file, shell, browser, research, image, and secret tools are allowlisted; workflow, planning, route, Pulse, and orchestration tools are not. Work adds only thin project-schedule wrappers that send one message to a project chat.
- The system prompt describes direct coding work, native CLI behavior, authorized server folders, and `WORK_FOLDER_<ALIAS>` references without workflow terminology.
- Existing AgentWorks skills are reused, including the skill creator. Work can
  list/search/install/import/remove skills; list/create/remove user or project
  secrets; list/search/connect/remove MCP servers; and list/attach/detach
  administrator-authorized server folders. The ordinary shared composer
  continues to provide attachments and the same visible Steer action for queued
  messages. Model selection uses the shared AgentWorks model catalog in the
  right-side Setup toolbar: the selected provider's Builder model is the
  default, and both the coding agent and model remain changeable. A changed
  runtime relaunches the retained CLI on the next message while preserving the
  stable project conversation and its AgentWorks chat history.
- Work also owns small platform-operation skills for MCPs/secrets/folders and
  model setup, skill lifecycle, message-only schedules, project-chat bots, and
  background work. These skills document the existing shared tools and UI
  boundaries; they do not introduce parallel stores or services.
- Each Work project has one canonical persistent Chat. Changing providers
  relaunches the native runtime behind that same application conversation;
  historical conversations remain read-only reference rather than additional
  live tabs.
- The project workspace now uses the shared AgentWorks Views and Setup toolbar primitives. Its reduced set exposes a general project Dashboard, Database, browser, costs/usage, message-only schedules, files, skills, secrets, MCP servers, model setup, project-chat bots, and attached folders while omitting workflow-only controls and workflow reporting semantics. Views and Setup remain open because this reduced toolbar is intentionally compact.
- The shared Files workspace keeps its root visible but starts every first-level
  folder collapsed across all products. Work additionally hides send-to-chat
  shortcuts and the project-root overflow menu.
- MCP and skill selection use the existing AgentWorks selectors and catalogs. Their stable references are accepted only because the Work profile explicitly declares those capabilities; fixed-purpose product profiles keep rejecting them.
- Folder grants are stored per user. Ordinary users can add only existing absolute directories within administrator-assigned roots. Paths are canonicalized server-side and enforced by execution guards rather than trusted from browser input.
- Work can search the same permission-filtered workflow catalog used by the
  AgentWorks picker, suggest name matches, and attach or detach an exact
  `Workflow/<folder>` as durable read-only context in `product.json`. Transient
  `#` references force the full turn path instead of being dropped by retained
  live input. Any durable reference change refreshes the project and relaunches
  retained chats before their next turn so mounted context cannot go stale.
- User identity lookup checks user ID, username, and email so existing assignments continue to resolve when all claim fields are present.
- Narrowing or removing assigned roots prunes stale grants, every read revalidates grants against current roots, and affected live CLI sessions are denied and closed so revoked access cannot remain cached.
- Concurrent folder updates are serialized to avoid lost grants.
- Multiple Work projects use the same generic product-project persistence helper already used by other AgentWorks products. The Work-specific transcript renderer was removed, folder grant rows were extracted into a shared component, and product-surface type validation now has one source of truth.
- Clicking **New project** creates `Chats/Work/projects/<generated-project>` through the shared generic product-project helper, without asking the user to select or authorize a folder.
- The Crew selector also offers permanent project deletion behind the shared
  danger confirmation. Deletion stops retained work, removes the authenticated
  project folder and its schedules/triggers/bots/files/database, clears the
  durable conversation slot and transcript index, closes local projections,
  and selects the next Crew. Deleting a folder alone is insufficient because it
  would leave a stale product-conversation registry binding.
- New projects also receive `code/` as the default home for newly created
  application and source files. The runtime placement policy preserves an
  existing repository layout, and the server idempotently backfills `code/`
  when older Work projects are opened.
- Enabling Database creates a valid project-owned `db/db.sqlite` through the
  guarded workspace database API. Creation is idempotent, so opening an older
  Work project upgrades it without requiring a manual migration or direct
  SQLite file access.
- The created project folder is both the durable project location and the native CLI's primary working directory, so chat, files, diffs, attachments, and CLI execution use the same workspace.
- `product.json` persists generic project/session identity plus the same
  `capabilities.llm_config` shape used by AgentWorks `workflow.json`. The saved
  project runtime is shared by chat, schedules, and bots; it does not duplicate
  MCP, skill, prompt, credential, or external-folder configuration.
- CLI security keeps the project folder writable and receives every optional authorized folder as readable, with only `read_write` grants writable.
- Visible Work copy uses projects and workspace rather than workflows.
- The changed server package compiles with the repository's current local `mcpagent` replacement; the Playwright session-registry capability is detected compatibly when present.

### Current Architecture And Handoff

- `agent_go/internal/workproduct/product.yaml` is Work's product source of
  truth. It selects shared feature bundles, provider options, runtime policy,
  workspace placement rules, and the base system prompt.
- `agent_go/pkg/agentprofiles/features.go` resolves each selected feature into
  its tools, skills, additive prompt extension, runtime capabilities, and
  shared UI panel IDs. Work and AgentWorks should consume those same resolved
  contracts instead of maintaining product-specific copies.
- `frontend/src/platform/chat/productProjects.ts` owns the generic durable
  `product.json` project/session manifest. Work keeps only identity and stable
  selections there; MCP definitions, skill contents, credentials, and runtime
  installations remain in their platform stores.
- `agent_go/cmd/server/product_conversation_registry.go` binds each project to
  its canonical durable AgentWorks chat history. Native interactive chat uses
  the shared retained tmux path, while background work and workflow steps keep
  the shared structured lifecycle. Older conversation files are reference
  history and are not parallel active project chats.
- Work's workspace shell is `frontend/src/products/work/WorkSurface.tsx` and
  `WorkWorkspacePane.tsx`, but its chat, tabs, files, terminal, browser,
  database, dashboard, costs, schedules, bots, skills, secrets, MCPs, and split
  divider are shared AgentWorks components. Product code supplies capability
  and authorization context rather than reimplementing those surfaces.
- Costs use the shared summary endpoint and existing date-wise presentation.
  Provider and model are dimensions within that daily breakdown, allowing a
  project to distinguish Muse, Claude, Codex, and future agents without adding
  a parallel costs section.

### Remaining Product And Operations Work

- Add the administrator UI for assigning users to servers and workspace roots. The current slice exposes backend root-assignment routes but no complete management screen.
- Add server selection when a deployment manages more than one execution server.
- Add a richer native terminal experience for developers who want to work
  directly in the project shell, not only inspect the coding CLI's terminal.
  It should reuse the AgentWorks terminal surface while supporting normal
  interactive shell behavior, terminal-native slash commands and command
  discovery, persistent working-directory state, history, completion, process
  control, resize, reconnect, and safe switching between chat, coding-agent,
  and developer-shell sessions.
- Design and implement a fully isolated developer shell per user. Each user
  must receive a distinct shell identity and process boundary with an isolated
  home directory, environment variables, shell startup files, history,
  credentials, CLI login/configuration, caches, sockets, temporary files, and
  process visibility. Project and administrator-authorized folder grants must
  remain the only shared filesystem access. The implementation should choose
  and document the deployment boundary—such as an OS user, container, or
  equivalent sandbox—and prove isolation with concurrent two-user tests,
  including process, filesystem, environment, credential, and network-policy
  escape attempts.
- Add runtime version/capability diagnostics and compatibility checks after native global CLI updates. Work remains an observer and launcher, not an updater.
- Add end-to-end authorization tests covering direct API calls, symlink/path changes, optional-folder revocation during a live task, user isolation, runtime switching, and project-folder file/terminal/UI behavior.

### Validation Results For This Slice

- Focused Work and shared product frontend tests passed.
- Focused frontend lint passed.
- The frontend TypeScript build passed. The repository-wide frontend test run has one unrelated existing import-cycle failure in `workflowSessionRestore.test.ts` (`getApiBaseUrl` / `useMCPStore` initialization); all other test files passed.
- Focused Work server authorization tests passed.
- Work-product and related Go tests passed.
- `git diff --check` passed.
- The repository-wide Go suite still has unrelated failures: the local `mcpagent` replacement lacks the newer reverse HTTP-session lookup used by a Playwright scheduled-group test, and `cmd/server/guidance` contains a pre-existing 1,030-character skill description that exceeds its 1,024-character limit.

### Change-Scope Note

The large `report_html_tools.go` validator rewrite and its tests are unrelated to Work. The Playwright compatibility shim is also dependency compatibility rather than Work functionality. Keep unrelated changes separate so Work can be reviewed, tested, reverted, and shipped independently.
