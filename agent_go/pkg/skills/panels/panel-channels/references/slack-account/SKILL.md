# Slack Account

Use `integrations action=slack_list_saved` to inspect your saved accounts and the account selected for this item. Choose a saved account with `slack_attach`; `slack_detach` removes it only from this item. `slack_connect` and `slack_reconnect` return a browser sign-in link that the account owner finishes. `slack_disconnect` removes the saved account everywhere.

Shared runs use the selected account under the current product access and account grants. Keep the existing selection when it already meets the request. Verify the saved selection before reporting success. Never repeat account credentials.

Read `references/operations.md` for the service procedures. Use only the actions and arguments offered by the current tool catalog.

The Slack page has two tabs: **My Slack account** sends as the person; **Slack bot** lets people talk to the project as a bot. My Slack account combines saved-account and Use here controls in one view. A saved personal sign-in is still separate from lending it to this project. Local installs call administrator setup **Slack sign-in setup**, not company sign-in. If sign-in setup is missing, help the administrator configure it in the current chat. Keep account-owner, selected-item and Builder checks.
