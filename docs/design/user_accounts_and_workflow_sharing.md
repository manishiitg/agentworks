# User accounts, product access, and workflow sharing

**Status:** design agreed 2026-09-02; all four phases built and deployed to the
RTS Video Studio box the same day. Kept as the reference for the model.
**Related:** `docs/core/multi_user_authentication.md` (current auth),
`docs/bugs/pulse_platform/sandbox/access/plat-262.md` (read-only enforcement, reused as is),
`deploy/aws-ec2/server/auth-gateway.go` (Video Studio gateway).

## Current behavior

Reviewed against the implementation on 2026-09-18. Account permissions live in
`config/users.json`; workflow sharing lives in each workflow's `workflow.json`.
Platform administration, workflow ownership, product access, and conversational
mode are separate concepts. Making someone a workflow owner does not make them
a platform administrator.

## Roles at a glance

The admin UI offers four account roles. These are combinations of permission
flags, not a `role` claim stored in a JWT.

| Account role | `admin` | `can_create` | `can_edit` | Meaning |
|---|---|---|---|---|
| Platform administrator (Admin in the UI) | true | true | true | Manages users, product access and shared integrations; owner access to all workflows |
| Member | false | true | true | Creates workflows; edits workflows they own |
| Contributor | false | false | true | Edits assigned workflows they own; cannot create or duplicate workflows |
| Read-only | false | false | false | Views, chats and runs workflows shared with them; cannot author workflows |

Workflow sharing has only **Owner**, **Reader**, and **No access**. Owner allows
edit, run, share and delete; Reader allows chat, run, trigger, stop and inspect.
There is no separate workflow editor role. An account with `can_edit=false`
remains a reader even if its ID appears in the owners list. Platform admins
have owner access regardless of the workflow sharing lists. Product access
and any additional workflow allowlist are checked separately.

Read-only sessions can submit a requested improvement through
`submit_workflow_suggestion`. It creates a durable owner-review item without
changing the workflow. Only an interactive workflow owner can answer, dismiss
or consume that suggestion; accepting it does not automatically implement it.
See PLAT-330.

## The model

Two places a permission can live, nothing else.

**On the account** (AgentWorks user record): one `role` per account —
`admin`, `creator`, `editor`, or `viewer`.
- `admin` manages users, sets product access, and can open any workflow.
  The first admin is named in config, never inferred.
- `creator` creates workflows and projects and owns what they create.
- `editor` may own and edit explicitly assigned workflows but cannot
  create or duplicate workflows.
- `viewer` is read-only.
- The legacy `admin`/`can_create`/`can_edit` booleans stay as fallback for
  records that predate roles (admin, then can_create, then can_edit, else
  viewer) and are dual-written on every admin save. `role` wins when set.
- `products: ["agentworks", "video-studio", ...]`. Which product surfaces the
  user may open. A creator with the list absent gets all products; an
  editor or viewer with it absent gets none.

**On the workflow** (`Workflow/<folder>/workflow.json`, `access` block),
using the same words as the account roles:
- `owners: [user_id, ...]`. The creator is the first owner. Owners edit, run,
  share, transfer, delete.
- `editors: [user_id, ...]`. Edit and run, but cannot share.
- `readers: [user_id, ...]` (the Viewer tier; the key keeps its name for
  backward compatibility). Read-only, with exactly PLAT-262 semantics:
  chat, trigger and watch runs, inspect files and DB; no mutating tools, no
  shell writes. Added either by an owner (sharing) or by an admin (assigning
  a specific workflow to a user).

Effective access is the lesser of the account role and the manifest grant:
a Viewer account is never more than a reader even when listed as owner or
editor. `/api/auth/me` reports the global tier plus the resolved
per-workflow map; clients gate workflow actions on the map.

Builder and Run are the presentation of workflow access, not a second authority
control. In workflow chat there is one rule: effective owner/write access means
Builder (stored as `workshop`), and read-only access means Run (stored as
`run`). Clients, saved routes, and restored requests cannot choose a different
authority mode. Origin (interactive, schedule, trigger, bot, child, Pulse) is
tracked separately only to narrow capabilities and label/audit the conversation.
Direct headless workflow execution remains Run because it is not a conversation.
Native-session admission is refreshed when its existing ChatPolicyKey changes,
while preserving the durable chat ID and conversation history. See
PLAT-262.

Products have no account roles of their own. Crew (`work` in the product
allowlist) projects are scoped to the authenticated user's private project
workspace; this is not a separate platform-admin role. Their selected MCP
servers are stored in the project's `workflow.json` under
`capabilities.selected_servers`. Video Studio projects are per user
(under `_users/<id>/Chats/Video Studio/projects`) and not shareable in this
phase; a project manifest can carry the same `access` block later if wanted.

