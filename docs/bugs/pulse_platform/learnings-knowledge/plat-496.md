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
