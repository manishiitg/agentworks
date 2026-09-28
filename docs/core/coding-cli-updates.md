# Coding CLI updates

AgentWorks uses the user's single globally installed coding CLI for every chat.
It does not install private copies, select release directories, or pin a CLI
version per chat.

The backend checks Codex, Claude Code, Cursor Agent, and Pi on startup when due,
then on an hourly background timer. It invokes each installed CLI's official
update command:

- `codex update`
- `claude update`
- `cursor-agent update` (with `agent` accepted as the executable name)
- `pi update --self`

After updating, AgentWorks resolves the executable from the normal process
`PATH` again and verifies it with `--version`. `PI_BIN`, when supplied, must be
an absolute executable path and is updated in place. Muse remains probe-only
because its launcher already performs its own background updates.

After a successful update AgentWorks keeps one release per CLI. Cursor Agent's
updater (and Claude Code's native installer) download each release beside the
previous ones under `~/.local/share/<cli>/versions/` and never delete them
(Cursor is ~0.5 GB per release). The updater removes every release there except
the one the verified executable resolves to, and skips any release a running
process still uses; that one goes on a later check. Only those two exact
stores are pruned: npm-global installs update in place, and other "versions"
directories (e.g. nvm's) are never touched.

An uninstalled CLI is recorded as `not_installed`; AgentWorks does not install
it. A successful check is due again after 24 hours. A failure is retried after
one hour without rerunning providers that are not due. Each attempt has a
15-minute timeout, and cancellation terminates its subprocess group.

## State and migration

The durable, atomically written `state.json` lives in:

- `$AGENTWORKS_STATE_ROOT/cli-updates/state.json` when an absolute instance state
  root is supplied.
- Otherwise, Go's user config directory under `agentworks/cli-updates/state.json`
  (normally `~/Library/Application Support/agentworks/cli-updates/state.json` on
  macOS and `${XDG_CONFIG_HOME:-~/.config}/agentworks/cli-updates/state.json` on
  Linux).

The state records each executable's status, version, resolved absolute path,
last attempt, last success, next check, and any error. An OS file lock serializes
backend processes sharing the root. Invalid JSON is preserved rather than reset.

On the first enabled check after upgrading from the private-release design,
AgentWorks removes its retired `bin`, `current`, `releases`, and `sessions`
directories. Only `state.json` and `update.lock` remain. It also clears the old
`AGENTWORKS_MANAGED_CLI_BIN` process override and removes that retired directory
from the backend's `PATH` before agents can launch.

`CLI_UPDATE_ENABLED=false` disables update checks. `CLI_UPDATE_DRY_RUN=true`
also disables updates. Neither setting changes which executable agents launch:
all launches use the global CLI resolved by the existing adapter and shell
environment.

## Security and verification

The updater receives the user's real home and XDG paths because official
self-updaters must modify the global installation. Workflow and provider secrets
are omitted from its environment; locale, proxy, and CA settings are retained.
Command output is not included in errors because proxy URLs can contain secrets.

Offline regression coverage uses fake global executables and never modifies a
real installed CLI:

```sh
go -C agent_go test -race ./internal/cliupdate
bash scripts/test-local-instance.sh
```

The focused tests cover scheduling, restart persistence, retry isolation, lock
contention, official command arguments, environment filtering, `PI_BIN`, Cursor's
`agent` alias, process cancellation, Muse's probe-only policy, and removal of the
retired private layout.