## Storage: JSON, deliberately

Tens of users, a few grants per workflow, one server process. The repo
already stores permission-like state as JSON with a mutex and atomic
temp-then-rename writes; this follows that pattern. SQLite (already present
for pulse/report data) would only earn its place with multiple replicas.

`config/users.json` (global, admin-managed, mode 0600):

```json
{
  "users": [
    {
      "id": "3f9a...",            // stable; sha256("user:"+username)[:16] to match today's AUTH_USERS ids
      "username": "manish",
      "email": "m@example.com",
      "password_hash": "$argon2id$...",   // absent for SSO-only accounts
      "sso": {"provider": "cognito", "external_id": "..."},  // optional
      "admin": true,
      "can_create": true,
      "can_edit": true,
      "products": ["agentworks", "video-studio"],
      "disabled": false,
      "created_at": "2026-09-02T12:00:00Z"
    }
  ]
}
```

Workflow manifest gains:

```json
"access": { "owners": ["3f9a..."], "readers": ["a1b2..."] }
```

Compatibility: older workflow manifests with `created_by` use that creator as
the owner. A manifest with neither an access block nor a creator retains the
legacy account-tier behavior. Legacy tier/product files still load for
identities not found in the user directory. `AUTH_USERS` remains a bootstrap:
users are imported into `users.json`, with passwords hashed.

## Shared integrations versus workflow/project selection

A shared MCP connection is server-wide. Removing it with `remove_mcp_server`
requires platform-administrator authorization and affects workflows and Crew
projects that use it. Workflow ownership alone does not authorize that action.

A workflow owner can detach a server from their workflow using
`update_workflow_config` with `remove_servers`. A Crew project user can select
or deselect an already connected server through
`update_project_mcp_server_selection`. Deselecting does not remove the shared
connection. Newly selected Crew MCP tools take effect on the next user message.
The UI and agent should explicitly distinguish removal **from this
workflow/project** from removal **for everyone on the server**.

## Login: both

- **Password**: `POST /api/auth/login` checks `users.json` (argon2id).
  Admins create users and set an initial password from the admin page; users
  change their own. No self-registration (unchanged).
- **SSO**: Cognito/Supabase authenticate the external identity, then the
  verified email is resolved against `users.json`. An admin-provisioned record
  is itself admission approval and does not also need to appear in
  `AUTH_ALLOWED_EMAILS`; first login links the provider identity while keeping
  the stable AgentWorks user ID and assigned permissions. An unknown SSO user
  is created with `can_create: false` and no products only when the deployment's
  OAuth admission policy allows that email. Disabled records remain blocked.
- JWT claims stay identity-only (no roles in the token); permissions are
  re-read per request from `users.json`, as today.

## Video Studio: users log in as themselves

The gateway's shared `ACCESS_PASSWORD` cookie is replaced by the app's own
login. The gateway already has this mode (`GATEWAY_DISABLE_PASSWORD_GATE`,
live on Dominion). With it on, the browser talks to the agent API with a
real per-user JWT, `MULTI_USER_MODE=true`, and each user gets their own
`_users/<id>` tree: own projects, own secrets, own Claude token, own history.
Product access is checked at `/api/agent-profiles/{id}/...`: a user without
`video-studio` in `products` gets 403 and the surface switcher hides it.

Migration on the box: everything today lives under the `default` user (the
gateway remaps its service identity to it). That tree is renamed to the
first admin's id in one step during the switch-over.

## Enforcement points (all existing code, re-keyed)

| Today | Becomes |
|---|---|
| `workflowAccessForIdentity` (env tiers) | `workflowAccessFor(userID, workflowID)`: owner if admin or in `owners`; read if in `readers`; none otherwise |
| `currentUserIsReadOnly` in the query path (`server.go`) | same flag, from the per-workflow answer |
| `requireWorkflowWriteAccess` route wrapper | owner-of-this-workflow check through `can_edit`; creation and duplication routes use the separate `can_create` gate |
| `requireWorkflowOwnerAccess` (4 admin routes) | `requireAdmin` |
| `filterWorkflowManifestsForUser` / `userAllowedWorkflowID` | list = owned + readers + all if admin; open = same |
| `config/user-product-access.json` | `products` on the user record; checked at product-profile routes and in the surface switcher |
| `WorkflowAccessPopup.tsx` (edits the global tier file) | Share popup on a workflow: add owner / add reader by username or email, remove, transfer |
| `GET /api/auth/users` (unused) | backs the admin page and the share popup's user picker |

Hardening folded in, because the new model makes these real holes:
- The agent server stamps `X-User-ID` on the workspace proxy from the JWT;
  the browser's own header is ignored.
- A session with an empty owner is no longer readable by everyone.
- `AUTH_USERS` plaintext comparison goes away with the import.

