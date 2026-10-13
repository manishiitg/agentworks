# Account access with MCP

Administrators with existing `users:manage` consent can use `users` to inspect
and repair an existing account's product and create permissions.

```json
{"name":"users","arguments":{"action":"list"}}
{"name":"users","arguments":{"action":"get","user_id":"user-example"}}
{"name":"users","arguments":{"action":"update","user_id":"user-example","role":"creator","products":["code","relays"],"create_products":["code"],"request_id":"access-example-1"}}
```

Use IDs from list. Role IDs are the app's existing values; Builder/Run behavior
still follows the same product access. Updates also support `disabled`,
`code_reviewer` and `vault_reader`. Omitted fields are unchanged; an empty
`create_products` allows creation in none, and `clear_create_products: true`
restores the role default. Do not combine that flag with `create_products`.

Changing administrator access requires `confirm` equal to `user_id`. The shared
handler refuses self-demotion, self-disable and removing the last active admin.
Successful updates save a durable `request_id` receipt with the directory: retry
the identical call within seven days for the recorded result; use a new ID for a
different change. During that window retries cannot reapply settings over a
later change, and changed arguments with the same ID are refused. Receipts expire
after seven days and are pruned on directory saves; an expired ID is a new update,
so inspect current access before resending an old call. Current admin authority
and consent are checked again before returning a receipt. Reads are labelled
`user_directory_read`. Every new mutation records `update_user_attempt` with its
caller and token before saving; an unavailable audit prevents the change. A save
failure records `update_user_failed` when audit is available. Successful writes
also appear in the app's existing user-change log after saving.

Responses include identity, sign-in method, role, products, create permissions,
status and review flags. This tool does not expose credentials or perform account
provisioning. The app handler currently has no revision/ETag parameter.
