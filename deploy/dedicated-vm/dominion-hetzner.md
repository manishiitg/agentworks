# Dominion on Hetzner

This is the deployment contract for the isolated Dominion product at
`trader.tectonicmarkets.com`. It deliberately does **not** reuse the legacy
shared-host deployment (retired; its scripts were removed on 2026-09-23).

Before standing up (or re-verifying) this deployment, run through
[`../ROOTLESS-LINUX-DEPLOYMENT-CHECKLIST.md`](../ROOTLESS-LINUX-DEPLOYMENT-CHECKLIST.md)
— every item on it was independently rediscovered as a live production
incident on this exact deployment because nothing forced it to be checked
up front. Don't repeat that.

## Deployment target

| Item | Value |
|---|---|
| Public hostname | `trader.tectonicmarkets.com` |
| Server | Hetzner host `116.202.210.102` |
| SSH port | `2299` |
| Runtime account | `dominion` |
| Application root | `/srv/dominion` |
| Product allowlist | `AGENT_PRODUCTS=dominion,work`; `AGENTWORKS_ADMIN_ONLY_PRODUCT_SURFACES=work` |

The host already runs unrelated applications. Dominion must never change their
containers, bound ports, systemd units, Caddy sites, or firewall rules.

## Account and filesystem isolation

Root is only used for one-time machine administration: creating the
`dominion` account, enabling linger, installing the Caddy site, and validating
the SSH configuration. All application processes run as `dominion`:

```text
/srv/dominion/
├── current -> releases/<release-id>
├── releases/                 immutable deployed releases
├── data/docs/                Dominion workspace and trading data
├── home/                     HOME for Claude Code and tmux
├── logs/                     application logs
├── state/                    durable runtime state
├── tools/                    user-owned Node/Claude Code installation
└── .env                      mode 0600; never commit or print
```

The deployment key is authorized for the `dominion` account. Future release
uploads use `ssh -p 2299 dominion@116.202.210.102`, not root.

## Network layout

All application listeners remain loopback-only. The existing Caddy service is
the sole public listener and receives one additional hostname block.

```text
Internet
  │ HTTPS :443
  ▼
Caddy (existing shared service)
  │ reverse_proxy 127.0.0.1:21080
  ▼
Dominion gateway       127.0.0.1:21080
  ├── Agent API         127.0.0.1:21000
  └── Workspace API     127.0.0.1:21001
```

The non-default ports are intentional: this host already has services using
the usual AgentWorks ports (`8000`, `8080`, and `8090`). The gateway supports
`AGENT_API_URL`, `WORKSPACE_API_URL`, and `GATEWAY_ADDR` so the product can
coexist without port conflicts. `GATEWAY_USER_ID=dominion` and
`GATEWAY_USERNAME=dominion` keep its authenticated data separate from Video
Studio.

## DNS and TLS

Cloudflare manages `tectonicmarkets.com`. The required record is:

| Type | Name | Content |
|---|---|---|
| A | `trader` | `116.202.210.102` |

The record may be Cloudflare-proxied. Caddy should serve
`trader.tectonicmarkets.com` directly and obtain an origin certificate. In
Cloudflare, use **SSL/TLS: Full (strict)** after the Caddy certificate is
healthy. If certificate issuance is blocked, temporarily set the record to
DNS-only while validating Caddy rather than weakening the application to HTTP.

The cookie-auth gateway deliberately marks its session cookie `Secure`; do not
publish this product on a bare HTTP IP address.

## Secrets

Never put secret values in this repository, systemd unit files, shell history,
or deployment logs. The runtime `.env` is owned by `dominion` and has mode
`0600`.

The recommended secret source is AWS Secrets Manager,
`dominion/global-secrets`, containing only the keys this product needs:

```text
AUTH_SECRET
ACCESS_PASSWORD
CLAUDE_CODE_OAUTH_TOKEN
```

`CLAUDE_CODE_OAUTH_TOKEN` can be copied from the existing RTS shared setup
secret during a release without displaying it. `AUTH_SECRET` and
`ACCESS_PASSWORD` are unique to this deployment. AWS is a secret source only;
Dominion runs entirely on Hetzner and has no AWS runtime dependency.

