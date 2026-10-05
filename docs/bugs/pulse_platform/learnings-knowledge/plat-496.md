[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-496 — Knowledge Base review: migration authority, consumer cutover and access confirmation

| Coordination | Value |
|---|---|
| State | fixed in PR #268; merge and deploy pending |
| Date | 2026-10-05 |
| Owner | learnings-knowledge |

## Problem

Migration dispatched before managed runtime restrictions. Preview listed legacy
consumers but cutover did not use that list. All agents received KB schemas and
Work/Code had ambient KB guidance. Access chat executed grants immediately,
leaving untrusted names as a prompt-injection risk. OAuth lacked KB scopes;
missing session policy admitted ambient grants; empty identity snapshots could
disable everyone; configured external KB roots lacked explicit CLI protection.

## Fix

- Migration is restricted to authenticated external source-owner connections,
  requires source builder authority for all stages, and is absent from ordinary
  schemas. Managed calls cannot bypass this gate with an action argument.
- Cutover inventories current consumers and refuses remaining legacy aliases;
  owners rebind them after import and before cutover.
- Register content tools only for bound workflow/Crew projects, pin execution to
  server sessions, fail closed on missing policy, and use dynamic guidance.
- Access mutations become fixed-argument, private proposals; only the same
  interactive person's app confirmation executes them with fresh ACL/manifest
  and authority checks. Agents cannot approve proposals.
- Add opt-in OAuth KB scopes, require read for write; protect configured KB roots
  in both CLI confinement policies; refuse empty multiuser directory sync.
- Rebase onto origin/main f610f5316, preserving current account-product and
  scheduled-role policy semantics. Record content ACL/admin/archive decisions.

## Validation

Focused server regressions exercise migration rejection, missing-policy denial,
new consumers, bound registration, frozen confirmation, cross-user/execution
principal rejection, OAuth pairing and configured-root protection. Existing
bridge coverage now explicitly confirms the proposal before reading the grant.
Frontend confirmation test checks untrusted labels render as text and only an
explicit click submits the proposal ID. All focused server KB/OAuth/product/retained-policy checks passed. Full domain,
agent-profile and workflowkb race tests passed; all product/common package
tests passed. Frontend: 32 focused tests, `tsc -b` and Vite production build
passed. Server KB race tests also passed.

## Remaining

Merge/deploy and a deliberately authorized production pilot. No production
migration performed. Git remote operator hardening remains a separate follow-up;
this PR keeps the configured backup remote and explicit commit/push semantics.

## Owner simplification: remove Activity

Removed the Activity view/component, viewer client, backend route and internal
activity operation. Content/access/commit/push/reconciliation no longer create
activity records or an activity directory. Existing files are not deleted.
Recovery journals, request outcomes and backup receipts remain intact. Full
domain race tests and focused server/frontend checks validate this removal.

## Owner simplification: remove dedicated Connect

Removed the KB Connect tab, panel and panel-specific tests. The app now has
Library and Access; connection management uses the existing global MCP/OAuth
flow and shared endpoint. No additional MCP server is needed. KB scope and
folder authorization remain enforced. TypeScript and focused KB frontend checks
validate the remaining views.

## Owner correction: reuse Vault’s platform UI

KB uses Vault’s shared chat tab, standard compact ChatArea composer/rendering,
ProductChatLandingCard and WorkspaceSplitRail. The custom model strip is removed;
Models is a workspace toolbar view using the existing WorkModelsPanel, without
folder navigation. New chat rotates the server-owned profile conversation and
marks the previous conversation view-only. Access-only capabilities, folder
context and confirmation remain enforced.

Validation: TypeScript project build and 22 focused frontend tests passed,
including conversation rotation, standard composer configuration and Models
placement. The running local app shows the shared composer and provider/account/
model/reasoning settings in the workspace Models view.

## Local MCP authentication

Shared global Connect detects a verified single-user loopback instance and offers
access tokens, with a fixed `agentworks-local` name, copy and revocation. Local full-access tokens have no automatic expiry. There is no permission
picker: local_full_access makes the server derive all scopes available to the
local account, with unrestricted connection caps but live folder/account grants.
The request is forbidden for multi-user servers or a foreign local identity.
Hosted and multi-user setup retains OAuth. Vault’s separate gateway retains its
OAuth path; this change is for the global MCP endpoint used by KB and other
platform products. Tokens remain in component memory only and are never placed
in generated example URLs or config. Backend issuance/admission/revocation and
16 focused frontend tests plus TypeScript validation pass.

Owner simplification: remove local expiry selection. Local tokens remain valid
until removed, including across restarts. Ordinary scoped tokens and hosted
OAuth retain their existing expiry behavior. Store tests verify authentication
ten years later, persistence and removal; server checks reject permanent local
tokens after switching to multi-user mode.

Local token name is fixed to `agentworks-local` in the UI and server issuance; the name input is removed.

## Owner refinements: direct MCP access, shared Files and backup setup

Unrestricted writable external connections now list/grant/revoke access directly,
with live Owner checks, expected ACL version, stable request ID, and token
revocation checks. Service accounts and backup setup require an administrator.
Managed workflow/Crew and capped content connections remain content-only. App
chat still uses frozen proposals and confirmation.

KB uses the shared Files pane, tree, open tabs, breadcrumbs, content viewer and
in-file search through a read-only KB data source. The custom library/reader and
tag/type filters are removed. Folder access uses the shared Ask AI action in the
Files header; all content remains protected by KB APIs. External images and
workspace navigation in shared Markdown are blocked.

Unconfigured backup appears once above ChatArea. Configure backup opens the same
builder and saves an initial SSH remote and branch through the existing access
tool, without adding an MCP tool. Configuration is private, durable and respects
deployment overrides; it cannot redirect existing backup history. Setup does not
contact the remote or commit/push. Local and generated MCP skills include KB
instructions, access authority and local token/hosted OAuth guidance.

Validation: full KB domain race suite and focused server KB/access/backup/skill
race checks passed. TypeScript, Vite production bundling and 27 focused frontend
tests passed, including the existing Files tree/Git regressions, read-only KB
loading, revoked content/tab removal, Ask AI routing, and banner refresh without
conversation rotation. The local browser opened a real KB entry in the shared
Files viewer. No production migration or Git publication performed.

Files highlight regression: absent `highlightedFile` and `originalFilepath`
previously compared equal, highlighting every KB row in blue. Require a nonempty
highlight target before matching either path. Shared tree regression verifies
no unsolicited highlights and exactly one explicit target.

## Files Git follow-up — 2026-10-05

Reuse the existing Files Git controls, line decorations and server read/action handlers through a scoped KB adapter. Add whole-repository authority checks and immutable private Git generations; successful pull/branch/stash/discard imports journal live Markdown and registries together. Keep root folder grants and surviving entry metadata. Plain Markdown only, bounded UTF-8 validation, clean-tree protection, fast-forward pull and lease-protected push. Persist uncertain push intents and bind/invalidate receipt branches. Add the `git` action to the existing backup MCP name; managed/scoped content connections retain their existing selected receipt flow.

Validation: real bare-repository domain tests for pull, branch switch, stash/restore, commit/push, scoped Reader visibility, invalid-tree atomic rejection and root/cap authority; server tests exercise shared Files handlers and the five-tool MCP boundary; frontend tests verify shared Source Control uses KB endpoints and disables root-reader writes. Existing Files Git/store tests and frontend typecheck are part of the final checks.

## External MCP setup and access dispatch — 2026-10-05

The external catalog's validation schema still used content/migration-only
definitions even though per-connection discovery advertised direct access,
backup setup and repository Git actions. Generate its validation schema from
the full external surface, keeping per-connection discovery and runtime
Owner/admin/scope checks. No new MCP tool names or permissions are added.

A Streamable HTTP regression exercises non-admin setup rejection, direct admin
setup/retry, destination pinning, Reader-to-Editor grants, immediate shared edits,
Editor access-management rejection, subtree isolation and immediate revocation.
The operations guide now documents actual external actions and clarifies that
private Git authentication supports a KB-owned encrypted PAT for HTTPS;
existing SSH backups retain host SSH credentials. Setup asks for repo URL,
username and optional PAT. The app uses the shared secret input in the existing
confirmation card; proposal storage encrypts supplied PATs and responses redact
them. Credentials do not enter Git config or content. No Vault dependency.
Validation includes real HTTPS Git authentication for receipt push and Files
pull, encryption/restart/rotation/removal checks, and masked app proposal flows.


2026-10-05: Added workflow KB resource selection across global MCP, root Builder and Attached folders UI, using Vault's selection and root authority patterns. Project binding actions remain within manage_knowledgebase_access (five MCP tool names). Every external project action requires appropriate Builder/Crew authoring bounds as well as unrestricted KB write authority; project ownership, current folder access, output audience and manifest CAS apply to every surface. Added default scoped step content tools and preserved the actual child session for authorization. UI uses existing Checkbox/Button/Ask AI controls, reports conflicts/audience errors, and performs no implicit grants. Verified actual MCP binding, Builder and child isolation, UI selections, step read/write/none and denied writes, with race-enabled backend checks.
