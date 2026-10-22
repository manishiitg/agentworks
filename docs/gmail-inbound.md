# User Gmail-backed Crew inboxes

The company controls only `enabled` through `manage_agent_email` status/configure, or My Vault → Company app setup → Crew inboxes. `AGENT_INBOX_ENABLED=true` is the deployment default; the saved private setting overrides it. Users reuse their personal Google connection in My Vault and explicitly choose one with `manage_google_account` action=set_inbox_account. The website and MCP use the same owner checks and operation. Existing Google permissions, encrypted token store and gog sign-in remain unchanged.

All existing and future Crews owned by that user get aliases automatically from their selected Gmail address and stable Crew manifest identity. Renames preserve the alias. A separate storage identity includes owner and connection ID: changing owners or accounts cannot mix message or attachment IDs from different sources. The first rollout covers Crews only. Company mailbox selection, reserved receiving accounts, Cloud setup/consent flows, Pub/Sub callbacks and watch renewal are removed.

## Receiving

Four bounded workers check selected user mailboxes about every 60 seconds, once per mailbox rather than once per Crew. Gmail `messages.list` filters by that user's Crew aliases (exact `deliveredto:` and an overlapping `after:` window). Only returned messages are fetched; exact To/Delivered-To addresses are verified again before storing. No scan of unrelated personal message bodies occurs. Mail is deduplicated on disk, and a failed check preserves the position for retry. Each worker rechecks the selected owner/account and live Gmail read grant. Disconnection, disabled access or changing the selected account stops the old source. The adapter always runs gog read-only with sending blocked and never lends mailbox credentials to a Crew.

Inbox pages, MCP reads, Watcher snapshots and attachment downloads use the current Crew access checks. Attachment IDs must belong to a stored message in that exact owner/connection-bound inbox, and fetching rechecks the current source account. Stored Crew mail remains readable after disconnection; attachment downloads need that same authorized source. Other personal Gmail and existing sending/lent-account permissions remain unchanged.

Receiving only stores mail and wakes ordinary optional Python Watchers. It never executes an agent or replies. Watchers retain independent cursors, use existing published entry points and execute with their owner's current permissions. Email and attachments are untrusted data.

## UI and deployment

The Crew right panel shows Inbox: address, conversation list, plain message content, attachment download, Refresh and Ask AI. My Vault shows one selected Crew inbox account. The company control is a single on/off button. Local deployments construct no receiving store, workers or inbox endpoints and show no inbox controls.

The new private store is `gmail-inbound/user-agent-email.db`. The old company store is retired, never read or imported as personal mail. The one-off connection migration backs up the registry before removing reserved company receiver records, so old delivery credentials cannot become ordinary shared accounts. Existing personal Google records and tokens are retained. Previously created Google Cloud resources are no longer used; removing them is optional administrator cleanup, not required for receiving.

Verification should cover exact alias/source isolation, disconnection, existing Crew sharing, attachments and a real Python Watcher path. A real Gmail delivery must be proven with the stored inbox record; code/build checks alone do not prove Google consent or mailbox delivery.
