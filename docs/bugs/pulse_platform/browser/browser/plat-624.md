[← browser / browser](index.md)

# PLAT-624: Code side chats cannot share an extension browser controlled by another project chat

| Field | Value |
|---|---|
| State | fixed on main |
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

- Code uses a server-registered private client for each trusted root chat within
  the authorized account/project binding. Each client gets a separate native CLI
  session, CDP capability, selected targets, element-reference cache and recording
  lease. The project tool gate still serializes complete actions.
- Extension 0.4.6 routes requests/replies and flattened page events by client ID,
  discards late disconnected-client replies, and verifies target/session ownership.
  Manually shared tabs are claimed on first attach. Other chats create their own
  tabs. The account code and project tab group remain shared.
- Client IDs are stable server-derived routing labels; private capabilities and
  native sessions are fresh on reconnect. Ownership/created flags survive network,
  worker and server reconnect in trusted session storage, never browser restart.
- Closing an idle Code side chat calls the authenticated Stop endpoint before
  dismissing its UI; a working chat still must be stopped before closing.
- Stop/clear releases only the chat capability and native runtime, discards its
  unfinished recording, closes its agent-created tabs and unshares manual pages.
  It leaves the other chats, account pairing and project socket live.
- `status` distinguishes this chat's `shared_tabs` from `project_shared_tabs`.
  Existing Crew/workflow root handoff, account authorization, background default,
  screenshot guards and headless-limit exemptions remain enforced.
- Older extensions refuse Code page actions with an explicit 0.4.6 reload message.

## Verification

- Real Chrome 154.0.4258.53 with the unpacked 0.4.6 extension, guarded workspace
  shell and native agent-browser 0.38.2: PASS (43 s). Two Code chats kept their
  own tabs, selection and saved references; foreign physical targets were refused.
  A delegate retained its parent chat's client. Both chats recorded independent,
  nonempty decodable WebMs concurrently; releasing B left A and the project live.
  B reconnected first after server restart without receiving A's page. Closing a
  restored chat before it re-registers its native client also released only its
  own agent-created tabs.
- Existing real-path checks passed: workflow step and paused cross-site iframe
  handoff, Code/Crew/account separation, screenshots, interrupted-take rejection,
  headless-limit exemption, background focus/explicit activation, delayed reply
  rejection, failed-init recovery, browser restart and account reset.
- `go test -race ./pkg/browserrelay`: PASS. Live sockets route identical request
  IDs only to the originating private client and revoke one without stopping the
  other; an older extension refuses Code page actions explicitly.
- `go test ./pkg/browser ./pkg/browserrelay`: PASS.
- Five server regressions covering project isolation, workflow runtime/HTTP
  account binding and session ownership: PASS.
- Frontend `tsc -b` and WorkSurface/chat-selection tests (12): PASS.
- JavaScript syntax, embedded ZIP byte equality and ticket index/check: PASS.

## Rollout

Use Browser Bridge 0.4.6 with the updated server. The download ZIP is included.
Local source checkouts are fast-forwarded after push; running servers need their
normal restart/rebuild. RTS deployment is separate and has not been performed.

Shared-file writes between Code chats are independent of browser tab isolation
and remain tracked in PLAT-571.
