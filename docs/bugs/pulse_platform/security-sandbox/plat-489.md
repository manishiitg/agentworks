[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-489 — Vault is on by default in local runs

| Coordination | Value |
|---|---|
| State | fixed on main; needs a local restart. Not yet checked against the owner's real workflows |
| Date | 2026-10-05 |
| Owner | security-sandbox |

## Source

Local runs started the gateway in standalone mode but never pointed the backend at it, so `CAPLAYER_SERVICE_URL`
was unset and the backend ran with no Vault: nothing checked grants, and the Vault secret-selection migration
never ran. Two LinkedIn schedules failed on 2026-10-04/05 because `IMGBB_API_KEY` and `VERTEX_API_KEY` were
selected but have no value anywhere; the owner wants Vault on locally by default.

## Done

- `run_server_with_logging.sh` starts the gateway in platform mode (private service token under
  `mcp-gateway/var/platform/`, outside the docs) and exports `CAPLAYER_SERVICE_URL` / `CAPLAYER_SERVICE_TOKEN_FILE`
  to the backend, like the server deploy. `AGENTWORKS_LOCAL_VAULT=0` turns it off. If the gateway fails to start the
  Vault URL is dropped, so the backend does not fail closed against nothing.
- Checked on an isolated stack (own ports, docs and state): gateway healthy in platform mode, backend up and ran the
  secret-selection migration, no Vault errors in the log.

## Left

- Restart the local app, then run the migration dry run and list the selections with no value
  (`IMGBB_API_KEY`, `VERTEX_API_KEY` on both LinkedIn workflows); add the values or detach them.
- The local gateway now starts with an empty platform state. Check the Platform group grants (PLAT-471) cover
  every existing workflow and MCP, and that a scheduled run still passes.
- Decide whether a missing secret should stop a whole run or only the step that needs it.
