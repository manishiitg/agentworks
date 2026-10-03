# Code — a private coding workspace on the shared server

> **Update 2026-10-01:** Code is always owner-only; human sharing is removed and existing grants are inert. The owner’s Crew/workflow may call explicitly declared Code functions through private internal bindings, without attaching its files. Shared readers/editors inherit no Code access. Audited admin/reviewer inspection remains read-only. See [decisions](../DECISIONS.md).

> **Update 2026-09-30:** a Code project's own `AGENTS.md`, `.claude/`, `.cursor/`, `.pi/`, `.codex/` and `.agents/` are never overwritten or deleted by a chat; the session prompt is a marked, session-counted block in `AGENTS.md`, and projected skills carry an ownership marker. Verified with real Claude and Codex chats overlapping in one Code project. See [project instruction files](project_instruction_files.md), [PLAT-371](../bugs/pulse_platform/security-sandbox/plat-371.md).

Status: proposal (2026-09-28). Not built.

## Summary

**Code** is where a person sits down and codes on the team's AgentWorks
server. It works like a Crew project (files, coding CLIs in a terminal, the
same chat), with four differences:

- **Always private.** Only the owner has normal Code access.
  Skills added in a Code are private to it too. MCP servers are, for now,
  the shared platform ones, as in Crew (private MCP comes later).
  Admins can inspect every Code (see [Admin inspection](#admin-inspection)).
- **Files first.** The files view opens by default. The dashboard is
  secondary.
- **Just a name.** A Code has no identity, role or purpose. It is a
  workspace, not an agent persona.
- **Private function calls.** Its owner can call declared functions from an owned
  Code/Crew/workflow. Other people and external MCP clients cannot call it, and
  it never appears in the public Crew/MCP catalog or folder attachments. The
  owner can use Slack DMs or WhatsApp; channels/groups and templates are absent.

It is the concrete "sit down and code" surface for
[Workbench](https://agentworkshq.com/workbench/). There, CLI subscriptions
(Claude Code, Codex, Cursor, Pi, Muse) are shared on a server, usage is
measured per person, and the CLIs run sandboxed. Code runs on those accounts.
Its usage appears in the existing per-user cost view like everything else.

Naming: the site says "Workbench", the product says "Code". Either rename the
product, or have the Workbench page say Code is its coding workspace, so
nobody looks for a "Workbench" button that doesn't exist.

## Code vs Crew

| | Crew | Code |
|---|---|---|
| Default visibility | Readable by everyone on the server | Owner only |
| Admin view | Owner-only chats (admins see usage, not chats) | Admins can inspect chats, terminals and files, read-only and audited |
| Default view | Chat and dashboard | Files, with editor and terminal |
| Identity / purpose / role | Yes | No, just a name |
| Templates (Crew catalog, playbooks) | Yes | No |
| Reaching others | Crews and workflows (as caller) | Crews and workflows the person can access (as caller); never another Code |
| MCP servers used inside | Yes, shared per server/Crew | Yes, the shared platform ones (as Crew); private per-Code servers later |
| Skills | Yes, shared | Yes, but private: ones added or created in a Code stay in it |
| Exposed over MCP (`ask_crew`, functions) | Yes | No |
| Bots | Slack channels and DMs, WhatsApp, Gmail | Slack DMs and WhatsApp only, 1:1 with a person; no Slack channels or group chats, no Gmail |
| Triggers | Yes | Message-only, owner-created, run as the owner (2026-09-29) |
| Schedules | Message-only | Message-only, owner-created, run as the owner (2026-09-29) |
| Chat UI | tmux terminal + chat area | Same |
| Coding CLIs, models, secrets, skills, browser, terminal | Yes | Yes |

## How to build it: a second product definition, not a copy

Crew is one product definition (`agent_go/internal/workproduct/product.yaml`,
profile `work`), built from a list of shared features. Code should be a
second definition, `codeproduct` (profile `code`), that picks a subset and
sets different defaults. The Crew code must not be forked: every Crew fix
(sandbox, chat, transport, files) then reaches Code for free.

Proposed features for `code`:

- **Keep:** `live-chat`, `coding`, `files`, `terminal`, `models`,
  `secrets`, `browser`, `costs`, `background-work`, `workspace-ui`,
  `attached-folders`. (`memory` was removed on 2026-09-28: a Code keeps
  notes in its own files. Crew's Suggestions view is not shown either.)
- **Keep, restricted:** `bots` limited to `slack,whatsapp` and 1:1 only
  (a new `dm_only` option). Slack channel and group routes can't be
  created for a Code, and a message from a Slack channel or group is
  refused. Each DM or WhatsApp message continues the sender's own chat of
  the Code, the same one-person-one-chat rule as Crew (`senderProfileTurn`),
  and only people with access to that Code are answered.
- **Keep, private peer calls:** `workflow-references` and the Crew/workflow
  calling tools (list and call Crew functions, ask a Crew, read and run
  workflows). A Code can use the Crews and workflows the person working in
  it can access, with that person's permissions. It can also call a Code
  with the same owner when the person owns both Codes. Other Codes remain
  unavailable as targets, reads or attached folders.
- **Keep, private:**
  - `mcp`: for now a Code selects from the shared platform MCP servers,
    exactly as a Crew does (admins add them). Private per-Code servers, with
    their credentials stored with that Code, are deferred (see Decisions).
  - `skills`: skills added, installed or created in a Code live in its own
    `skills/` folder and aren't published to the shared skills list. Other
    Codes and Crews never load them.
  - Only the owner uses its MCP servers, skills and credentials.
- **Leave out:** `voice`, `database`.
- **Schedules and triggers (user, 2026-09-29: "required for sure"):**
  - Message-only, as in a Crew. Only the Code's owner creates them: the
    tools and routes resolve the manifest under the owner; another person gets
    "unavailable".
  - They run as the owner, in the owner's chat of the Code (or a schedule's
    own isolated chat), pinned to the owner, with the owner's personal MCP
    servers and secrets.
  - Owner-created Automation triggers are webhooks only. The private
    function path uses hidden internal bindings that do not appear in
    the Automation panel. Only the actual owner, from an owned Code/Crew/workflow,
    may invoke them; no owner identity is inherited by shared readers.
  - Anyone with a trigger's URL and secret starts a run as the owner, with
    the owner's MCP servers, secrets and tools, on an untrusted payload. The
    UI and the tool say so, and the turn frames the payload as untrusted
    data, never instructions.
  - Schedules stay owner-only; there are no shared Code editors.
- **`dashboard`:** keep it, but as a secondary tab, not the landing view.

Other settings:

- **Tools:** no `work.set-identity` tool, and no identity fields in
  create/rename; a Code has a name only.
- **Prompt:** a plain coding-assistant system prompt with no Crew identity.
- **Workspace:** projects live under the owner's tree, e.g.
  `_users/<owner>/Chats/Code/projects/<slug>-<id8>`. It keeps the Crew rule
  that new source goes under `code/`.
- **Sandbox and home:** as Crew. A private `/tmp` per command, and `HOME` in
  `.sandbox-cache/home`, so git and CLI logins stay with that Code.

Code-specific work outside the definition:

1. **Private Code functions.**
   - Keep Code out of public Crew/MCP catalogs, folder references and attachments.
   - The actual owner may call another owned Code, or call Code from an owned
     Crew or explicitly owned workflow using `#code:<id>`. Source manifests and
     ownership are verified again at dispatch, polling and queued execution.
   - Code offers only explicitly declared functions, with input/result schemas;
     it has no implicit `ask`. Declare/remove functions in the owner's Code chat.
   - A Code call runs in the owner's isolated target conversation and returns
     the function result. It never grants the caller access to the Code folder.
   - Shared readers/editors, other owners and external connections cannot call
     Code functions. Owner-created authenticated webhooks are a separate API.
2. **No templates.** The Crew template catalog, playbooks and "install a
   role" flows do not appear anywhere in Code: not in creation, the empty
   state, or the Ask AI suggestions.
3. **Files-first UI.** Open a Code on the files view. The file tree, editor
   and terminal come first; the chat is beside them, and the dashboard is a
   tab. The chat is the same component as today (tmux terminal + chat area).

## Access model

### Always private

A new Code is visible to its owner only. It does not appear in other users'
lists, search, cost drill-downs (beyond totals), or the global monitor.

### Privacy

Code cannot be shared with other users. Normal files, links, chats, terminal,
Git, credentials and runtime are owner-only. Old viewer/editor/co-owner grants
are ignored without deleting the legacy server data. Old share API URLs return
410; Code's shared directory stays empty. Code has no Setup → Share panel.

Owned Crew/workflow function calls do not change this rule: the actual caller
must be the Code owner, and only explicitly declared functions are exposed.
They return results through internal bindings without attaching Code files.

### Admin inspection

This is the most important part of Code for teams. **Admins can see what
everyone does in Code.**

- **Read-only.** An admin can open any user's Code: chats, terminal
  transcripts, files and usage. They cannot send messages, resume a session,
  or edit files as that user.
- **Visible to users.** Setup → General ("Access") and the Code landing page
  say that admins and Code reviewers may inspect it, read-only and logged.
  "Private" means private from colleagues; inspection is a separate audited
  permission, never a normal Code role or permission to run it.
- **Audited.** Every admin view is logged: who, which Code, what, and when.
  An admin can see the log, so the audit trail covers admins too.
- **Code reviewers** (user, 2026-09-28; built). An admin can tick "Code
  reviewer" on any account, on top of its role (`UserRecord.code_reviewer`).
  A reviewer is not an admin but reviews like one, for Code only:
  - every Code's cost row and per-person split in the cost overview (other
    products' rows stay owner/admin-only);
  - every Code's chats and files, read-only, through the same inspection
    endpoints; each view is audited with `role: "reviewer"`;
  - the audit log itself (admins and reviewers both read it).
  A disabled account loses it. Ticking it also enables the Code product for an
  account whose products are a restricted list, since the inspector lives in
  Code. Only an admin sets it (`/api/admin/users` is admin-only).
- **Providers → Conversations and Costs** (user, 2026-09-30). These review
  screens are visible only to admins and enabled Code reviewers, including
  the provider account cost section. Conversations filters Code projects by
  owner and chats by participant, then opens a read-only transcript through
  the existing audited inspection APIs. Changing projects, chats or access
  clears the previous transcript and ignores late responses. Personal Work
  chats remain outside the Code reviewer scope. The global cost overview
  and provider account cost APIs require reviewer/admin permission as well;
  their existing work and account visibility filters still apply. The legacy
  unfiltered `/api/cost/summary` is admin-only. Per-workspace cost views retain
  their existing workspace access rules.
- **Review over MCP / the external API** (built). Token scope `code:review`
  plus seven read-only tools: `list_code_workspaces`, `get_code_costs`,
  `list_code_files`, `read_code_file`, `list_code_chats`, `read_code_chat`,
  `get_code_audit`. They run the same inspection handlers, so every call
  (lists and cost reads included) is audited, with `via: "token:<id>"`. The
  account is re-checked on every call: a token whose user stops being an admin
  or reviewer gets 403 at once, and a token holding the scope without such an
  account gets 403 and does not even list the tools. Only admins and reviewers
  can mint `code:review` or see and grant it on the OAuth consent screen. This
  is review of Codes, not calling one: a Code itself is still not callable
  over MCP.
- **Scoped to Code.** Today chat history is owner-only even for admins
  (`chatHistoryVisibleTo`, `agent_go/cmd/server/chat_history_routes.go:167`),
  and that deliberate rule stays for Crews and personal chats. Admin
  inspection is a per-product setting, on for Code.
- **An Admin view.** A place to list users → their Codes → open one
  read-only. It sits next to the existing per-user usage and cost overview
  (`cost_overview.go`).

## Security prerequisites

Code is private and has a live terminal, so the server's isolation between
users has to hold:

- **Done (PLAT-364 step 1, deployed on RTS 2026-09-28, QA #236).**
  - Each sandboxed command gets its own private `/tmp`, and the tmux server
    socket is unreachable from the sandbox.
  - `HOME` is per workflow or Crew.
  - The shared `/tmp` credentials were removed.
- **Open, accepted for now.** In "Native agent tools" (hybrid) mode the CLIs'
  own read tools are not sandboxed and can read other users' folders.
  - They only read; writes and execution go through the sandbox.
  - Tracked in PLAT-364; the fix is to run the CLIs under the Landlock
    launcher with per-user CLI homes.
  - *(Decided 2026-09-28 by the owner: native tools on by default.)* Code
    now runs with native agent tools through its "Native agent tools"
    switch, like Crew, unless the Code turned it off. *(2026-09-29: the
    switch is now shown in the Code UI, Models → Agent tools, and native
    tools are on for every Code turn type, owner decision "only off for
    workflow steps".)* Known exposure until
    the CLIs are confined (PLAT-364 part 2): native reads in a Code can
    reach files outside the Code, and a run on someone else's shared
    provider account can read that account's login files. Confining the
    CLIs closes both.
- **Open.** The browser socket folder `/tmp/.agent-browser` is shared by all
  users, so one user's command could drive another user's browser daemon.
  It needs per-user socket folders before Code enables the browser for
  several users.

## Build plan

1. **Definition.**
   - Add `codeproduct` with the feature set above, a plain prompt, no
     identity tools and the `Code` branding.
   - Register it and add it to the product switcher.
2. **Always private.** Owner-only normal access; human shares are retired.
3. **Private function callers, no templates, owner-only bots.** Allow declared
   functions from owned Codes/Crews/workflows only for their actual owner. Refuse
   public MCP function access and Slack channel/group access. Allow owner Slack
   DMs and WhatsApp. Hide template, playbook and role flows.
4. **Files-first UI.** Land on files; editor and terminal alongside chat;
   dashboard as a tab.
5. **Admin inspection.** Per-product setting, read-only views, the user
   notice, the audit log and the admin listing.
6. **Per-user browser sockets** (security prerequisite above).
7. **QA ticket** after each user-visible step, per the QA template.

## Acceptance (live, on RTS)

1. **Privacy.**
   - User A creates a Code. User B cannot see it in lists, search, MCP or the
     monitor, or by guessing its URL or ID; each returns 404.
   - There is no sharing control. Old viewer/editor/co-owner grants give B no
     access to files, links, chats, Git, bots or runtime.
2. **Admin.**
   - An admin opens A's Code read-only and sees chats, terminal and files,
     but can't send or edit.
   - The view appears in the audit log, and A sees the "admins can view"
     note.
3. **Closed.**
   - Public Crew/MCP catalogs and folder attachments never reach a Code;
     only owner-authorized declared functions are callable.
   - A Slack channel or group route to a Code can't be created, and a
     channel message is refused.
   - The owner's Slack DM and WhatsApp messages continue the owner's own
     chat of the Code.
   - A DM from someone without access is refused.
   - A skill added in A's Code is usable there. It is absent from B's
     Codes, every Crew and the shared skills list. (Private MCP servers are
     deferred; a Code uses the shared platform ones.)
   - From a Code, calling a Crew function, asking a Crew and running a
     workflow the person can access all work.
   - Declared Code functions work from owned Codes/Crews/workflows for the
     actual owner. Shared readers/editors, foreign sources and external callers
     are refused; loss of source ownership blocks polling and queued starts.
4. **Isolation.** From A's Code terminal and shell tool, B's files, `/tmp`,
   tmux and browser are unreachable (QA #236 checks, plus the browser socket
   check).
5. **Regression.** Crews work exactly as before.

## Basic setup first (2026-09-28)

Code ships a basic setup first; integrations come later. In Code's
`product.yaml` today:

- **On:** chat with the coding agent (`live-chat`, `coding`), `files`,
  `terminal`, `models`, `costs`, `secrets`, `skills` (private to
  the Code), `attached-folders`, `browser`, `dashboard` with its `database`,
  outbound `workflow-references` (calling Crews and workflows) and
  `background-work`. Audited admin inspection is on; sharing is removed.
- **Chat apps, 1:1 only** (user, 2026-09-28): `bots` with `dm_only`.
  - Slack: only the Code's **own dedicated Slack app**, never the shared
    server bot or a channel; it answers **1:1 DMs** from the owner only. The Setup tab offers
    only "this Code's own bot".
  - WhatsApp: private per person, as everywhere in AgentWorks. A Code is
    offered on WhatsApp to its **owner only**, just as on Slack and the web.
  - Gmail / Google Workspace: the Code's own private accounts only (below).
- **Later:** `mcp` (MCP servers).

## Decisions (2026-09-28)

- **Code to Crew:** not supported. A Code does not become a Crew.
- **Admin-wide MCP servers and skills in Codes:** deferred; not in scope now.
- **MCP in Code: the same as a Crew** (user, 2026-09-29, superseding the
  2026-09-28 per-person model in [code_private_mcp.md](code_private_mcp.md)). A
  Code is a place: its connections are added with the owner's login and used by
  every chat in it ([personal_mcp_attach.md](personal_mcp_attach.md#code)); the
  global platform servers work too; secrets are the Code's project secrets. The
  Code agent manages the Code's connections with `manage_my_mcp_servers`, and
  deployment sign-in apps (Google, GitHub, ...) make Connect a one-click
  sign-in. A Code's connections never appear in Crews or workflows.
- **Terminal:** the coding CLI's own terminal, and a **Terminal** tab in the Code
  (user, 2026-10-03; a standalone shell panel was built, removed 2026-09-28, and
  rebuilt on the user's request once everyone had their own Linux account). It is a
  real shell in the Code's folder, owner only, run as the person's own slot account
  inside the same Landlock sandbox as the agent's shell tool (private /tmp and
  /dev/pts). A person without a slot gets none. One per person per Code; it keeps
  running while the tab is closed and stops after 30 idle minutes, on Stop, or when
  the Code is deleted. Not enabled on RTS until its instance-role exposure is closed.
  See docs/DECISIONS.md, 2026-10-03.

## To think about

- [ ] **Share a provider connection with chosen people** (user, 2026-09-28;
  later). Today a personal provider connection (a Cursor key, a Claude login)
  is its owner's alone (`provider_connections.go`: owner-only listing and use),
  and the only shared option is the admin's server account. Wanted: person X
  shares their connection with specific users A, B, C, who can then pick it
  in their own chats. Platform-wide, not Code-only. Open points: who pays and
  sees the cost (the owner), revoking a share, and whether a shared login's
  CLI home is safe to use from several people's sessions at once.

- [x] **Gmail in Code: private to the Code** (user, 2026-09-28; built; switched off 2026-09-29 in favor of Google's Workspace MCP servers, restored 2026-09-30: those servers are a Google Developer Preview that needs Google-side enrollment, while the gog integration (Gmail tab: Gmail, Drive, Calendar, Docs, Sheets, Slides) works with a normal OAuth client; MCP stays for GitHub and the rest).
  Today a Gmail/Google account connected through gog sits in one
  server-wide registry: any workflow or Crew can use it by ID, or fall back to
  the default connection. A Gmail account added in a Code must instead be
  that Code's own:
  - The connection records the Code it belongs to (its project root).
    Crews, workflows and the user's other Codes never list, select, default to
    or use it; the Google CLI tool, the grant tools, workflow Gmail settings
    and notifications all refuse it outside that Code.
  - Only the Code's owner connects it (from the Code's Setup), like WhatsApp.
  - Grants work as today: read needs the read grant, drafting/sending needs
    the explicit agent-write opt-in plus the compose grant.
  - Decided: a Code uses **only its own** accounts, never the owner's
    shared ones; and only in the **owner's chats** (web, Slack DM,
    WhatsApp), never an editor's.
  - Built as `bots` option `gmail: own`, `GmailConnection.ScopeWorkspace` /
    `OwnerID`, `services.GmailUseScope` enforced by the Google CLI tool, the
    grant tools, the settings API (`workspace_path`), workflow notification
    senders and the default connection.
  - Decided (user, 2026-09-28): stored **separately**. Each private
    connection has its own gog store (`<gog home>/../gog-private/<hash>`, 0700),
    never the shared GOG_HOME that trusted terminals, workflows and Crews are
    handed. Sign-in imports there; status, send and the Google CLI tool run
    with `--home` pointing there; deleting the connection deletes the store.
  - Decided (user, 2026-09-28): a Code **may call** Crews and workflows (ask /
    call_function) that use Gmail. They run in their own context with
    whatever Gmail they are set up with, never the Code's.

- [ ] **App preview.** When someone builds a web app in a Code, the agent runs
  it on the server (for example `npm run dev` on port 3000). A private
  preview link, which only people with access to the Code can open, would let
  them see the running app. It is useful for UI work but exposes server ports,
  so it needs per-user lockdown. Not decided; not in the first version unless
  decided.

## Native agent tools (2026-09-29)

A Crew and a Code have no "Native agent tools" switch: they are always on and
people cannot turn them off. The server ignores a `native_agent_tools: false`
an older project stored (`ProjectNativeAgentTools` is always true for a
project product). The separate per-workflow setting is unchanged. A Crew's
native tools still apply to its owner's own turns only; readers keep
AgentWorks-only tools.
