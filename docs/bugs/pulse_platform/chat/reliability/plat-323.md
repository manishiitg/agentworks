[← chat / reliability](index.md)

# PLAT-323 — workflow-builder chats stay inside their workflow but are isolated by authenticated user

| Field | Value |
|---|---|
| State | deployed |
| Priority | P0 |
| Product | chat |
| Area | reliability |
| Summary | P0 — a chat owned by one account was visible to another user, |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented, deployed, and user-verified on Confida` |
| Last synchronized | `2026-09-16` |

- **Priority:** P0 — a chat owned by one account was visible to another user,
  while moving chats outside the workflow would break Builder and cross-CLI
  access to workflow-relative context.
- **Category:** Chat Reliability, with a security boundary owned jointly by the
  chat-history resolver and Folder Guard.
- **Implementation:** `4b9c694a1` (`Isolate workflow builder chats by user`),
  followed by `1ea884404` (`Canonicalize linked SSO user identities`).

## Product contract

1. Builder chats remain below the workflow they belong to:
   `Workflow/<workflow>/builder/conversation/users/<internal-user-id>/<date>/`.
   Builder and resumed coding CLIs therefore retain normal workflow-relative
   file access; no inaccessible external chat path is injected into context.
2. Listing, restore, resume and direct-path reads resolve only the authenticated
   user's namespace. Folder Guard blocks sibling user directories even when two
   people can access the same workflow.
3. Account identity is canonicalized before the path is selected. The owner
   account `<a member's email>` and the read-only
   `manish@confida.ai` account do not share an internal user ID or chat folder.
4. Unattributed legacy files are never silently assigned to the current viewer.
   They move to `conversation/system/` until an explicit owner mapping is
   supplied, avoiding both cross-user disclosure and misleading ownership.
5. Work/Crew product storage is unaffected: the migration scans workflow
   Builder paths only and does not rewrite Crew or `_users` product data.
6. The native single-user launcher is a deliberate exception to legacy
   quarantine: its authenticated owner is `default`, so
   `run_server_with_logging.sh` idempotently claims legacy Builder chats into
   `users/default` before startup. Multi-user deployments still require an
   explicit owner map and continue to quarantine ambiguous records.

## One-time migration

`scripts/migrate_workflow_builder_chats.py` is dry-run by default and is invoked
by deployment with `--apply` after an owner map has been reviewed. It moves
legacy date folders into the appropriate `users/<id>/` namespace, updates the
chat index atomically, preserves runtime/resume metadata, and is idempotent when
rerun. Ambiguous sessions are quarantined as System/legacy instead of being
shown to a random signed-in user.

## Verification

- Migration unit tests cover mapped, embedded-owner, index-owner, ambiguous,
  idempotent and conflicting-destination cases.
- Server tests cover per-user path generation, list/restore filtering and
  Folder Guard denial of sibling user chat directories.
- Confida identity correction was live-verified by the user: the owner account's
  chats returned, while the distinct read-only account remained separate.

## Supersession rule

New defects involving chat ownership, durable message history, resume/restore,
native transcript reconciliation or chat-directory migration belong in the
`chat-reliability` category. Pure presentation defects that do not affect the
conversation record remain in `frontend-chat`; coding-CLI transport mechanics
without persistence impact remain in `coding-agent-bridge`.
