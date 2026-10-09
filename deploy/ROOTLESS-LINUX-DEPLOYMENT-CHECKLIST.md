# Rootless Linux deployment checklist

Shared requirements for the **rootless, `systemd --user`, host-level-Caddy**
deployment pattern used by shared workflows
([`rootless-linux/README.md`](rootless-linux/README.md)) and
Video Studio ([`aws-ec2/README.md`](aws-ec2/README.md)): an
unprivileged service account runs the agent + workspace API + gateway
directly (no root, no Docker), a shared host-level Caddy reverse-proxies to
it, and shell tools run inside a Landlock-first sandbox with a mount-
namespace fallback.

This doc exists because Video Studio's deploy (`./deploy.sh rts`) guarantees most
of the items below automatically, every release. **Dominion has no
equivalent script — every one of these was independently rediscovered as a
live production incident**, one at a time, because there was nothing
forcing a new dedicated-VM deployment to prove it satisfies them before
going live. Use this as a pre-launch checklist for any *new* deployment on
this pattern, and re-run it after any change to the underlying `workspace`/
`agent_go` modules that touches sandboxing, credentials, or Caddy config.

The legacy root-owned shared-host deployment (Docker Caddy + frontend) that
some items below still mention was retired and its scripts removed on
2026-09-23; those notes are kept only as history.

## Checklist

### Rootless Docker for Work builds

Native Linux Work installations that offer Docker builds must use a daemon
owned by the application service account. Never add that account to the
host's `docker` group or expose `/var/run/docker.sock`; either grants effective
root access to the host. Install and verify the shared contract with:

```bash
sudo deploy/common/install-rootless-docker.sh <service-user>
deploy/common/install-rootless-docker.sh --check <service-user>
```

The check fails unless the user service is enabled and active, its Unix socket
belongs to the service account, Docker reports `name=rootless`, its data root
is inside the service home, and the account is absent from the privileged
`docker` group. Set the workspace service's `DOCKER_HOST` to
`unix:///run/user/%U/docker.sock`; `workspace/security/environment.go` accepts
only that current-user socket and rejects host or TCP Docker endpoints.

### Release gate: report preview assets (every release)

Build with `npm ci && npm run build` from `frontend/`. Do not substitute
`vite build`: it omits the report-preview runtime. The full build now runs
`check:release-assets`, which rejects missing or empty `index.html` and
`report-preview.js`.

After assembling the release, run the shared checker against the **packaged**
frontend directory, then copy the checker with the release and run it on the
target host **before changing the current symlink or restarting services**:

```bash
node frontend/scripts/check-release-assets.mjs <release-directory>/frontend
# On the target host, using the uploaded checker:
node <release-directory>/check-release-assets.mjs <release-directory>/frontend
```

Set the agent service's `STATIC_DIR` to the same directory (for example
`Environment=STATIC_DIR=%h/app/current/frontend`). A bundle served only by
nginx in another container is insufficient: the agent process must be able to
read `report-preview.js` at its own configured static root. For split-container
deployments, copy or mount the checked assets into the agent container too,
and execute the check there before promotion. Frontend build success alone
does not verify that runtime mount/configuration.

Reject the release on a nonzero checker exit. After activation, run a report
preview and confirm it captures a screenshot; a generic HTTP 200 can be an
HTML fallback and is not proof that the preview runtime loaded. This applies
to Dominion/manual deployments as well as automated releases. The EC2 and
dedicated-VM scripts run the packaging checks automatically; frontend Docker
builds inherit the build gate through `npm run build`.

### 1. Claude Code CLI installed and authenticated

The agent shells out to a bare `claude` on `PATH`
(`exec.LookPath("claude")`, no configurable override). Without it, every
turn fails with `claude cli not found in PATH`.

```bash
npm install -g --prefix <tools-prefix> @anthropic-ai/claude-code
```

Put `<tools-prefix>/bin` ahead of the system default in `PATH` for every
service unit's `EnvironmentFile`, then verify it actually authenticates
with the deployed token — a present-but-unauthenticated CLI fails
differently, not with the PATH error above:

```bash
CLAUDE_CODE_OAUTH_TOKEN=... HOME=<service-home> PATH=<tools-prefix>/bin:$PATH \
  claude -p --output-format json <<< "say OK"
# expect: {"is_error":false, ..., "result":"OK", ...}
```

### 2. The Landlock launcher binary is present

