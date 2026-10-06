[← browser / browser](index.md)

# PLAT-624: Code side chats cannot share an extension browser controlled by another project chat

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | browser |
| Area | browser |
| Summary | Code side chats share a project extension binding, but its persistent controller permits only the first chat. |

## What happened

The owner asked whether the multiple Code chat tabs added in
[PLAT-571](../../code/frontend-chat/plat-571.md) cause browser issues.
Reviewed against main `2dd249b9c`, including the five-chat UI change
`956d4d177`. This issue is confirmed by code inspection, not a live Code UI run.

- Each side chat resolves its own conversation/session in the same project
  directory (`resolveProductProjectBindingWithStore`). Browser isolation is
  project-scoped, so each chat finds the same account/project extension binding.
- `handleExtensionBrowser` passes its trusted workflow root or chat session ID
  to `AcquireForActive`. Separate Code chats have separate controller IDs.
- `AcquireForActive` permanently assigns `b.owner` on the first page command.
  Releasing its tool gate does not release that assignment. Another chat is
  rejected with `CHROME_EXTENSION_BUSY`, including after the first chat's tool
  call has completed. The existing relay test explicitly checks that rejection.
- The connection status can still show connected because it reports the live
  project socket rather than whether the current chat may control it.

This is a compatibility gap with the new multi-chat feature, not evidence of a
cross-account grant or commands interleaving. The rejection protects cached
element references and the one native CDP controller.

## Fix

None implemented during this review. Do not remove the owner check or make all
chats impersonate the primary: that would let one chat reuse another's selected
tab and snapshot references.

The managed workspace-browser path already has per-conversation tab routing
with selection and command execution under the shared browser automation lock.
Direct CDP has its own conversation tab routing. Extension mode returns through
its separate executor and does not reuse those protections.

## Left

- Decide and implement safe multi-chat extension routing: separate conversation
  tab/ref state within the authorized account/project grant, with serialized
  tool actions and a defined lifetime for native CDP clients and recordings.
- Preserve workflow step/delegate browser handoff, account isolation, opt-in
  visible activation, screenshot grants and recording ownership.
- Verify through a real extension and two Code chats: each navigates and takes
  snapshots of its own tab, switching the UI does not move the other chat's
  selection, and stale references cannot act on the other chat's page.
- Expose which chat can control the connection, or make that availability clear
  when the current chat cannot use it.

Until then, use one Code chat to control a given extension connection. Separate
Code projects still have separate project browser bindings. Shared-file writes
between Code chats are a different known limitation tracked in PLAT-571.
