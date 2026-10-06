[← sandbox / environment](index.md)

# PLAT-622: Shell environment leaks server and account data

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | sandbox |
| Area | environment |
| Summary | User shells inherit the server's environment minus a deny-list; sign-in emails, other users' ids and the Google CLI keyring password reached Code terminals |

## What happened

## Fix

## Left

## What happened

Excellence, 2026-10-06: `env` in a Code terminal (running as slot12) showed the server's environment: `AUTH_ALLOWED_EMAILS` (everyone allowed to sign in), `AGENTWORKS_SLOT_CLI_USERS` (other users' ids), `SSH_AUTH_SOCK` (the service's SSH agent), `GATEWAY_*` settings, internal paths and ports. `GOG_KEYRING_PASSWORD` is also passed, on purpose, by `gogconfig.Environment`. No server API keys or tokens passed: the deny-list covers AUTH_SECRET, MCP/bridge tokens, provider keys and the login password.

Cause: native mode (`workspace/security/environment.go` `buildNativeEnvironment`) passes the whole service environment minus a deny-list, so any new server variable reaches every user's shell unless someone adds it.

## Done

Deny-list now also drops `AUTH_*`, `GATEWAY_*`, `AGENTWORKS_SLOT_CLI_USERS`, `SSH_AUTH_SOCK` and systemd bookkeeping (`MEMORY_PRESSURE_*`, `NOTIFY_SOCKET`, `INVOCATION_ID`, `JOURNAL_STREAM`, `SYSTEMD_EXEC_PID`). Only server processes and launcher scripts read `AUTH_*`/`GATEWAY_*`. Pinned by `TestNativeEnvironmentDropsServerIdentityAndAccountData`. Not deployed.

## Left

- Replace the deny-list with an allowlist for user-facing (slot) shells: PATH, HOME, LANG, TZ, TERM, TMPDIR, SHELL, USER/LOGNAME, the tool paths and the few AGENTWORKS_* variables the shell helpers need.
- Owner decision: `GOG_KEYRING_PASSWORD` in every user's shell. On a multi-user server it should be per-user, or not passed to slot shells, which needs the Google CLI keyring to move out of the shared service home.