The workspace API resolves its shell-sandbox launcher by the hardcoded name
`video-studio-landlock-runner` (`workspace/security/landlock_policy.go`) —
**that literal filename regardless of which product is actually running** —
first next to its own binary, then on `PATH`. Without it, PLAT-118's
fail-closed design refuses every `execute_shell_command` call with
`SANDBOX_UNAVAILABLE` rather than running unsandboxed.

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -o releases/<release-id>/bin/video-studio-landlock-runner \
  ./workspace/cmd/landlock-runner
```

Verify: `curl http://127.0.0.1:<workspace-port>/health` →
`shell_sandbox.available=true`, `shell_sandbox.backend=landlock`.

### 2a. The pinned `gog` CLI is installed for Gmail

Gmail connections use `gog` (`github.com/openclaw/gogcli`) rather than the
older `gws` npm CLI. Install the pinned Linux release system-wide, verify its
published SHA-256 checksum before extraction, and ensure `gog` is visible on
the agent service's `PATH`. On the Video Studio EC2 host this is automated by
`deploy/aws-ec2/install-system-tools.sh`.

```bash
command -v gog
gog --version
# expect the version pinned in install-system-tools.sh
```

The application passes an explicit `--home` plus either an access token or a
named account/client for every invocation. Do not configure a process-global
default mailbox as a substitute: it could make one workflow send as another
connection. After installation, set `use_gog_backend: true` in the Gmail
configuration and reconnect/import any legacy account that has not yet been
registered with `gog`. Verify status and one explicitly addressed test email
before removing the old `gws` binary.

For a headless service, also persist a strong `GOG_KEYRING_PASSWORD` in its
mode-0600 environment file and set `GOG_KEYRING_BACKEND=file`. Generate the
password once and preserve it across releases; rotating or losing it makes the
stored refresh-token keyring unreadable. The EC2 deploy script enforces this
contract automatically.

### 3. Both Downloads directories exist

Two *different* directories, both required, both silent `SANDBOX_UNAVAILABLE`
failures if missing:

- **Workspace-level** `Downloads/` under the docs root (e.g.
  `<docs-dir>/Downloads`) — one of the hardcoded read/write folders every
  multi-agent chat session grants (`server.go`, `tool_setup.go`).
- **`$HOME`-level** `Downloads/` under the service account's own `$HOME`
  (e.g. `<service-home>/Downloads`) — referenced by the Folder Guard policy
  bootstrap independent of the workspace docs tree. Its absence produces
  `SANDBOX_UNAVAILABLE: ... stat <home>/Downloads: no such file or
  directory` — silently blocking `execute_shell_command` for every session
  from first boot, as observed on an early dedicated deployment.

Create both explicitly during account/directory bootstrap, not as an
afterthought:

```bash
mkdir -p <docs-dir>/Downloads <service-home>/Downloads
```

### 4. `MCP_API_URL` is explicitly set — never left to infer

`GetCodeExecAPIURL()` (`agent_go/cmd/server/server.go`) resolves the URL
custom tools use to call back into the agent, in this priority order:
1. Explicit `MCP_API_URL` env var — **always wins if set.**
2. `NATIVE_WORKSPACE=true` → `http://127.0.0.1:<agent-port>`. This flag is
   only set by a local-dev wrapper script (`run_server_with_logging.sh
   --with-workspace`), **not** by `./deploy.sh rts` — don't rely on it
   for a production rootless deployment.
3. Otherwise, if `WORKSPACE_API_URL` contains `localhost`/`127.0.0.1`
   (true for essentially every rootless deployment), it falls through to
   **`http://host.docker.internal:<agent-port>`** — a Docker-only hostname
   that does not resolve on a bare-metal/rootless host at all.

Branch 3 is what actually fires when this is missed: tool calls fail with
DNS resolution errors against `host.docker.internal`, and the agent itself
may go looking for it (`getent hosts host.docker.internal`) while trying to
self-diagnose. Found live on Dominion 2026-08-29, ~5 days after the rest of
this checklist's items were already fixed — this one had no earlier
symptom because nothing had needed a custom tool callback until then.

`./deploy.sh rts` guarantees this for Video Studio automatically
(`grep -q '^MCP_API_URL=' ... || echo 'MCP_API_URL=http://127.0.0.1:8000' >>
...`, idempotent on every release). Set it explicitly and permanently in
`.env` for any deployment without that script:

