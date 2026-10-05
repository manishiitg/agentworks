[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-489 — A Crew/Code chat's built-in shell runs as the app account with platform secrets in its environment and the app's Docker socket

| Coordination | Value |
|---|---|
| State | open (found 2026-10-05 by a read-only probe on Excellence, release agents-f429a8cc; nothing changed; no secret value was printed, no container was run) |
| Priority | P1 |
| Owner | security-sandbox |

## How it was found

Owner asked "can we do some check in native bash". Through the MCP connection (as the owner) a scratch Crew (`smoke-test`, owner's own, no secrets) was asked to run a fixed script with its BUILT-IN shell (`exec_command`/Bash) and the same script with the bridge shell (`execute_shell_command`) as control. The script prints names, yes/no and HTTP codes only. (The Crew declined a first version that touched another Crew's data and made a scratch folder; that version was dropped, not pushed.)

## Facts (built-in shell vs bridge shell)

| Target | Built-in shell | Bridge shell | Who refused |
|---|---|---|---|
| identity | `agents` uid 990, member of EVERY slot group (slot01..50, slotshared) | `slot03` | n/a |
| open `/srv/agents/.env`, `state/vault/agent.env` | refused although `access()` says readable | refused | Landlock (built-in), Unix (bridge) |
| list state/, docs root, _users, Workflow, vault state dir, app home, slots/run, releases, state/cli-runtimes/v1 | ALLOWED (folder names only; `ListPaths: ["/"]` in `internal/clisandbox/landlock.go`, by design) | refused | n/a |
| `/srv/confida` (other product) | refused | refused | Unix |
| write in docs root, state/ | refused by a real `touch` (docs root); `access()` says writable | refused | Landlock / Unix |
| tmux on the slot socket | blocked by the slot tmux shim ("Permission denied") | cannot connect | shim / hidden folder |
| loopback `/health`: 24001, 8080 | 200 | 200 | none |
| `docker info` | WORKS, via `DOCKER_HOST=unix:///run/user/990/docker.sock` (the APP account's rootless Docker) | works (the slot's own Docker) | none |
| environment (names) | includes `SUPABASE_SERVICE_ROLE_KEY`, `SUPABASE_ANON_KEY`, `GOG_KEYRING_PASSWORD`, `ADMIN_USERS`, `AUTH_ALLOWED_EMAILS`, `CAPLAYER_SERVICE_TOKEN_FILE`, `SSH_AUTH_SOCK`, `GATEWAY_*`, `DOCKER_HOST` (not `AUTH_SECRET`, not `ACCESS_PASSWORD`) | `ADMIN_USERS`, `AUTH_ALLOWED_EMAILS`, `GATEWAY_*`, `DOCKER_HOST`, `MCP_API_TOKEN` (its own bridge token); no Supabase keys, no keyring password | n/a |

## Why it matters

- The CLI process inherits the agent's environment (PATH, NVM, SYSTEMD, ... all present), so `env` in the built-in shell can print the Supabase service-role key and the gog keyring password to anyone who can get that CLI to run a command (a Crew/Code user, or a prompt injected into their chat).
- Landlock does not govern `connect()` on pathname Unix sockets, so the app account's own Docker daemon is usable from inside the lock: a container with a bind mount runs outside it and could read what `agents` can read (its `.env`, state, and, through the slot groups it belongs to, every user's tree). Only `docker info` was run; no container was started.
- The app account is in every slot group, so Unix permissions do not separate users for this shell; Landlock is the only boundary. Reads of files were refused by Landlock; names are listable.
- Recorded decisions this touches (owner's call): native tools on in `full` (2026-10-03/04, PLAT-385..419), PLAT-364 "keep both tool sets", PLAT-446 "Crew turns run as the app account", slots keep Docker (2026-10-04).

## Not tested

Writes into other users' trees; reading with the Supabase key; running a container; whether other CLIs (Claude, Cursor, Muse, Pi) show the same environment (this run was Codex); RTS and Confida (RTS app Docker socket exists at `/run/user/999`).

## Options (none applied)

1. Build the CLI's environment from an allowlist (or strip a denylist of platform secrets) at launch, as the bridge shell already does (`BuildSafeEnvironment`); the CLI needs a handful of variables, not the service-role key. Closes the env exposure for every native tool.
2. Remove the app Docker from the app-account CLI: unset `DOCKER_HOST` AND hide `/run/user/<app uid>` in the CLI's mount namespace (the private-roots mechanism of PLAT-480 F1, not yet in the provider launcher), or run Crew CLIs as the user's slot (the slot CLI canary exists: `AGENTWORKS_SLOT_CLI_USERS`), which also removes the slot-group membership problem; conflicts with PLAT-446.
3. Disable the native shell per CLI (Codex `features.shell_tool=false`; Claude print-mode denylist; Muse allowlist; Agy hook; Cursor has no deny-all mode). Owner rejected `mcp_only` for chats (2026-10-05). Does not by itself remove the env (file tools may read `/proc/self/environ`) so it needs option 1 anyway.
