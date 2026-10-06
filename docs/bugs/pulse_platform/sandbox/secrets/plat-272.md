[← sandbox / secrets](index.md)

# PLAT-272 — Workflow secrets were per-user, so anyone but the person who typed them ran with empty values

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | sandbox |
| Area | secrets |
| Summary | secrets/authorization, severity high — with per-workflow |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; RTS live reverify pending` |
| Last synchronized | `2026-09-03` |

- **Priority:** secrets/authorization, severity high — with per-workflow
  sharing (PLAT-262 read-only accounts, user-accounts phase 3
  owners/readers) now live, every user other than the one who stored a
  secret — a read-only reviewer running the workflow, a co-owner, a
  scheduled run started under any other identity — resolved nothing and
  every `$SECRET_<NAME>` was silently empty.
- **Origin:** raised by the user while moving the `rtslatency` workflow to
  the RTS server: "if i am using a read only login.. i should be able to
  access secrets … these are workflow secrets, should be accessible across
  users". PLAT-267 had just papered over one instance of the same root cause
  (scheduled runs resolving against the placeholder `default` user) by
  routing the scheduler to the creator; this ticket fixes the model.

## Problem

"Workflow" secrets were stored **per user**:
`_users/<uid>/workflow_secrets/<sha256(path)>.json`, with the ciphertext
AES-GCM bound to that user's ID as additional data. `loadSelectedSecrets`
(the single runtime funnel for chat, workshop, scheduled and bot runs)
read the *requesting* user's document and decrypted with the *requesting*
user's ID. A second identity therefore found an empty document, and even
a copied blob would have failed authentication. The three
`/api/secrets/workflow/*` routes also had **no per-workflow gate at all** —
any authenticated user could write to their own copy; nothing consulted
the manifest's owners/readers.

## Resolution

- **Shared store** (`cmd/server/workflow_shared_secrets.go`,
  `pkg/chathistory/shared_secrets.go`): one document per workflow at
  `_users/_shared/workflow_secrets/<hash>.json`, ciphertext bound to
  `"workflow:" + canonical path`. `_shared` is a reserved pseudo-user no
  real ID can collide with (directory IDs are sha256 hex, OAuth/bot IDs are
  lowercase slugs). Reusing the `_users/` layout means every guard that
  already hides that tree from agents — root-listing filter, DB-query
  rejection, folder-guard read allowlists, the git-push
  `workflow_secrets/` check — covers the new location by construction; no
  store-interface signature changed.
- **Access gating** (`secrets_routes.go`): store/delete require
  `requireWorkflowOwner`; list requires the workflow to be visible and
  returns ciphertext only to owners; `/api/secrets/decrypt` accepts
  `workspace_path` and treats it as a reveal that only owners may perform.
  The client still encrypts against itself via `/api/secrets/encrypt`; the
  store handler re-binds the value to the workflow.
- **Runtime** (`server.go` `loadSelectedSecrets`): reads the shared
  document, so the value is identical whoever starts the run. Precedence
  unchanged: workflow > reusable user secret > `GLOBAL_SECRET_*`.
- **Lazy one-shot migration** (`ensureSharedWorkflowSecrets`): when the
  shared document is empty, the requesting user's, the creator's and the
  listed owners' legacy per-user entries are decrypted, re-bound to the
  workflow, written to the shared document and removed from the per-user
  ones. An entry that cannot be decrypted (different `AUTH_SECRET`) is left
  in place and logged. Nothing to run by hand on any deployment.
- **Builder tools** (`secrets_tools.go`): `set_workflow_secret`,
  `delete_workflow_secret`, `list_secrets` use the shared store; the
  mutating tools were already withheld from read-only sessions (PLAT-262).
- **Frontend** (`SecretSelectionSection.tsx`, `api/secrets.ts`): add /
  delete / reveal disable for read-only users via
  `useCanWriteWorkflow(workflowPath)` (names remain visible); reveal sends
  `workspace_path`; the "Private" badge became "Shared".
- **Unchanged on purpose:** reusable user secrets (`_users/<uid>/secrets.json`)
  stay personal; per-user provider credentials (Claude Code / Cursor / Pi
  keys) stay personal; global secrets untouched.

## Verification

- `TestSharedWorkflowSecretsAreGatedByWorkflowAccess` — owner stores; the
  document lands under `_users/_shared/` and not under the owner; reader
  lists names without ciphertext; unrelated member gets 403 on every route;
  reader reveal 403, owner reveal returns the value; the caller-bound
  decrypt path cannot open a workflow-bound blob even for the owner;
  reader delete 403, owner delete succeeds.
- `TestLoadSelectedSecretsResolvesSharedWorkflowSecretsForEveryUserAndMigratesLegacy`
  — a pre-PLAT-272 per-user value stored by the owner resolves for a
  reader on first touch, is migrated into the shared document (workflow-
  bound) and removed from the owner's document; owner, reader and an
  identity with no record all resolve the same value; a workflow secret
  still beats a same-named reusable user secret.
- Both fail to compile with the shared-store helpers removed; the existing
  secrets / workflow-access / provider-credential tests and the
  `chathistory` package suite pass; `tsc` and `eslint` clean.
- Live reverify on RTS (Video Studio + AgentWorks box) after its next
  deploy: log in as a read-only account, confirm the `rtslatency` Secrets
  pane lists names with disabled controls, run a step that reads
  `$SECRET_*`, and confirm the agent log shows the one-shot
  `[SECRETS] migrated N workflow secret(s)` line — pending.

## 2026-09-03 live note (RTS)

Partial reverify. On RTS (release `18c0da5a9-20260903141144`) the Builder chat's
`set_workflow_secret` wrote to the shared store (`_users/_shared/workflow_secrets/`
holds one record) and re-attached the secret to the `rtslatency` manifest, and
the workflow's step shells resolve `$SECRET_*` from the Workshop's live map. Not
yet evidenced: the one-shot `[SECRETS] migrated N workflow secret(s)` log line
(no pre-existing per-user records may have needed migrating on this box) and the
read-only login's Secrets pane. The same-turn chat-shell gap found while checking
this is a separate defect, [PLAT-276](plat-276.md).