```bash
echo 'MCP_API_URL=http://127.0.0.1:<agent-port>' >> <deployment>/.env
```

### 5. Caddy compresses responses

Caddy does not compress by default — a site block with no `encode`
directive silently ships the full uncompressed frontend bundle (multiple
MB) on every page load. Confirmed live on Dominion 2026-08-28 as the root
cause of a real "the server is slow" report: the main JS bundle transferred
at ~3.7 MB instead of the ~1 MB gzip produces.

```
your-hostname.example.com {
    encode zstd gzip
    reverse_proxy 127.0.0.1:<gateway-port>
}
```

Verify against the *real* JS bundle, not the small `/login` page (which
compresses trivially either way and can look fine even when the real
bundle isn't):

```bash
curl -sS -H "Accept-Encoding: gzip" -D - -o /dev/null \
  https://your-hostname.example.com/assets/index-<hash>.js
# expect: content-encoding: gzip
```

Applies to this doc's rootless pattern only — the legacy Docker-Caddy
deployment configures compression separately in its own Caddyfile.

### 6. The mount-namespace Landlock fallback actually works

Landlock's rule model is purely additive (no allow-with-carve-out), so it
correctly *refuses* any Folder Guard policy that needs one — e.g. write
access to a folder except one of its own subfolders (a real policy the
generic AgentWorks chat requests for a workflow's `planning/` folder). It's
supposed to fall through to a mount-namespace backend for that case.

As of 2026-08-29 the code-level bug that made that fallback permanently
non-functional on every rootless deployment is fixed
(`workspace/security/isolator.go`/`isolator_linux.go` — see git history for
"Fix the Landlock mount-namespace fallback"); any deployment built from
`workspace` at or after that fix already has it. What's still a genuine
**per-host** thing to check, not fixed by that patch: Ubuntu 23.10+/24.04
defaults `kernel.apparmor_restrict_unprivileged_userns=1`, which separately
blocks unprivileged user-namespace creation for any process without an
explicit AppArmor profile.

**This is no longer a manual step.** The rootless activation (`build-and-activate.sh`) runs the real sandbox test as the service
account before it switches `current`, so a host where the sandbox cannot work stops the deploy with the fix in the message, and
`./deploy.sh` installs the path-scoped exception (`provision-slots.sh userns`) on every host it prepares (`HOST_SETUP_SCRIPT`).
A host that goes live without it fails every shell command while the other checks pass. The commands below
are for looking at a host by hand.

```bash
cat /proc/sys/kernel/apparmor_restrict_unprivileged_userns
# if 1: decide (a real security trade-off, not a default to reach for)
# between disabling it host-wide via /etc/sysctl.d/ (simplest, loosens
# every process on a shared host) or a narrower AppArmor profile granting
# userns_create to only this product's own binaries.
```

**Verify with the actual test**, not a manual curl reproduction — compile
it locally and ship the binary if the host has no Go toolchain (typical for
a deploy-target-only server):

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go test -c ./workspace/security/ -o security.test
# ship security.test to the host, then:
./security.test -test.run TestMountNamespaceFallbackEnforcesLandlockRejectedOverlapPolicy -test.v
```

### 7. If syncing an existing workflow's data from a dev machine

A full directory sync of workflow state (chat history, builder sessions,
`.claude/` project state) from a local/dev machine to a server carries that
machine's own absolute paths with it — specifically
`runtime.agent_session_handle.provider.working_dir` inside any `builder/`
conversation JSON, captured when a native Claude Code session was last used
*locally*. Resuming one of those sessions on the server fails with
`SANDBOX_UNAVAILABLE: ... mkdir <dev-machine-path>: permission denied`
(found live on Dominion 2026-08-28, 221 affected files).

Before considering a sync complete, grep the synced tree for the source
machine's own absolute path and bulk-correct it to the real server path.
This is silent until a native coding-agent session actually tries to
resume — it will not surface at sync time.

### 8. `npm`'s prefix must resolve correctly for the service identity

`npm install -g --prefix <tools-prefix> @anthropic-ai/claude-code` (item 1)
only fixes where the CLI is *installed*; it doesn't make `npm config get
prefix` return that path afterward for the identity that actually runs
`claude`. If no `.npmrc` sets `prefix=<tools-prefix>` under the service's
real `HOME` (the systemd unit's `Environment=HOME=...`, not an interactive
SSH login's own `$HOME` — these can differ), `npm` falls back to the system
default (typically `/usr`), and Claude Code's own self-update tries to
write there on every invocation and fails on permissions. That failure
doesn't break the turn itself — it prints a persistent "✘ Auto-update
failed: ... · Run claude doctor" footer at the bottom of the tmux pane
below the real idle `❯` prompt. `multi-llm-provider-go`'s
`hasReadyInputPrompt` completion scan (`claudecode_interactive_adapter.go`)
hits that footer first and reports "not ready" indefinitely — confirmed
live on Dominion 2026-08-30, wedging a turn for 3+ hours with the CLI
itself having answered in 8 seconds. Fixed there as one instance of a
general shape (unrecognized footer line masking the idle prompt); any
*other* still-unrecognized Claude Code footer text can reproduce the same
class of hang regardless of this specific fix.

Verify with the real service identity, not a bare SSH login:

```bash
HOME=<service-home> npm config get prefix
# expect: <tools-prefix>, not /usr or any other system default
```

### 9. Server logs must actually reach a file the service account can read

Found live on Dominion 2026-08-31 while trying to check for tool errors and
finding nothing at all, despite the binary's own `--log-file` flag: this is
a shared-code gap, not a Dominion-only misconfiguration, so check it on any
new rootless deployment (Video Studio's `video-studio-agent` almost
certainly has the identical gap — unconfirmed as of this writing).

Two independent problems, both required:

1. `createServerLogger()` (`agent_go/cmd/server/server.go`) ignores
   `--log-file` entirely and hardcodes an empty log path, on the assumption
   that whatever launches the binary redirects stdout to a file itself. A
   bare `ExecStart=` in a systemd unit does not do that — output goes to
   the system journal instead, which an unprivileged service account
   (not in `adm`/`systemd-journal`) cannot read (`journalctl --user` fails
   with "insufficient permissions"). Fix at the systemd-unit level, not in
   application config: add `StandardOutput=append:<path>` and
   `StandardError=append:<path>` to each service's unit file.
2. Several other log streams (`logs/schedule.log`, `logs/llm_debug.log`)
   use a path *relative to `WorkingDirectory`*, which is the `current`
   release symlink. A plain `mkdir logs` inside each new release silently
   orphans the previous release's history on every redeploy, starting over
   empty — indistinguishable from logging being broken, since nothing
   errors. Fix: symlink each release's `logs/` to one persistent,
   release-independent directory instead of letting the app create a plain
   one:
   ```bash
   ln -s /srv/dominion/logs /srv/dominion/releases/<release-id>/logs
   ```

Verify after any release, against real content, not just file existence:

```bash
ssh -p <port> <user>@<host> 'tail -5 /srv/dominion/logs/agent.log'
# expect: recent [API] request lines, not empty and not stale
```

### 10. Python package tooling is installed for the service runtime

On Ubuntu/Debian, install the runtime prerequisites during host provisioning:

```bash
sudo apt-get update
sudo apt-get install --no-install-recommends -y python3-pip python3-venv
sudo -u <service-user> python3 -m pip --version
```

Verify using the service identity and the same Python executable as scripted
steps. Also create a temporary venv in a service-writable directory and verify
its `bin/python -m pip --version`. Ubuntu's externally managed system Python
may reject `pip install --user`; use a workflow-local venv for dependencies
and explicitly invoke that interpreter. Do not set a host-wide
`PIP_BREAK_SYSTEM_PACKAGES` override. Verify imports through the actual saved
step runner: installing into a venv alone does not change its interpreter.

### 11. Workflow documents and code remain on persistent disk

Configure `--docs-dir` outside release directories, container writable layers and
`/tmp`. RTS uses `/data/video-studio/docs` on its encrypted EBS root volume;
`DeleteOnTermination=false` was verified on 2026-09-07. Keep workflow `code/`,
`learnings/`, `db/` (including assets), run outputs, and `.sandbox-cache/` under
this durable docs root. Release cleanup must never delete or replace it.

Verify `findmnt -T <docs-dir>` and the service's configured docs path before
deploying. Instance replacement must reattach/mount the retained volume and
point the new service at the same docs directory. Persistence is not a backup;
configure snapshots/backups separately. A release redeploy does not migrate or
clear existing workflow documents or change their code-layout versions.

## Shared deployment automation

The shared rootless deployer and the Video Studio deployer enforce these requirements.
Use the repository-root entry point and the private deployment configuration for an
existing host; retired product installers are not supported.
