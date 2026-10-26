# Saved Google accounts

List saved accounts and reuse the requested login. Integrations → Google apps owns the saved
connection; Use here/Remove here selects it for this project through
integrations attach/detach. Connect or reconnect returns the actual
provider sign-in link; do not create another MCP connection for Gmail.
Removing the selection leaves other projects connected. Disconnect removes the
saved account everywhere. Current account ownership and saved grants apply.

For Crew inbox selection, use set_inbox_account on the requested owned,
connected account with Gmail read access. An older private account can be
selected here while its stored personal selection is missing. If the tool
reports Choose your own account despite verified ownership, reapply attach to
the same account here, retry set_inbox_account, then read back the result.
Use here records that explicit personal selection without another login.
Never edit account flags in files or substitute a different account.
