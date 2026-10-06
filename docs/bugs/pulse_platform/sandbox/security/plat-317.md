[← platform / security-sandbox](index.md)

# PLAT-317 — Builder user management with live owner/admin enforcement

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | sandbox |
| Area | security |
| Summary | Interactive workflow Builder receives `manage_user_access` and a workshop-only |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | Implemented and regression-tested; RTS release verification pending |
| Last synchronized | 2026-09-12 |

Interactive workflow Builder receives `manage_user_access` and a workshop-only
user-management reference in the builder skill bundle. Admission is declared in
AgentWorks chat policy. Run, scheduled, Pulse, child, bot and notification
execution do not receive the capability. Tool visibility is not authority:
every invocation checks current caller permissions and disabled state.

Owners/admins can inspect or replace workflow sharing and resolve user IDs.
Non-owners receive errors. Admins additionally list full account metadata,
create accounts and update accounts. Mutations reuse the existing UI handlers,
including last-owner preservation, user resolution and self-lockout checks.
Password values/hashes are not returned. No live users or access grants were
changed during implementation verification.

The guidance explains shared KB audience checks: the consumer's entire
owner/reader audience must be allowed on the source. Personal admin access alone
does not authorize distribution to a broader audience. Inspect both workflows,
identify the mismatch and apply only user-requested sharing changes; do not
bypass the policy by editing credential/access files through a shell.

Tests cover owner/admin success, non-owner errors, invalid paths, last-owner
protection, account metadata without passwords, admin demotion, builder-only
admission and the absence of the management reference in Run.

Related: [PLAT-262](plat-262.md), [PLAT-310](../../brain/learnings-knowledge/plat-310.md)).

## 2026-09-12 deployment confirmation

The latest follow-up above is included in RTS release `3a37a1c-20260912143119`
(app `3a37a1c75`). Source revisions and all three services were verified after
activation. The configured-environment Linux sandbox regression passed.
Earlier pending-deployment notes are superseded; feature-specific live acceptance
limits remain as documented. No user accounts/sharing or notification recipients
were changed during verification.
