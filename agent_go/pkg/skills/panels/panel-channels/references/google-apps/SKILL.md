# Google Apps

Use `integrations action=google_list_saved` to inspect your saved accounts and the account selected for this item. Choose a saved account with `google_attach`; `google_detach` removes it only from this item. `google_connect` and `google_reconnect` return a browser sign-in link that the account owner finishes. `google_disconnect` removes the saved account everywhere.

Shared runs use the selected account under the current product access and account grants. Keep the existing selection when it already meets the request. Verify the saved selection before reporting success. Never repeat account credentials.

Read `references/operations.md` for the service procedures. Use only the actions and arguments offered by the current tool catalog.
