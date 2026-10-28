# User Gmail-backed Crew inboxes

Crew inboxes are available by default on server deployments, with one receiving setting per Crew. There is no company email switch or receiving configuration file. Users reuse their personal Google connection in your project's Vault panel or in chat and explicitly choose one with `manage_google_account` action=set_inbox_account. The website and MCP use the same owner checks and operation. Existing Google permissions, encrypted token store and gog sign-in remain unchanged.

Incoming email is on by default for each Crew (`email_inbox_enabled` in its product manifest; omitted means on and explicit false stays off). The owner enables or disables it through Builder `configure_agent_inbox` or MCP `crew_integrations` action=configure_agent_inbox with arguments.enabled. Only enabled Crews receive aliases from their selected Gmail address and stable Crew manifest identity. Selecting the inbox account starts receiving for that owner’s enabled Crews. It never creates a Watcher. Disabling retains stored mail and removes that address from receiving; with no enabled Crews or personal Gmail Watchers the mailbox poller is removed. Renames preserve the alias. A separate storage identity includes owner and connection ID: changing owners or accounts cannot mix message or attachment IDs from different sources. The first rollout covers Crews only. Company mailbox selection, reserved receiving accounts, Cloud setup/consent flows, Pub/Sub callbacks and watch renewal are removed.

## Receiving

Four bounded workers check selected user mailboxes with enabled Crews about every 60 seconds, once per mailbox rather than once per Crew. Gmail `messages.list` filters by that user's Crew aliases (exact `deliveredto:` and an overlapping `after:` window). Only returned messages are fetched; exact To/Delivered-To addresses are verified again before storing. No scan of unrelated personal message bodies occurs. Mail is deduplicated on disk, and a failed check preserves the position for retry. Each worker rechecks the selected owner/account and live Gmail read grant. Disconnection, disabled access or changing the selected account stops the old source. The adapter always runs gog read-only with sending blocked and never lends mailbox credentials to a Crew.

Inbox pages, MCP reads, Watcher snapshots and attachment downloads use the current Crew access checks. Attachment IDs must belong to a stored message in that exact owner/connection-bound inbox, and fetching rechecks the current source account. Stored Crew mail remains readable after disconnection; attachment downloads need that same authorized source. Other personal Gmail and existing sending/lent-account permissions remain unchanged.

Receiving only stores mail and wakes ordinary optional Python Watchers. It never executes an agent or replies. Watchers retain independent cursors, use existing published entry points and execute with their owner's current permissions. Email and attachments are untrusted data.

## UI and deployment

The Crew right panel shows Inbox: address, conversation list, plain message content, attachment download, Refresh and Ask AI. your project's Vault panel shows one selected Crew inbox account. Ask AI changes only the current Crew’s receiving setting through Builder; MCP uses the same operation. Local deployments construct no receiving store, workers or inbox endpoints and show no inbox controls.

The new private store is `gmail-inbound/user-agent-email.db`. The old company store is retired, never read or imported as personal mail. The one-off connection migration backs up the registry before removing reserved company receiver records, so old delivery credentials cannot become ordinary shared accounts. Existing personal Google records and tokens are retained. Previously created Google Cloud resources are no longer used; removing them is optional administrator cleanup, not required for receiving.

Verification should cover exact alias/source isolation, disconnection, existing Crew sharing, attachments and a real Python Watcher path. A real Gmail delivery must be proven with the stored inbox record; code/build checks alone do not prove Google consent or mailbox delivery.


### Watch your existing Gmail

Ask your Crew Builder to watch your personal Gmail and send only matching content to your Crew chat. It uses the same Watcher setup: check_mode=inbox, email_source=personal_gmail, with Python supplied through script_content. You must own the Crew and select your own Gmail inbox account in your project's Vault panel or in chat with read access. Server deployments only. The Crew’s dedicated address may be on or off independently; creating this Watcher does not change that setting.

It watches new received mail, including archived arrivals, about every60seconds; it excludes sent mail and drafts and does not import old mail. Private message bodies exist only in the temporary detector snapshot, up to20 at a time, then are deleted. Message identities and the Watcher cursor stay private; personal mail is never listed in the Crew Inbox. Only the event payload chosen by your Watcher is sent to your chat or published function. Use target kind=agent and a message for chat; functions must be declared entry points. Changing the selected Gmail requires a new Watcher.
