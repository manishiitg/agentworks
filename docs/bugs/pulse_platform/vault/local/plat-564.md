[← vault / local](index.md)

# PLAT-564: Local Vault fails to start: configuration key left in the old state folder

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | vault |
| Area | local |
| Summary | Local Vault never started after its state folder moved: the key stayed behind and the failure only warned |

## What happened

- 71097ddda (2026-10-05, "Vault on by default in local runs") points the local gateway's state folder at
  `mcp-gateway/var/platform`. The gateway reads its configuration key from the state folder.
- Existing installs keep the key in `mcp-gateway/var/gateway.sqlite.key`, while the encrypted configuration
  database (`workspace-docs/_users/default/Chats/CapLayer/db/gateway.sqlite`) still exists. The gateway then
  refuses to start ("gateway configuration key is missing") rather than create a key that cannot decrypt it.
- A default-on gateway failure only warned ("Continuing without the MCP Gateway"), so the owner's local app ran
  without Vault from 2026-10-05 until 2026-10-06 unnoticed.

Brain is not affected: it is a core product that starts with the backend and does not use Vault.

## Fix

- The launcher copies an existing `var/gateway.sqlite.key` into `var/platform/` (0600) when the new folder has
  none. Copy, not move: the old file stays as a backup.
- With local Vault on (the default), a failed Vault start stops the run with the log path and the opt-out
  (`AGENTWORKS_LOCAL_VAULT=0`). A standalone gateway with Vault opted out stays auxiliary.

Verified 2026-10-06: the gateway built from this change, run against copies of the owner's database and key on a
spare port, failed with "configuration key is missing" before the copy; after it, it opened the existing
configuration (health 200, reconnect attempt for the saved connection `c1`).

## Left

- Confirm on the owner's next local start: "Copied the Vault configuration key" appears once and Vault is listening.