## Product data

The Dominion UI reads its fixed workflow data from:

```text
Workflow/tectonicusadaytrading/
```

Copy that directory from the approved local workspace source into
`/srv/dominion/data/docs/Workflow/tectonicusadaytrading/`, preserving its
SQLite database and variables manifest. Do not deploy an empty workspace: the
dashboard and its restricted chat tools rely on that database.

## Release requirements

Build Linux artifacts locally; do not use a Docker build on the deployment
host. A release contains:

- `dominion-agent` (AgentWorks, built for Linux amd64)
- `dominion-workspace` (workspace API, built for Linux amd64)
- `mcpbridge`
- `dominion-gateway`
- `video-studio-landlock-runner` (yes, that literal filename regardless of
  product — see below), built from `workspace/cmd/landlock-runner`
- the built frontend with a runtime configuration exposing only `dominion`
- the versioned `playbooks/` catalog, including the local Finance, Website Growth, Sales, Customer Success, Engineering, and Shopify Crew catalogs and Automation Playbooks, copied into the release root; `validate_playbooks.py` must pass before staging
- `mcp_servers_dominion.json` with an empty `mcpServers` object

### Logs are required, not automatic (found live 2026-08-31)

A fresh release directory must symlink its `logs/` subfolder to the
persistent `/srv/dominion/logs/`, not let the app create a plain directory
there:

```bash
ln -s /srv/dominion/logs /srv/dominion/releases/<release-id>/logs
```

Without this, logging on this deployment is silently broken in two
independent, stacked ways — both confirmed live while debugging an
unrelated user report, after `grep`ing through conversation JSON files was
the only way to find anything:

