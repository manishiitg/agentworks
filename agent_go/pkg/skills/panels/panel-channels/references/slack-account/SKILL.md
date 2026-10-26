# Slack Account

Use `integrations action=slack_list_saved` to inspect your saved accounts and the account selected for this item. Choose a saved account with `slack_attach`; `slack_detach` removes it only from this item. `slack_connect` and `slack_reconnect` return a browser sign-in link that the account owner finishes. `slack_disconnect` removes the saved account everywhere.

Shared runs use the selected account under the current product access and account grants. Keep the existing selection when it already meets the request. Verify the saved selection before reporting success. Never repeat account credentials.

Read `references/operations.md` for the service procedures. Use only the actions and arguments offered by the current tool catalog.
