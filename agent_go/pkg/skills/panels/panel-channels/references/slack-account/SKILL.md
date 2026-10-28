# Slack Account

Use `integrations action=slack_list_saved` to inspect your saved accounts and the account selected for this item. Choose a saved account with `slack_attach`; `slack_detach` removes it only from this item. `slack_connect` and `slack_reconnect` return a browser sign-in link that the account owner finishes. `slack_disconnect` removes the saved account everywhere.

Shared runs use the selected account under the current product access and account grants. Keep the existing selection when it already meets the request. Verify the saved selection before reporting success. Never repeat account credentials.

Read `references/operations.md` for the service procedures. Use only the actions and arguments offered by the current tool catalog.

The Slack page shows two connection statuses together: **Let people message this project** for the bot, and **Use Slack as me** for personal sign-in. A saved bot does not complete personal sign-in, and a missing personal account does not mean the bot is missing. My Slack account combines saved-account and Use here controls in one view. A saved personal sign-in is still separate from lending it to this project. Administrators can complete **Set up Slack sign-in** directly on the page, using the same encrypted app store as sign_in_apps in chat/MCP. If setup is missing, check saved bots as well as accounts and sign-in apps before claiming anything is absent. Reuse an existing internal Slack app where supported; its bot/app tokens are different from its client ID/secret. Use the callback and required scopes returned by the platform; do not guess addresses or change the authentication flow. Keep account-owner, selected-item and Builder checks.
