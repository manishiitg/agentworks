[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-432 — Scripted routes are named tools

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | plans-contracts |
| Related | PLAT-423 (Relay Python tools, to be retired in favour of this), PLAT-431 |

## Why

The owner asked for one way for an agent to call a script: scripted routes
(sub-steps) rather than a second "Python tools" concept. Routes already took
typed arguments and ran saved scripts; they lacked a native named tool, a JSON
return value and a richer argument schema.

## Done

- Every saved scripted route of an Agent step is also offered to that agent as
  its own tool, named after the route (`lookup-customer` → `lookup_customer`),
  with its parameters as the native input schema. It runs the same route code as
  `call_scripted_sub_agent` (validation, call folder, sandbox, history), always
  synchronously. A name that collides with a platform tool is skipped (the route
  stays reachable through `call_scripted_sub_agent`).
- A route returns a value by writing `route_result.json` in its `STEP_OUTPUT_DIR`;
  the caller gets exactly that JSON (both the named tool and
  `call_scripted_sub_agent`). An unusable file (not JSON, over 1 MiB) is logged and
  the run summary is kept. `result.json` was not used: steps already write it as
  ordinary output.
- `script_parameters_schema`: a route may declare one full JSON Schema (type
  object) instead of the flat `script_parameters`; never both. External `$ref`s are
  never loaded. The named tool uses it as its input schema.
- Bug fixed: the Agent step could not read its own routes' saved `main.py` (the
  read ran under the agent's folder guard), so a route looked script-less and
  fell back to generating a new script. The Agent step now has read access to its
  scripted routes' code folders.
- `cli-step-contract` checks it live: the agent calls `contract_lookup` by name and
  writes back a value only the script knows. PASS on a Mac isolated server
  2026-10-04: claude-code, codex-cli, muse-cli.

## Left

- In that failing run the route's saved `code/<route>/` folder ended up empty
  after the fallback; how the generation path removed it is not traced yet. To be
  handled with the owner's decision that scripted steps never self-heal at run
  time (next ticket).
- Retire PLAT-423 Python tools (never deployed) and point the Relay skill at
  scripted routes.