## Admin page (frontend)

Under Settings, admins only: user list (add, disable, reset password, choose
Admin/Member/Contributor/Read-only, product checkboxes), and per user the
workflows they own or read. Owners see a Share button on their own workflows.
Read-only users see neither.

## Phases, each shippable alone

1. **User directory + login** (built): `users.json`, argon2id, import from
   `AUTH_USERS`, admin named in config (`ADMIN_USERS=<username>`), admin page
   ("Users & access", `frontend/src/components/admin/UsersAdminPanel.tsx`)
   with user CRUD and product toggles, `/api/admin/users`, `/api/auth/password`.
   Env tiers still honoured underneath for identities the directory does not know.
2. **Video Studio as real users** (built): gateway password gate off, the
   gateway verifies the app JWT itself and stamps `X-User-ID`, login rate
   limited, `deploy/aws-ec2/migrate-to-user-accounts.sh` (one-time; removed 2026-09-23 after RTS migrated) moved the `default`
   tree to the admin.
3. **Workflow ownership and sharing** (built): `access` block
   (`workflow_access.go`), `created_by` alone still names the owner on older
   manifests, a manifest with neither keeps the account-tier behaviour;
   Share popup (`WorkflowSharePopup.tsx`) with `/api/workflow/access` and
   `/api/users/directory`; list annotates `my_access`; per-workflow checks on
   manifest get/update/delete/duplicate, the workspace proxy, schedules, and
   the query path's read-only gate. Readers may run, trigger and stop.
4. **Hardening** (built): the agent's workspace proxy and the gateway both
   stamp `X-User-ID` from the verified token; a session with no recorded
   owner is visible only to admins (or the single local user); passwords
   are hashed. The legacy tier/product files still load for identities the
   directory does not know, so nothing needs deleting on day one.

## Out of scope for now

Roles inside products, shareable Video Studio projects, teams or
organisations, multiple server replicas.

## Invitation email (2026-09-29)

> **Removed 2026-10-01.** The invitation email, the resend route and the per-deployment
> `USER_INVITE_EMAILS` switch no longer exist, and a sign-in no longer creates an account: an
> administrator adds every person (and, later, assigns their slot). The section below is kept
> as history. See `docs/DECISIONS.md`.

Adding a person by email (Access → Users, or the top-bar Users page) sends
them an invitation through the same Supabase project that handles Google
sign-in: Supabase Auth's admin invite (`POST /auth/v1/invite`). The link in the
email lands on the deployment's sign-in page (`PUBLIC_URL`, else the request's
host); the person continues with Google using the invited address, and the
account keeps the role and products the admin set.

- **Never blocks adding.** The create response carries `invite_email`:
  `sent`, `not_configured` (no key: nothing goes out), `exists` (that address
  already has a Supabase sign-in, so Supabase will not invite it again) or
  `failed` with a reason (rate limit, default-SMTP team-only, unreachable).
  The panel shows it and offers **Copy invitation** for anything but `sent`.
- **Resend** on an Invited row: `POST /api/admin/users/{id}/invite`, admin only,
  only for a person added by email who has not signed in.
- **Setup, once per deployment (needs Supabase dashboard access):**
  1. Authentication → SMTP: set a real SMTP account (for Excellence, Google
     Workspace SMTP with an app password, sender no-reply@ their domain).
     Until then Supabase only emails its own team, at 2 per hour, and every
     invitee fails with "Email address not authorized".
  2. Authentication → URL Configuration: add the deployment's URL to the
     redirect allow list.
  3. Authentication → Email Templates → Invite user: word it for the product
     ("You have been added … sign in with Google using this address").
  4. Put `SUPABASE_SERVICE_ROLE_KEY` in the server's private env file
     (`/srv/<product>/.env`), never in git or in chat, and restart.
- **Per-deployment switch:** `USER_INVITE_EMAILS=off` turns invitation emails
  off for a deployment even when it has the key (status `disabled`; the panel
  hides Resend and offers Copy invitation). Excellence sends; RTS
  (`video-studio-agent.service`) and Confida (`product.env` EXTRA_ENV) are off
  (user, 2026-09-29).
- **Shared project.** Excellence signs in through Confida's Supabase project,
  so SMTP settings and templates apply to Confida's own auth emails too, and
  an invited address becomes a user of that project. A dedicated project
  avoids both.
- **The key is a full-admin secret for that project.** The server's
  environment is inherited by coding-agent processes, so
  `SUPABASE_SERVICE_ROLE_KEY` is on multi-llm-provider-go's scrub list
  (`isScopedCredentialEnvironmentKey`): a scoped agent never inherits it.
  Files the server user can read are still reachable by the CLIs' own read
  tools until they are confined (PLAT-364 part 2), the same exposure as
  `AUTH_SECRET`.
