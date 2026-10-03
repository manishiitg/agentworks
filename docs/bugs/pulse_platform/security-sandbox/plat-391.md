# PLAT-391 — Gmail sender widening needs separate owner consent

Status: implemented and locally validated; deployment pending.

## Finding

Sender allowlists introduced by ad2da5f23 replaced the owner-only authorization
rule. An interactive owner tool context allowed an agent to write extra senders
without a human confirmation. Untrusted content could induce this, then verified
mail from those senders could execute with the owner's target and send replies.
Common public mailbox domains were accepted as whole-domain grants.

## Decision and fix

- Builder proposes configuration; only the target owner's signed-in browser
  can approve or revoke additional senders. There is no approval tool or writable
  approval flag. Agent bridge/PAT/CLI/MCP/bot contexts cannot confirm.
- Private SQLite receipts bind owner/target/workspace/mailbox, filters, rule
  actions, replies and enabled state. Configuration edits revoke the receipt;
  restoring an earlier configuration cannot revive it. Cosmetic trigger renames
  preserve it. Confirmation checks the current digest transactionally.
- Non-owner admission, queued dispatch and email replies require current receipt
  plus existing sender authentication, filters, owner/target/connection access.
  Old lists without receipts fail closed; owner mail retains its existing path.
- Reject whole public suffixes and common shared mailbox domains; exact mailbox
  addresses remain valid. This denylist is defense in depth, not exhaustive.
- Incoming email stays read-only for configuration, with an explicit owner
  security acknowledgement/approval and revocation. Builder reports pending
  consent; mailbox watch readiness alone does not mean external senders can run.

## Verification

Private receipt persistence/revocation, stale proposals, ownership, scope and
configuration binding, forged internal owner contexts, agent approval attempts,
common/per-rule domain policies, queued dispatch and UI acknowledgement.
No live mailbox test or deployment in this change.

Package and scoped server regressions passed with the race detector. All 16
Gmail pane tests, frontend type checking and production/release build checks
passed. The provider-domain denylist remains deliberately non-exhaustive; the
private owner confirmation is the authority boundary for every extra sender.
