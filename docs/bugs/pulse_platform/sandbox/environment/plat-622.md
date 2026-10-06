[← sandbox / environment](index.md)

# PLAT-622: Shell environment leaks server and account data

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | sandbox |
| Area | environment |
| Summary | User shells inherit the server's environment minus a deny-list; sign-in emails, other users' ids and the Google CLI keyring password reached Code terminals |

## What happened

## Fix

## Allowlist (done)

Commands that run as a person's slot account (their Code terminal and the agent's shell for them) now get an allowlist of the service environment (`security.SlotShellEnv`, applied in the slot branch of `isolator_linux.go` before the slot HOME and per-call values): PATH/HOME/locale/TMPDIR, terminal and tmux, the platform URLs and `AGENT_*`/`AGENTWORKS_*` (minus `AGENTWORKS_SLOT_CLI_USERS`), git/pip/python/npm/node/go/cargo settings, proxies and CA bundles. Anything else, including a server variable added later, stays out. Verified in the Linux container harness (`TestRealSlotChain`, "the slot's shell gets only the allowlisted environment": planted AUTH_ALLOWED_EMAILS, GATEWAY_USERNAME, AGENTWORKS_SLOT_CLI_USERS and an unknown variable are absent, PATH present), plus `TestSlotShellEnvKeepsToolsAndDropsTheRest`.

## Google CLI keyring password (no change needed)

Checked live on Excellence 2026-10-06: slot12's Code terminal has no `GOG_*` variable; slot commands already drop them (`hostGogRestricted` is true for any slot). Only the service account's own shells get the password, and that account owns the keyring (`/srv/agents/home/.config/agentworks/gog`, agents 0700; slot12 cannot list it).

Not deployed.
