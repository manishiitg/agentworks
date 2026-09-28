# Code — a private coding workspace on the shared server

Status: proposal (2026-09-28). Not built.

## Summary

**Code** is where a person sits down and codes on the team's AgentWorks
server. It works like a Crew project (files, coding CLIs in a terminal, the
same chat), with four differences:

- **Private by default.** Only the owner sees a Code until they share it.
  MCP servers and skills added in a Code are private to it too.
  Admins can inspect every Code (see [Admin inspection](#admin-inspection)).
- **Files first.** The files view opens by default. The dashboard is
  secondary.
- **Just a name.** A Code has no identity, role or purpose. It is a
  workspace, not an agent persona.
- **Closed to callers.** It isn't exposed over MCP: no functions, no
  `ask_crew`, no Slack channels or group chats, and no Crew templates. The
  owner can reach their own Code from a Slack DM or WhatsApp.

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
| Default visibility | Readable by everyone on the server | Owner only, until shared |
| Admin view | Owner-only chats (admins see usage, not chats) | Admins can inspect chats, terminals and files, read-only and audited |
| Default view | Chat and dashboard | Files, with editor and terminal |
| Identity / purpose / role | Yes | No, just a name |
| Templates (Crew catalog, playbooks) | Yes | No |
| Reaching others | Crews and workflows (as caller) | Crews and workflows the person can access (as caller); never another Code |
| MCP servers used inside | Yes, shared per server/Crew | Yes, but private: ones added in a Code belong to that Code |
| Skills | Yes, shared | Yes, but private: ones added or created in a Code stay in it |
| Exposed over MCP (`ask_crew`, functions) | Yes | No |
| Bots | Slack channels and DMs, WhatsApp, Gmail | Slack DMs and WhatsApp only, 1:1 with a person; no Slack channels or group chats, no Gmail |
| Triggers | Yes | No |
| Schedules | Message-only | No, for now |
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
  `memory`, `attached-folders`.
- **Keep, restricted:** `bots` limited to `slack,whatsapp` and 1:1 only
  (a new `dm_only` option). Slack channel and group routes can't be
  created for a Code, and a message from a Slack channel or group is
  refused. Each DM or WhatsApp message continues the sender's own chat of
  the Code, the same one-person-one-chat rule as Crew (`senderProfileTurn`),
  and only people with access to that Code are answered.
- **Keep, outbound only:** `workflow-references` and the Crew/workflow
  calling tools (list and call Crew functions, ask a Crew, read and run
  workflows). A Code can use the Crews and workflows the person working in
  it can access, with that person's permissions. Another Code is never a
  valid target: calls, reads and attached folders pointing at a Code are
  refused.
- **Keep, private:**
  - `mcp`: MCP servers added in a Code, and their credentials, are stored
    with that Code. Other Codes, Crews and workflows never see or use them,
    and a Code sees none of the MCP servers another Code added.
  - `skills`: skills added, installed or created in a Code live in its own
    `skills/` folder and aren't published to the shared skills list. Other
    Codes and Crews never load them.
  - Viewers and editors of a shared Code use its MCP servers and skills, but
    never see MCP credentials.
- **Leave out:** `triggers`, `schedules`, `voice`, `database`.
- **`dashboard`:** keep it, but as a secondary tab, not the landing view.

Other settings:

- **Tools:** no `work.set-identity` tool, and no identity fields in
  create/rename; a Code has a name only.
- **Prompt:** a plain coding-assistant system prompt with no Crew
  vocabulary: no identity, functions or bots.
- **Workspace:** projects live under the owner's tree, e.g.
  `_users/<owner>/Chats/Code/projects/<slug>-<id8>`. It keeps the Crew rule
  that new source goes under `code/`.
- **Sandbox and home:** as Crew. A private `/tmp` per command, and `HOME` in
  `.sandbox-cache/home`, so git and CLI logins stay with that Code.

Code-specific work outside the definition:

1. **Not callable, and Codes are sealed from each other.**
   - Leave Code out of `list_crews`, the MCP Crew tools, Crew-to-Crew calls,
     trigger links and Slack channel or group routes.
   - Calls go one way: a Code may call Crews and workflows, but nothing may
     call a Code, including another Code.
   - Workflow and Crew references, attached folders and file grants never
     resolve to a Code's folder. The server should refuse
   them for profile `code`, not merely hide them.
2. **No templates.** The Crew template catalog, playbooks and "install a
   role" flows do not appear anywhere in Code: not in creation, the empty
   state, or the Ask AI suggestions.
3. **Files-first UI.** Open a Code on the files view. The file tree, editor
   and terminal come first; the chat is beside them, and the dashboard is a
   tab. The chat is the same component as today (tmux terminal + chat area).

## Access model

### Private by default

A new Code is visible to its owner only. It does not appear in other users'
lists, search, cost drill-downs (beyond totals), or the global monitor.

### Sharing

The owner shares with named people:

- **Viewer:** read files and chat history.
- **Editor:** also edit files, and run the agent in their own chat of it.
  This is the same one-person-one-chat rule as Crew readers and Slack DMs.
- **Co-owner:** also manage sharing.

A shared Code stays in the owner's tree. It does not move to the shared
`Crew/<id>` root. Unsharing removes access at once.

### Admin inspection

This is the most important part of Code for teams. **Admins can see what
everyone does in Code.**

- **Read-only.** An admin can open any user's Code: chats, terminal
  transcripts, files and usage. They cannot send messages, resume a session,
  or edit files as that user.
- **Visible to users.** Code shows a note: "Admins on this server can view
  your Code workspaces." "Private" means private from colleagues, not from
  admins.
- **Audited.** Every admin view is logged: who, which Code, what, and when.
  An admin can see the log, so the audit trail covers admins too.
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
  - Until then, Code should default to MCP-only tools, where native tools are
    off.
- **Open.** The browser socket folder `/tmp/.agent-browser` is shared by all
  users, so one user's command could drive another user's browser daemon.
  It needs per-user socket folders before Code enables the browser for
  several users.

## Build plan

1. **Definition.**
   - Add `codeproduct` with the feature set above, a plain prompt, no
     identity tools and the `Code` branding.
   - Register it and add it to the product switcher.
2. **Private by default + sharing.** Owner-only visibility, viewer, editor
   and co-owner grants, and the one-chat-per-person rule for editors.
3. **Not callable, no templates, DM-only bots.** Refuse MCP, function,
   trigger and Slack channel or group access for profile `code`. Allow Slack
   DM and WhatsApp for people with access, each in their own chat. Hide the template, playbook and role flows.
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
   - A shares with B as viewer: B can read, but can't edit or run.
   - A shares with B as editor: B runs the agent in B's own chat of it.
     Unsharing removes access immediately.
2. **Admin.**
   - An admin opens A's Code read-only and sees chats, terminal and files,
     but can't send or edit.
   - The view appears in the audit log, and A sees the "admins can view"
     note.
3. **Closed.**
   - `list_crews`, `ask_crew`, functions and triggers never reach a Code,
     and there are no templates anywhere.
   - A Slack channel or group route to a Code can't be created, and a
     channel message is refused.
   - The owner's Slack DM and WhatsApp messages continue the owner's own
     chat of the Code.
   - A DM from someone without access is refused.
   - An MCP server or skill added in A's Code is usable there. It is
     absent from B's Codes, every Crew and the shared skills list, and B
     can't read A's MCP credentials even when A's Code is shared with B.
   - From a Code, calling a Crew function, asking a Crew and running a
     workflow the person can access all work.
   - The same calls against another Code (by ID, path or attached folder)
     are refused.
4. **Isolation.** From A's Code terminal and shell tool, B's files, `/tmp`,
   tmux and browser are unreachable (QA #236 checks, plus the browser socket
   check).
5. **Regression.** Crews work exactly as before.

## Decisions (2026-09-28)

- **Code to Crew:** not supported. A Code does not become a Crew.
- **Admin-wide MCP servers and skills in Codes:** deferred; not in scope now.
- **Terminal:** both, the vendor CLI's own terminal (as in Crew today) and a
  plain shell, in the same sandbox as the shell tool.

## Open questions

1. App preview. When someone builds a web app in a Code, the agent runs it on
   the server (for example `npm run dev` listening on port 3000). Should the
   person be able to open that running app from their browser through
   AgentWorks, via a private preview link that only people with access to the
   Code can open? Without it they can see the code but not the running app.
   Proposed: not in the first version.
