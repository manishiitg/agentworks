# PLAT-550 — Extension workflow cannot read required browser documentation

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | browser |
| Area | browser |
| Summary | fixed on main, not deployed. |

State: fixed on main, not deployed. Date: 2026-10-05. Priority: P2. Reported by owner.

## Evidence and cause

The Upwork `search-find-and-shortlist` step verified connected extension mode,
then failed before discovery at 23:54 IST. Its required managed call
`agent_browser(command="skills", args=["get", "core"])` returned
`CHROME_EXTENSION_UNSUPPORTED: skills is unavailable through the extension`.
The standard browser guide requires installed CLI docs, but the extension
adapter's page-command allowlist rejected the documentation command as well.

## Fix

Read installed CLI docs through the managed tool for `skills list` and
`skills get <name> [--full]`. Keep authenticated account/workspace and step-local
folder grants. Accept only documentation argument forms; reject skill paths,
connection/launch flags and other operations. No relay lease, endpoint, tab
selection or browser launch is used, including with an offline selection.
Actual page actions continue to fail closed when that extension is disconnected.

Clarify extension capabilities, empty endpoint prefix and attached `read_skill`
routes in the platform guide. Add adapter instructions to returned upstream
docs so examples cannot imply unsupported extension network/HAR, transfer,
recording, trace, profiler or teaching features.

## Verification

- Real unpacked Chrome extension check: workflow steps read skill list, core
  overview and full core through the real guarded workspace shell before any
  page action; no workflow tab is created by documentation reads. The existing
  workflow create/fill/click/read and isolation/reconnect checks pass.
- One focused regression checks restored offline selection, trusted account,
  step grants, rejected arguments and continued page-action refusal. Existing
  CDP documentation and rendered browser guidance checks pass.

Qualification used isolated fixture pages, not the user's Upwork account.

## Remaining

Restart/deploy the backend before retrying steps that require managed CLI docs.
Existing step instructions remain compatible; the attached `read_skill` guide
also works without this new managed documentation route.

## Register notes

[PLAT-550](plat-550.md), P2, fixed on main, not deployed. Serve installed CLI skills through the managed extension adapter without a relay/tab; retain workspace grants, clarify attached read_skill guidance and extension limitations. Isolated real Chrome workflow qualification passes.
