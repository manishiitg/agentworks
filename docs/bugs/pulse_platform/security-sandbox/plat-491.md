[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-491 — A Crew/Code chat's built-in shell runs as the app account with platform secrets in its environment and the app's Docker socket

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
3. Disable the native shell per CLI (Codex `features.shell_tool=false`; Claude print-mode denylist, interactive `--tools` list; Muse allowlist; Agy hook; Cursor: in full-native mode its preToolUse deny hook exempts Shell (`cursorFullNativeAllowedTools`) and cli.json allows `Shell(*)`, so dropping both would deny it. CORRECTION 2026-10-05: an earlier version of this ticket said Cursor had no deny-all mode; that came from a peer note ("none that I verified"), not from the code, and the code shows it is possible; untested live). Owner rejected `mcp_only` for chats (2026-10-05). Does not by itself remove the env (file tools may read `/proc/self/environ`) so it needs option 1 anyway.

## 2026-10-05: option 1 built (environment scrub), not deployed

`workspace/security/landlock_runner_linux.go`: `ScrubPlatformSecretEnv` runs at the launcher's final exec, the one place every confined CLI passes through (not the six adapters). Deploy self-test row `cli-launcher-env-scrub` (full level) plants canary values under the platform names, runs `env` through the launcher and requires them gone and an ordinary variable kept. Container e2e: passes with the new launcher; with the OLD launcher (origin/main before the change, `E2E_OLD_IS_ENV_SCRUB_BASELINE=1`) the row FAILS and names the leaked variables. Left: deploy (RTS, Excellence, Confida; owner's go each), a live check from a real Crew's built-in shell that `env` no longer shows the names, the Docker socket (option 2), and the native-shell switch (option 3, asked of ai-work-2c by the owner). The macOS local app (Seatbelt) does not go through this launcher.

## Done: native shell off in Full mode (2026-10-05, option 3; on main, not deployed)

Owner decision (see DECISIONS 2026-10-05): the CLI's built-in shell is off in Full mode on every CLI; native file read/edit, skills and
subagents stay; the bridge shell is the only shell. One switch, off by default: `AGENTWORKS_CLI_NATIVE_SHELL=on` restores it
(`nativeshell.Enabled()` in the provider). The prompt says so (`native-shell-off` section) so a model does not conclude it has no shell.

| CLI | Provider commit | Mechanism | Real CLI result |
|---|---|---|---|
| Codex 0.160.0 | 1073ab3 | `shell_tool` and `unified_exec` disabled | no shell tool offered; native edit worked; `on` restored shell |
| Claude Code 2.1.289 | 68ca08f | Bash/PowerShell/Monitor/BashOutput/KillShell dropped from `--tools`; `--disallowedTools` when tools=default | no `Bash` call possible incl. subagent; edit worked; `on` restored |
| Muse 1.4.2 | 01d351e | shell tools out of the allowlist baked into the hook | `bash` refused with reason naming execute_shell_command; edit worked; `on` restored |
| Agy 1.2.16 | 24f86bc | PreToolUse hook denies `run_command`/`send_command_input` | denied with reason; `write_to_file` worked; `on` restored |
| Cursor 2026.10.01 | f740613 | preToolUse deny + `beforeShellExecution` deny; `Shell(*)` dropped from allow | live 2026-10-05 with the RTS key (`CURSOR_API_KEY`, process only): interactive and structured, switch off: shell refused, native Write worked; interactive on: shell ran. Structured on: the shell is "Rejected" by Cursor itself, as it was BEFORE this change (checked on the previous commit): `--print` never ran the built-in shell in Full mode |
| Pi | n/a | already bridge-only | n/a |

Existing live tests that drive the built-in shell now set the switch on. `cli-sandbox-contract` needs the server started with the switch on
(noted in the command).

## Left (native shell)

- The bridge shell in the SAME chat was not driven live on any CLI (stubs only); a real Crew chat on an isolated server (`id -un` refused, native
  edit works, `execute_shell_command` works) was not run, nor the three contracts (they need the switch on).
- Codex structured and Claude print paths: argument construction only, not live. Claude: a skill declaring `allowed-tools: Bash` not tested.
- No deploy: Excellence and RTS need their own live check and the owner's go per server.