1. **`--log-file` on `dominion-agent`'s `ExecStart` does nothing.**
   `createServerLogger()` (`agent_go/cmd/server/server.go:6591`) hardcodes
   `logFile := ""` regardless of the flag, with a comment saying it expects
   the *launching shell* to redirect stdout to a file. Nothing in this
   deployment does that, so under systemd every `log.Printf` from the main
   server — including `[API]` request/response tracing and tool-execution
   errors — goes to stdout/stderr, which systemd sends to the system
   journal. The `dominion` account is not in the `adm`/`systemd-journal`
   groups and cannot read it (`journalctl --user` fails with "insufficient
   permissions"), so this output was completely unrecoverable. Fixed by
   adding `StandardOutput=append:/srv/dominion/logs/agent.log` and
   `StandardError=append:/srv/dominion/logs/agent.log` to
   `dominion-agent.service` (mirrored for `dominion-workspace.service` →
   `workspace.log` and `dominion-gateway.service` → `gateway.log`) — this is
   the systemd-native equivalent of the shell redirection the code already
   assumes exists, and needs no code change.
2. **`logs/schedule.log` and `logs/llm_debug.log` are real and already
   working, but not durable.** Several places (`schedule_logging.go`,
   `base_llm.go`, `llm_agent.go`, `base_orchestrator_agent.go`) write to a
   *relative* `logs/...` path, resolved against `WorkingDirectory` — which
   is `/srv/dominion/current`, a symlink to whichever release directory is
   active. A plain `mkdir logs` inside a new release (what every release up
   to and including `62a096fe1-mlp-b932c7b-20260825050548` did) silently
   orphans the previous release's log history on every single redeploy and
   starts over empty — indistinguishable from logging being broken at all,
   since nothing errors. The symlink above fixes this by construction: any
   release-relative `logs/...` write and any systemd-captured stdout file
   land in the same one persistent directory, continuously, regardless of
   how many releases follow.

Verify after any release:

```bash
ssh -p 2299 dominion@116.202.210.102 \
  'ls -la /srv/dominion/current/logs && tail -5 /srv/dominion/logs/agent.log'
# expect: /srv/dominion/current/logs is a symlink -> /srv/dominion/logs,
# and agent.log has fresh [API] lines from just now, not empty/missing.
```

### The Landlock launcher is required, not optional (PLAT-118)

`dominion-workspace` resolves its shell sandbox launcher by a hardcoded name,
`video-studio-landlock-runner` (`workspace/security/landlock_policy.go`),
first next to its own binary (`filepath.Dir(executable)`), then on `PATH`
(`exec.LookPath`) — checked regardless of which product is actually running.
Without it, PLAT-118's fail-closed design means `execute_shell_command`
refuses every call with `SANDBOX_UNAVAILABLE` rather than running
unsandboxed — confirmed live: a fresh Dominion deploy's `/health` reported
`shell_sandbox.available=false`, and the agent's real tool call (Dominion's
only path to its own custom tools is `execute_shell_command` + curl, per
this profile's own product.yaml) failed with "Landlock launcher not found."

Build it alongside the other four binaries and place it in the same `bin/`
directory as `dominion-workspace`:

```bash
GOOS=linux GOARCH=amd64 CGO_ENABLED=0 go build \
  -o releases/<release-id>/bin/video-studio-landlock-runner \
  ./workspace/cmd/landlock-runner
```

Restart `dominion-workspace` after adding it, then confirm via
`curl http://127.0.0.1:21001/health` that `shell_sandbox.available=true` and
`shell_sandbox.backend=landlock` before considering a release complete.

The `dominion` account owns Node, Claude Code, and its tool cache under
`/srv/dominion/tools` and `/srv/dominion/home`. No application process runs as
root. Node/npm are already present system-wide on this host; Claude Code CLI
itself is not, and `dominion-agent` shells out to a bare `claude` on `PATH`
(`exec.LookPath("claude")` — no configurable override), so a release without
this step deploys a working agent that fails on its first real turn with
`claude cli not found in PATH`. Install it into the dominion-owned tool
prefix, as `dominion`, idempotently on every release:

```bash
npm install -g --prefix /srv/dominion/tools @anthropic-ai/claude-code
```

Then ensure `PATH` includes `/srv/dominion/tools/bin` ahead of the system
default in `/srv/dominion/.env` (`EnvironmentFile` for every service unit),
and restart `dominion-agent` after installing or upgrading it. Verify with
`/srv/dominion/tools/bin/claude --version`, and confirm it actually
authenticates with the deployed `CLAUDE_CODE_OAUTH_TOKEN` — a present-but-
unauthenticated CLI fails differently, not with the PATH error above:

```bash
CLAUDE_CODE_OAUTH_TOKEN=... HOME=/srv/dominion/home PATH=/srv/dominion/tools/bin:$PATH \
  claude -p --output-format json <<< "say OK"
# expect: {"is_error":false, ..., "result":"OK", ...}
```

### Being on `PATH` is not enough for a tool a WORKFLOW STEP execs (PLAT-281)

`dominion-agent` shelling out to `claude` directly (above) is the trusted
top-level process calling `exec.LookPath` itself — that call never goes
through the Landlock sandbox at all. A workflow's own shell steps are
different: every `execute_shell_command` call a workflow step makes is
Landlock-sandboxed, and Landlock is allow-list-only by kernel design —
`landlockSystemReadPaths()` (`workspace/security/landlock_runner_linux.go`)
grants read+execute on a fixed set of standard system dirs (`/bin`,
`/usr`, `/lib`, ...) and nothing under `/srv/dominion/tools` or
`/srv/dominion/home`. A tool a *workflow step* invokes — as opposed to one
the agent process invokes directly — being on `PATH` is irrelevant if
Landlock denies executing it: PATH resolution finds the binary, then
Landlock denies the exec, `rc=126: Permission denied`, no login/config
error, no hint it's a permissions problem. This is exactly what happened
to the `alpaca` CLI (`tectonicusadaytrading`'s trading step): installed,
on PATH, working login — and unable to launch from inside a workflow step
for the same reason for over a week (see [PLAT-281](../../docs/bugs/pulse_platform/security-sandbox/plat-281.md)).

**Any CLI tool installed under `/srv/dominion/tools` that a *workflow
step* (not the agent process itself) will invoke needs an explicit grant**
in `/srv/dominion/.env` (read by every service via `EnvironmentFile`):

```
SANDBOX_EXTRA_SYSTEM_PATHS=/srv/dominion/tools:/srv/dominion/home/.config
```

Colon-separated; add further paths (e.g. another tool's config directory
under `/srv/dominion/home/.config`) to the same value rather than
overwriting it. Restart `dominion-workspace` (and `dominion-agent`, since
it shares the same `.env`) after changing it. There is no separate
verification endpoint for this — the `/health` sandbox check only proves
the launcher itself works, not that a specific path is granted; confirm by
actually running the tool from inside a real workflow step, or by
reproducing directly against the shipped launcher binary the way
[PLAT-281](../../docs/bugs/pulse_platform/security-sandbox/plat-281.md) did.

### A workflow step CAN install packages — and what that relies on (PLAT-283)

Since PLAT-283 (2026-09-05) a landlocked `execute_shell_command` step can run
`python3 -m venv` + `pip install …`, `pip install --user …`, or `npm install …` —
proven on this box with a real `pip install yfinance` inside the shipped launcher,
`HOME` left unwritable and only the step's folder granted for writes. Before that
every attempt died in one of three places and looked like a permanent limit; it
never was (Landlock here is filesystem-only, and this box has no egress firewall,
so the network was never the problem). Nothing is configured on the box for this —
it is all in two binaries, so a release that ships both is all it takes:

- `dominion-workspace`: native-mode shells now set `PIP_BREAK_SYSTEM_PACKAGES=1`
  (PEP 668 opt-out), and route `PIP_CACHE_DIR`/`XDG_CACHE_HOME`/`PYTHONUSERBASE`/
  `npm_config_cache`/`TMPDIR` into `.cache`/`.local`/`.tmp` under the step's own
  folder instead of the real `$HOME`, which lies outside every step's write grant.
- `video-studio-landlock-runner`: the read baseline grants all of `/etc` read-only
  in addition to its explicit entries (pip reads `/etc/debian_version` and
  `/etc/mime.types`; enumerating files was whack-a-mole). The explicit entries are
  not redundant: a Landlock rule on `/etc` does not reach a symlink's target, and on
  this Ubuntu box `/etc/resolv.conf` → `/run/systemd/resolve/stub-resolv.conf` — a
  build with `/etc` alone broke every DNS lookup in the sandbox.

Two consequences to keep in mind:

- **Never put a secret readable by the `dominion` user under `/etc`.** Landlock rules
  are additive; a deployment cannot carve one back out of the `/etc` grant.
  `/srv/dominion/.env` is where secrets live, and DAC still applies (verified: no
  file under `/etc` is readable by `dominion` without being world-readable).
- Installs land in the workflow's own `Workflow/<id>/.sandbox-cache/` (PLAT-284):
  `pip install --user`, `npm install -g`, a venv or a downloaded binary put in
  `$SANDBOX_PERSISTENT_DIR` all persist across that workflow's runs — run 1
  installs, every later run finds it already satisfied with no network. Per
  workflow, never system-wide: that is the sandbox doing its job. A tool every
  workflow on the box needs still goes in `/srv/dominion/tools` per the
  PLAT-281 section above. The folder can be deleted for a clean slate; the
  next run rebuilds it. It is a few hundred MB per workflow that installs
  numpy/pandas-class packages, and backups skip it.

Verifying a sandbox change by hand (what caught the `resolv.conf` gap): run the real
command through the shipped launcher with a policy granting only a scratch dir, e.g.
`env -i HOME=/srv/dominion/home PATH=/usr/local/bin:/usr/bin:/bin <routing vars>
/srv/dominion/current/bin/video-studio-landlock-runner --config <policy.json> -- /bin/sh
-c '…'`. The launcher deletes its `--config` file after reading it, so stage one
policy copy per run. After flipping `current`, restart `dominion-workspace` — it
resolves the launcher next to `/proc/self/exe`, i.e. the old release dir, until it
does. And run the *real* install, not a stand-in: a synthetic cache-write probe passed
while `pip` still failed.

## Verification

Before changing Caddy, validate the three local services as `dominion`:

```bash
curl -fsS http://127.0.0.1:21000/api/health
curl -fsS http://127.0.0.1:21001/health
curl -fsSI http://127.0.0.1:21080/login
```

After adding the Caddy hostname and reloading only after `caddy validate`,
verify:

```bash
curl -fsSI https://trader.tectonicmarkets.com/login
```

The site block must also include `encode zstd gzip`. Caddy does not compress
responses by default -- a block with no `encode` directive silently ships
the full, uncompressed frontend bundle (multiple MB) on every page load.
This was missed on the original 2026-08-24 deploy and confirmed live
2026-08-28 as the root cause of a real "the server is slow" report: the main
JS bundle was transferring at its full ~3.7 MB instead of the ~1 MB gzip
would produce. Fixed by adding the directive to only this site's block in
the shared host's `/etc/caddy/Caddyfile` (root-only file; back it up before
editing, `caddy validate` before `systemctl reload caddy`, and never touch
the other unrelated sites' blocks in the same file). Verify against the real
bundle, not just the small `/login` page (which compresses trivially either
way and can look fine even when the real bundle isn't compressed):

```bash
curl -sS -H "Accept-Encoding: gzip" -D - -o /dev/null \
  https://trader.tectonicmarkets.com/assets/index-<hash>.js
# expect: content-encoding: gzip
```

The public site shows Dominion to regular accounts. Crew (`work`) is enabled
only for admins: the frontend product list includes it, while
`AGENTWORKS_ADMIN_ONLY_PRODUCT_SURFACES=work` hides it from non-admin users
and rejects their product API requests. `AGENT_PRODUCTS=dominion,work` keeps
Video Studio, Finance, and unrelated profiles unregistered on this host.
Verify both admin and non-admin access after a release.

AgentWorks (the generic, profile-less workflow builder) is the one
deliberate exception: `frontend/current/runtime-config.js`'s
`enabledProductSurfaces` lists `"agentworks"` alongside `"dominion"` so it
can render for a user who is explicitly granted it, but every logged-in user
who is NOT granted it (the default, e.g. `john`) never sees or can reach it
— see `config/user-product-access.json` below. Confirm this per-user
boundary after any change here, not just that the page loads.

## Current bootstrap state

Live on the target host, first deployed 2026-08-24:

- `dominion` system account, private directory tree, deployment-key SSH
  access over port `2299`, systemd user lingering enabled
- `.env` written with `AUTH_SECRET`, `ACCESS_PASSWORD`, `CLAUDE_CODE_OAUTH_TOKEN`,
  and the gateway/agent product env vars from this doc
- Claude Code CLI installed under `/srv/dominion/tools`, authenticated via
  `CLAUDE_CODE_OAUTH_TOKEN`
- `dominion-agent`, `dominion-workspace`, `dominion-gateway` installed as
  `systemd --user` units (`~/.config/systemd/user/`, not `/etc/systemd/system/`
  — no root needed for the app services themselves), enabled and running
- `Workflow/tectonicusadaytrading/` transferred in full to
  `/srv/dominion/data/docs/Workflow/tectonicusadaytrading/` (its
  `workflow.json` `id` is `wf_36021393` — the folder name and the manifest
  ID are different strings; always confirm the real ID via
  `GET /api/workflows/manifests` rather than assuming they match)
- Caddy hostname block added for `trader.tectonicmarkets.com` (root step),
  cert obtained via `tls-alpn-01`, public HTTPS verified
- Per-user product/workflow access control added 2026-08-25:
  `frontend/current/runtime-config.js`'s `enabledProductSurfaces` widened to
  `["dominion", "agentworks"]`, and `config/user-product-access.json`
  written granting `manish` `products: ["dominion", "agentworks"]` scoped to
  `workflow_ids: ["wf_36021393"]`, and restricting `john` to
  `products: ["dominion"]`. A user with no entry in that file is
  unrestricted by design (see `agent_go/cmd/server/user_product_access.go`)
  — every user this deployment creates going forward needs an explicit
  entry to stay scoped the way this doc describes.
- Crew is available to the `manish` admin account only. The deploy script
  adds `work` to the staged frontend surfaces and sets the admin-only backend
  boundary before activation. `john` remains limited to Dominion. Each release
  also versions the `runtime-config.js` script URL in `index.html` because the
  public CDN has served that file with a four-hour browser cache lifetime,
  despite the gateway's `no-cache` header. This makes newly enabled surfaces
  visible after a page reload.
- `/srv/dominion/home/Downloads` created 2026-08-25 — its absence broke the
  shell sandbox's Folder Guard policy setup for every session
  (`SANDBOX_UNAVAILABLE: ... stat /srv/dominion/home/Downloads: no such file
  or directory`), silently blocking `execute_shell_command` (Dominion's only
  path to its own custom tools) since the original 2026-08-24 deploy. Any
  future dedicated-VM product needs this directory created alongside
  `HOME=/srv/dominion/home` itself, not as an afterthought.
- Scheduling ownership moved from local dev to this server, 2026-08-28: the
  local instance's copy of `tectonicusadaytrading`'s 3 schedules were
  disabled (`enabled: false` in its `workflow.json`) before a one-time
  `rsync` of the workflow's working files (excluding `.git`) from local into
  `/srv/dominion/data/docs/Workflow/tectonicusadaytrading/`, overwriting the
  stale 2026-08-24 copy. The 3 schedules were then re-enabled on the
  server's copy only. This server is now the sole source of scheduled runs
  for this workflow — re-enabling them locally would cause duplicate paper
  trades and duplicate market-data API usage against the same workflow.
- `encode zstd gzip` added to this site's Caddy block, 2026-08-28 — see the
  Verification section above. Missing since the original deploy; the main
  JS bundle now transfers at ~1 MB instead of ~3.7 MB.
- Gateway shared-password layer disabled, 2026-08-28: `GATEWAY_DISABLE_PASSWORD_GATE=true`
  added to `/srv/dominion/.env`, with `deploy/aws-ec2/server/auth-gateway.go`
  changed to make that an explicit per-deployment opt-out (default
  unchanged everywhere else, including Video Studio). Decision: with real
  per-user login (`manish`/`john`) already gating the app, the extra shared
  password was pure friction, not additional real protection for this
  deployment. Accepted trade-off: a handful of routes the agent API
  intentionally leaves public for pre-login bootstrap (`/api/health`,
  `/api/capabilities`, `/api/shared/*`, `/api/auth/*`) are now reachable
  from the open internet without any password — they were already reachable
  to anyone who cleared the shared password before, so this is a narrowing
  of what the shared password protected, not new exposure of anything the
  inner app didn't already intend to expose at that boundary. The gateway
  still runs and still routes/serves the frontend/proxies `/api`, `/api/wp`,
  `/ws` — only the password-session check is skipped.
- Stale local absolute paths in synced workflow state, found and bulk-fixed
  2026-08-28: the original local→server sync of `Workflow/tectonicusadaytrading/`
  (see the scheduling-ownership entry above) carried over 221 `builder/`
  conversation/session files whose `runtime.agent_session_handle.provider.working_dir`
  still pointed at the local dev machine's absolute path
  (`/Users/mipl/ai-work/mcp-agent-builder-go/workspace-docs/...`). Resuming
  any of those native Claude Code sessions failed with `SANDBOX_UNAVAILABLE:
  ... mkdir /Users: permission denied` (the launcher trying to `mkdir -p` a
  path that only exists on the original dev machine). Bulk-corrected with a
  literal string replace across `builder/` to the real server path
  (`/srv/dominion/data/docs/Workflow/tectonicusadaytrading`). **Any future
  full workflow-directory sync from a local/dev machine to a server must
  repeat this check** — grep the synced tree for the source machine's own
  absolute path before considering the sync complete; this class of bug is
  silent until a native coding-agent session tries to resume.
- Mount-namespace Landlock fallback was two stacked bugs, both fixed
  2026-08-28/29 — a `SANDBOX_UNAVAILABLE: Landlock cannot represent this
  Folder Guard policy and mount namespaces are unavailable: blocked-write
  path overlaps writable path` kept recurring even after the first fix
  landed, which is what exposed the second one:
  1. **`kernel.apparmor_restrict_unprivileged_userns` disabled host-wide**
     (`/etc/sysctl.d/60-dominion-userns.conf`, `sysctl -w`, persisted across
     reboots). Ubuntu 24.04 defaults this to `1`, blocking unprivileged
     user-namespace creation for any process without an explicit AppArmor
     profile, independent of `kernel.unprivileged_userns_clone` (which can
     show enabled while this still blocks it). Host-wide trade-off, chosen
     over a narrower Dominion-only AppArmor profile grant for speed —
     revisit if this shared VM ever needs the stricter posture back for an
     unrelated tenant.
  2. **The actual application-level bug, found after step 1 alone didn't
     fix it**: `workspace/security/isolator.go`'s `executeIsolatedMountNamespace`
     and `isolator_linux.go`'s `mountNamespaceAvailable` both ran a plain
     `unshare -m` (mount namespace only, no `-U`/`--user`). That needs real
     `CAP_SYS_ADMIN` in the *current* user namespace, which an unprivileged
     service account never has — it fails with `Operation not permitted`
     regardless of step 1's sysctl, confirmed by running the exact command
     as `dominion` and reproducing the identical failure. This made the
     fallback permanently non-functional for every rootless deployment,
     silently — it always compiled and started, it just could never
     actually execute anything. Fixed by adding `--user --map-root-user`
     (the standard unprivileged-mount-namespace pattern rootless container
     runtimes use), confirmed live against the exact overlap policy that
     was failing. Both fixes were independently necessary: step 1 alone
     doesn't help without step 2's corrected command, and step 2 alone
     would still be blocked by step 1's AppArmor restriction on an
     unconfined process.
  
  Why the fallback matters at all: Landlock's rule model is purely additive
  (no allow-with-carve-out), so it rejects any Folder Guard policy that
  needs one — concretely, "write access to a workflow folder except its
  `planning/` subfolder", a real, intentional policy the generic AgentWorks
  path requests that Dominion's own narrower profile never happened to
  trigger. The fallback is what makes that policy shape work at all on
  Linux.
- `MCP_API_URL=http://127.0.0.1:21000` and `NATIVE_WORKSPACE=true` added to
  `/srv/dominion/.env`, 2026-08-29. Without an explicit `MCP_API_URL`,
  `GetCodeExecAPIURL()` falls through to `http://host.docker.internal:21000`
  for custom-tool callbacks on any rootless (non-Docker) deployment —
  discovered live when a workflow chat's tool calls stopped resolving.
  See [`../ROOTLESS-LINUX-DEPLOYMENT-CHECKLIST.md`](../ROOTLESS-LINUX-DEPLOYMENT-CHECKLIST.md)
  item 4 for the general form of this gap.
- Foreground Claude Code turns could wedge for hours with the CLI itself
  already done — two stacked bugs, found and fixed 2026-08-30:
  1. **npm prefix misconfigured for the `dominion` service identity**:
     `npm config get prefix` resolved to `/usr` (the system default) rather
     than `/srv/dominion/tools`, because `/srv/dominion/.npmrc` was never
     written — only `/srv/dominion/tools/bin` being ahead on `PATH` made
     `claude` resolve correctly at all. Claude Code's own self-update then
     tried to write to `/usr` on every invocation, failed on permissions,
     and printed a persistent "✘ Auto-update failed: no write permission to
     npm prefix · Run claude doctor" footer at the bottom of every tmux
     pane. Fixed by writing `prefix=/srv/dominion/tools` to
     `/srv/dominion/home/.npmrc` — **the actual `HOME` the systemd services
     use (`Environment=HOME=/srv/dominion/home`), not an interactive SSH
     login's own `$HOME`**; verify with
     `HOME=/srv/dominion/home npm config get prefix`, not a bare
     `npm config get prefix` over SSH, which checks the wrong file.
  2. **`multi-llm-provider-go`'s tmux-pane completion scan didn't recognize
     that footer line**: `claudecode_interactive_adapter.go`'s
     `hasReadyInputPrompt` scans backward from the bottom of the pane for
     the idle `❯` prompt, skipping known non-prompt "footer" lines via an
     explicit allowlist (`isIgnorableClaudePromptFooterLine`). The
     auto-update-failure banner sits below the real idle prompt and wasn't
     on that allowlist, so the scan hit it first and reported "not ready" —
     even though the CLI had already answered 8 seconds earlier. The
     session showed no bug from the CLI's perspective at all; only the
     completion watch never fired, so the platform never surfaced the
     answer or freed the turn. Fixed by adding a
     `strings.Contains(trimmed, "Auto-update failed")` match to that
     allowlist (matched on the fixed substring, not the specific reason
     after it, since npm can fail this self-update for other causes too —
     network, disk — with the identical pane-boundary problem either way).
     Both bugs had to be fixed together: bug 1 is what put the footer on
     the pane in the first place, and fixing only bug 1 leaves any other
     transient cause of that same footer (or of any future CLI footer line
     not yet on the allowlist) able to reproduce bug 2's hang again.
  As defense in depth against the general failure shape (an unrecognized
  footer line permanently masking an idle prompt, of which this is the
  second confirmed instance after the upgrade-notice case), the shared
  library's disabled-by-default "stale pane backstop" is enabled for this
  deployment only —
  `CLAUDE_CODE_INTERACTIVE_STALE_PANE_BACKSTOP_SECONDS=7200` (2 hours) in
  `/srv/dominion/.env`. Deliberately **not** flipped as the shared library's
  global default: inferring turn completion from pane inactivity risks a
  false-positive "done" on a legitimately long-running turn, and that
  trade-off shouldn't be forced on every other product sharing this
  library without their own opt-in. 2 hours is chosen to sit safely above
  any real Dominion turn's expected duration while still bounding the
  worst case far below the "hours, indefinitely" this incident actually
  hit.
- Logging was silently broken since the original 2026-08-24 deploy, found
  2026-08-31 while trying to check for tool errors and finding nothing —
  see "Logs are required, not automatic" under Release requirements above
  for the two-part fix (systemd `StandardOutput=append:`, plus symlinking
  each release's `logs/` to the persistent `/srv/dominion/logs/`). All
  three services now log to `/srv/dominion/logs/{agent,workspace,gateway}.log`,
  continuously across releases.

## Outstanding items (checklist)

Kept here as the single place to check what's still open on this
deployment, instead of scattered across incident write-ups above. Update
this list — don't leave a fixed item unchecked, and don't let a new gap go
undocumented elsewhere only.

- [ ] **Cloudflare SSL/TLS mode not yet Full (strict).** The DNS record is
      currently unproxied (DNS-only), which is why certificate issuance
      worked without any Cloudflare-side change. Optional, not blocking —
      the account owner's call, since it changes what protects this
      hostname.
- [x] **No automated release script.** Fixed: `deploy/dedicated-vm/deploy-dominion.sh`
      now builds all 5 binaries + frontend natively on the box, stages a
      release, and (with `--activate`) flips `current` and restarts services
      with a health check + automatic rollback — mirroring
      the RTS deploy (now `./deploy.sh rts`) as originally proposed here. Also wired into the
      shared top-level `./deploy.sh dominion [--activate]` entry point, which
      SSHes in and invokes the on-box copy at its fixed path
      (`/srv/dominion/deploy-dominion.sh`).
- [ ] **`deliver-briefing` step reported `delivered: False status: failed`**
      on the `tectonicusadaytrading` workflow's 2026-08-31 scheduled run
      (`runs/iteration-0/default/execution/deliver-briefing/delivery_receipt.json`).
      Found while checking logs for an unrelated report, not yet
      root-caused — needs investigation on its own; may or may not be
      related to today's other fixes.

The logging gap this deployment found is shared code, not
Dominion-specific, so the open item to check other rootless deployments for
it (Video Studio) lives in
[`../ROOTLESS-LINUX-DEPLOYMENT-CHECKLIST.md`](../ROOTLESS-LINUX-DEPLOYMENT-CHECKLIST.md#9-server-logs-must-actually-reach-a-file-the-service-account-can-read),
not duplicated here.
