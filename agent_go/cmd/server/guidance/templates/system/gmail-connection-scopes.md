## Managing the Gmail/Google Workspace bot's permissions

Read this when a user asks to connect, reconnect, or change what a Gmail
sending account can do — "give this workflow Drive access", "why can't it
read my Google Sheet", "increase the scope for gmail_001", "add Calendar
read-only".

### Direct terminal use

Builder and workflow terminals can run the real `gog` directly through
`execute_shell_command`, including pipes and scripts. `GOG_HOME` is supplied by
the execution environment and identifies the shared credential store. Use
`gog auth list --json` to discover the exact account/client pair, then run
`gog --account '<email>' --client '<client-name>' <command>`. No hardcoded host
path or workflow `VAR_GOG_HOME` is needed. Use `gog <command> --help` for the
installed command syntax. A missing or inaccessible configured store is a runtime
configuration issue; it does not by itself mean Google authorization expired.
Restricted agent profiles may intentionally lack terminal credential access.

New or migrated connections use gog for token storage and refresh. Legacy gws
connections remain supported until migrated; do not uninstall gws or reconnect
an otherwise working account merely because both binaries exist.

### The two things that determine what an account can actually do

1. **The stored request** — `GmailConnection.AllowReadAccess` (Gmail read),
   `GmailConnection.AllowAgentWriteAccess` (agent draft/send/reply), and
   `GmailConnection.Services` (which
   Google Workspace services beyond Gmail — Drive, Sheets, Docs, Slides,
   Calendar — and whether each is read-only or read+write). This is what
   the connection is *configured* to ask for.
2. **What Google actually granted** — fixed the moment the user last
   completed Google's consent screen for this connection. Google has no API
   to widen or narrow a token's scope after the fact; the only way to change
   it is a fresh consent (Reconnect).

**These two can drift apart.** The shared account cards show **AgentWorks
settings** and **Google** permissions separately. Neither substitutes for the
other: Gmail reading requires both the saved `allow_read_access` opt-in and a
Google read grant. If Google already granted `gmail.readonly` but the saved flag
is off, say "Google already granted read access; AgentWorks still has mailbox
reading disabled" and guide the owner to **Change access**. Do not claim Google
has withheld read access in that case, or enable it automatically.

### Connecting through either OAuth source

The same **Connect a Google account** form is used by Code, Crew, workflows and
Relays. The owner/admin can choose the **Company Google app** configured by the
deployment administrator, a saved named app, or **Use my own OAuth JSON**. For
an upload, the person gives the app a unique name and uploads the original file
in the UI; never ask them to paste client secrets into chat. A company app needs
no per-account upload. Reusing a saved named app needs no second upload. Existing
accounts retain their connection ID and OAuth client when changing access.
Code accounts remain private to their owner; shared accounts remain admin-managed.

### Always check current state first, from chat

Call `list_gmail_connections` (optionally with `connection_id`) before answering
any scope/permission question, and before every `update_gmail_connection_grants`
call — `services` is a full replacement list, so acting without first reading
the current one silently drops every service not repeated.

It returns, per connection, both the stored request (`allow_read_access`,
`allow_agent_write_access`, `services`) and what Google has **actually** granted (`granted_scopes`, from
the live token — this is the one that's true, not the stored fields), plus a
`stored_but_not_granted` list that already tells you what's wrong. Use it
directly instead of asking the user to describe screenshots:

- If `stored_but_not_granted` is empty, the connection has everything it's
  configured for.
- If it lists something and the user hasn't reconnected since requesting it,
  tell them to click **Reconnect** (or call `update_gmail_connection_grants`
  to get a fresh `reconnect_url`) and complete Google's consent screen.
- If it lists something **and the user says they already reconnected**, first
  check whether the browser callback actually reported **Gmail connected**.
  A failed callback, including a gog import failure, leaves the old grant in
  place. Report that failure and retry sign-in. If a successful callback still
  omits access, investigate the exact OAuth response, app and organization
  policies. Do not assume Google silently drops scopes that are absent from
  the consent-screen configuration.

### Changing what a connection is authorized for, from chat

Call `update_gmail_connection_grants`:

- `connection_id` — omit to target the account's default connection.
- `allow_read_access` — omit to leave Gmail read access unchanged.
- `allow_agent_write_access` — permit agents to create drafts and send/reply.
  It is off by default and requests `gmail.compose`; omit it to leave the
  setting unchanged. Notification delivery through `notify_user` is separate.
- `services` — the **complete replacement list** of Workspace services this
  connection should be authorized for. Omit entirely to leave services
  unchanged. Pass `[]` to strip every service grant back to Gmail-only.
  Passing `[{"service":"drive"}]` when the connection already has Sheets
  authorized **removes Sheets** — always include everything that should
  remain, not just what's being added. Read the connection's current
  `services` first if you don't already know them.

**Every entry in `services` also needs a `write` decision — do not just omit
it.** Omitted/`false` means read-only; the service cards show explicit Read only and Read and edit choices — a user who says "give this workflow
Drive access" almost always means it needs to *create or edit* files there,
not just read them, and a silent read-only grant produces a confusing
"permission denied" later with no obvious cause. Infer `write` from what the
user is actually trying to accomplish, not just the literal words:
- Verbs like save, create, upload, write, edit, update, post, send (for
  Sheets/Docs/Slides/Calendar), or "so the workflow can output to X" → set
  `write: true` for that service.
- Verbs like read, check, look up, search, "so it can reference X" → leave
  `write: false`.
- If genuinely ambiguous, ask the user rather than guessing read-only by
  default — silently under-granting is what causes this confusion in the
  first place.

This call only updates the **stored request** — it does not talk to Google
and does not change what the account can do yet. It returns a
`reconnect_url`. You must:

1. Tell the user to open `reconnect_url` and complete Google's consent
   screen. New Google scopes require successful consent. Saved restrictions
   can take effect immediately; an existing Google grant does not itself
   turn on the application's Gmail read opt-in.
2. Call `perform_ui_action(action="open", view="mcp")` right after and point at the
   Gmail tab, so the Sending accounts panel is visible and they can see the
   updated request (and click Reconnect there instead, if they'd rather not
   use the link).

Never claim the new access is active before the user confirms they
completed the consent screen — the tool call succeeding only means the
*request* was saved.

### A workflow's own permission is a third, separate layer

Even once a connection's token genuinely has broad access, a specific
*workflow* can only use a Google service through `google_workspace_cli` if
that service is in the connection's `services` list — this is the exact
same allowlist `update_gmail_connection_grants` edits, so fixing "this
workflow can't use Drive" and "widen this account's Drive access" are the
same action, not two separate systems to reason about.

### Two backends exist; both are supported

A Gmail connection's actual sending call may run through `gws` or `gog`
(`GmailConfig.use_gog_backend`, a deployment-wide setting) — this is
transparent to a connection's stored request and to this tool; don't try to
detect or reason about which backend a deployment uses when managing
scopes. `google_workspace_cli` (Drive/Sheets/Docs/Slides/Calendar) always
uses `gog`, regardless of that setting.

A provider login or billing email is not a Gmail connection or the signed-in AgentWorks user. The app login email also does not establish mailbox consent. Identify usable mailboxes through `list_gmail_connections` and verified scoped account checks. An empty list means no connected account; ask which account the user wants to connect without guessing from provider metadata.
