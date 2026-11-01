# WhatsApp Connector

WhatsApp is the thread-less, owner-paired channel: each account links one
phone number, and workflow selection happens in-chat via slugs
(`@list`, `@switch`, `@<slug>`, `@off`). There is no platform-level token
to share, so WhatsApp never needed the per-workflow connection registry
that Slack has — account scope is per owner, route scope is per message.

## Model

```text
WhatsAppServiceManager ("whatsapp")
  services[userKey]            the account's phone
        |  one WhatsAppService per account
        v
BotConversationManager (BotConnector; Capabilities: WorkflowProgress only)
```

- One person, one WhatsApp (2026-09-26): the account key is
  `whatsappUserKey(userID)` and its one phone's pairing state lives in
  `<baseDir>/<userKey>/session.db`. Restart reconnects from disk — no re-pair.
  Pairing is QR-based (`EnsurePairingQR` / `GetQR`); `IsPaired` gates
  readiness. Asking to pair another phone (`?device=next` once paired, or a
  named slot) is refused: unpair the linked phone first.
- Accounts could once link extra phones (`devices/<slot>/`). Startup logs
  each one out of WhatsApp and deletes it
  (`retireExtraWhatsAppDevices`); one that cannot be logged out right then
  keeps its files and is retried at the next start. A managed channel id
  minted for an extra phone (`<user>~<slot>|…`) names no service.
- `UnpairDevice` resets the account's pairing to a fresh one. The phone's
  label (`SetDeviceLabel`) is stored without a WhatsApp handshake.
- Access state (`whatsapp_meta` row) holds a 6-digit link code
  (auto-rotated, 24h expiry) and the bound-DM-chat list with last-seen times.

## Ingress

Every DM arrives with `IsMention=true` — a message to the paired number
always addresses the bot. The service pre-resolves owner identity
(`UserEmail`, `WorkspaceUserID`) and attaches the route before the manager
sees it:

- `PresetWorkflow`: the slug-selected `ChannelRoute` (`WhatsAppRouting` is
  `map[slug]ChannelRoute`), or nil for generic chat / default-profile turns.
- `PresetProfile`: set when the route names a product profile conversation.
- Voice notes are transcribed before they reach a conversation (on-device
  STT via `SetVoiceTranscriber`); if the model isn't installed the sender
  is asked to set it up instead of blocking on a download.

## Routing commands

- `@list` shows candidate workflows; `@switch <number|name> [run|workshop]`
  and a direct `@<slug>` select one; `@off` drops back to generic chat.
  Selection persists per chat until changed.
- Modes are `run` (execute pinned) or `workshop`; the mode is part of the
  route key, so switching modes is a conversation boundary.
- Access is re-checked per message against the paired owner
  (`workflowRouteAllowed` → `WhatsAppWorkflowAccessFunc`): routes are
  user-scoped, and a saved slug never confers access by itself.

## Sessions and identity

- Thread-less: the chat JID is the thread. Replies continue the bound
  conversation within the 1-hour idle window; past it, a new conversation
  starts with a short preamble from the old one.
- The chat → session pointer persists in the account's store
  (`botSessionBindingStore`), filtered by route key on load, so a restart
  restores the conversation but never a different workflow's.
  Route-change isolation (P1) and the full lifecycle live in
  `bot_connectors_combined.md`.
- Turns run as `bot_owner`: the sender was authenticated at ingress
  (pairing ownership + per-slug access), so the turn executes as the paired
  owner with nothing further to revalidate. See "Bot principals" in
  `slack_connections.md`.
- Capabilities declare `WorkflowProgress` only: no threads, streaming,
  reactions, message edits, or history reads.

## Key files

| Area | File |
|---|---|
| Manager, pairing, unpair, extra-phone retirement | `agent_go/cmd/server/services/whatsapp_manager.go` |
| Service, QR, slugs, ingress, bindings, link codes | `agent_go/cmd/server/services/whatsapp_service.go` |
| Voice wiring, access func, profile router | `agent_go/cmd/server/server.go` |
| Lifecycle, route isolation, mention policy | `agent_go/cmd/server/services/bot_connector.go` |
| Principal model (`bot_owner`) | `docs/core/slack_connections.md` |
