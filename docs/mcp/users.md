# Account access with MCP

Administrators with existing `users:manage` consent can use `users` to inspect and manage existing accounts. The People & access page shows account
status and permits basic role, product and enable/disable updates. Use admin MCP
for creation permissions, review access, AI limits, model restrictions, password
resets and account deletion.

```json
{"name":"users","arguments":{"action":"list"}}
{"name":"users","arguments":{"action":"get","user_id":"user-example"}}
{"name":"users","arguments":{"action":"update","user_id":"user-example","role":"creator","products":["code","relays"],"create_products":["code"],"request_id":"access-example-1"}}
```

AgentBridge, Brain and Vault are available to every account on servers that run them. `shared_products` lists these services; `products` lists the remaining assignable products. Changing a person’s product list never revokes shared-service access. Brain folder roles, Vault grants, private accounts and agent publication still govern their resources.

Use IDs from list. Role IDs are the app's existing values; Builder/Run behavior
still follows the same product access. Updates also support `disabled`,
`crew_reviewer` and `vault_reader`. Omitted fields are unchanged; an empty
`create_products` allows creation in none, and `clear_create_products: true`
restores the role default. Do not combine that flag with `create_products`.

Changing administrator access requires `confirm` equal to `user_id`. The shared
handler refuses self-demotion, self-disable and removing the last active admin.
Updates and deletions save a durable `request_id` receipt with the directory: retry
the identical call within seven days for the recorded result; use a new ID for a
different change. During that window retries cannot reapply settings over a
later change, and changed arguments with the same ID are refused. Receipts expire
after seven days and are pruned on directory saves; an expired ID is a new update,
so inspect current access before resending an old call. Current admin authority
and consent are checked again before returning a receipt. Reads are labelled
`user_directory_read`. Every new update records `update_user_attempt` with its
caller and token before saving; an unavailable audit prevents the change. A save
failure records `update_user_failed` when audit is available. Successful writes
also appear in the app's existing user-change log after saving.

Responses include identity, sign-in method, role, products, create permissions,
status and review flags. This tool does not expose credentials or perform account
provisioning. The app handler currently has no revision/ETag parameter.

## Detailed administration

- `users action=update`: `create_products`, `clear_create_products`,
  `crew_reviewer` and `vault_reader` retain their existing rules.
- A password reset uses `users action=update` with `password` supplied by the
  person and a fresh `request_id`. Never repeat or log its value. The server
  stores its hash; neither account responses nor retry/audit records contain it.
  Company sign-in is normally managed through the company's sign-in provider.
- `users action=delete`, with `user_id` and a fresh `request_id`, removes only
  the account record. Its files remain. Self-deletion and deleting the last
  active administrator remain refused. Deleting an admin requires the existing
  `confirm=user_id`. Identical retries return the recorded deletion;
  `delete_user_attempt` / `delete_user_failed` record the caller and request ID.
- Discover `account`'s current schema: `action=usage` inspects shared-account
  use and effective limits/models, `action=set_limits` changes token limits,
  and `action=set_allowed_models` changes model restrictions. These are the
  existing admin operations, with the same consent and audit records.

The in-app Builder's `manage_user_access` retains admin-only password updates
and supports `delete_user` through the same handler. Provisioning new accounts
remains with the server administrator. Do not change permissions, model lists
or limits merely because someone opens the page or copies an admin request.
