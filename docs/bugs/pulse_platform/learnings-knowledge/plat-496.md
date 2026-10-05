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
access tokens, with name, expiry, copy and revocation. There is no permission
picker: local_full_access makes the server derive all scopes available to the
local account, with unrestricted connection caps but live folder/account grants.
The request is forbidden for multi-user servers or a foreign local identity.
Hosted and multi-user setup retains OAuth. Vault’s separate gateway retains its
OAuth path; this change is for the global MCP endpoint used by KB and other
platform products. Tokens remain in component memory only and are never placed
in generated example URLs or config. Backend issuance/admission/revocation and
16 focused frontend tests plus TypeScript validation pass.
