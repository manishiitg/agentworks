# Coding CLI updates

## Native SDK components

Claude and Cursor's SDK transports also need their official Node packages.
One provisioner (`agent_go/internal/codingsdks/coding-sdks.cjs`) owns the
certified Claude0.3.311 / Cursor1.1.0 versions and import/export checks. The
managed installer runs that file; the backend embeds the same file, so a
desktop DMG does not need a source checkout to prepare its components.

The backend checks/prepares them in the background at startup, using the
Node/npm on its normal PATH (including Desktop's imported login-shell PATH).
It reuses a matching global SDK, or installs into writable app state. Only
an SDK chat waits for its provider's setup; other providers and the app remain
available. Slow setup shows progress in chat. Failures use a plain error and
retry on the next SDK chat. Logs record the selected Node/npm and readiness,
without npm output or credentials. Import validation makes no model request.
Hosted servers only check and bind the components provisioned by deployment;
they do not install SDK packages while serving chats. Managed CLI prefixes are
checked before npm's default global directory.

The existing SDK path overrides remain supported. Update-disabled/dry-run
settings allow checks and use of installed components but prohibit downloads.
Node.js/npm must already be available; absence is reported rather than claimed
as successful setup. The remote Windows CLI/laptop connector does not run a
model backend and therefore does not install these host-only components.

## Coding CLI updates

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

## Deployment provisioning

Every server deployment (rootless products, server A and Dominion) uses
`deploy/common/install-coding-clis.sh` to install or update Claude Code, Codex,
Cursor Agent, Pi, Muse and AGY. It requests current official releases and
checks that every managed executable launches with `--version` before the
release can activate. Missing binaries are installed; existing binaries are
updated. The backend updater above handles checks between deployments.
Provider menus retain all offered CLIs and report authentication/runtime
readiness. AGY has no separate local-only visibility gate.

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
