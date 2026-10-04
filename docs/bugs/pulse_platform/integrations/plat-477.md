[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-477 — trigger_mcp_discovery cannot be called for a private MCP connection (schema declares no name)

| Coordination | Value |
|---|---|
| State | fixed on `main`; needs a backend restart |
| Date | 2026-10-04 |
| Owner | integrations |
| Related | PLAT-474, PLAT-475 (Upwork connection) |

## Source

After the owner's Upwork sign-in, the Upwork Builder chat could not use the connection.
Every Upwork tool call failed with "invalid tool name get_account: invalid resource
name", no discovery ever ran, and `trigger_mcp_discovery` failed both ways:
`{}` gave "name is required", `{"name":"upwork"}` gave "unknown field(s): name".

## Cause

Two pieces that were never reconciled:

- The tool was registered on 2026-03-26 with a schema of no properties; it discovered the
  shared connections.
- The per-chat wrapper (2026-09-11) routes it to the user-scoped executor
  (`privateMCPTool`). On 2026-10-03 (Vault checkpoint `ba931c50b`) that executor gained a
  `trigger_mcp_discovery` branch that discovers one private or permitted connection and
  requires `name`, but the declared schema stayed empty. Argument validation rejects a
  property the schema does not declare, so no call could satisfy both. Nothing tested that
  a tool's schema and its executor agree. The agent therefore never got the connection's
  real tool names and guessed (hence the 400).

## Done

- `server.go`: for the per-chat registration the schema declares `name` (and the description
  says to pass it). The shared fallback is unchanged.
- `TestTriggerMCPDiscoverySchemaDeclaresTheConnectionName`.

## Left

- Not tried live with Upwork; after the restart the upwork chat should call
  `trigger_mcp_discovery` with `{"name":"<connection name from list_mcp_servers>"}`.
- The other private-connection tools (install, edit, remove, list) declare their
  arguments; only this one was found out of step.
