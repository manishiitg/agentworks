# Rootless Linux product deployments

A repeatable redeploy pipeline for fixed-workspace products running as their
own isolated Linux account on a shared rootless-systemd host, generalized
from Confida's original deployer. One host can run several products, each under
its own system account (`sparkquill`, `confida`, `dominion`, ...); this
pipeline only ever touches the one account and `$PRODUCT-*` systemd units
named on the command line.

Confida and SparkQuill use this shared pipeline. Its local half is the
`deploy_rootless_product` function in the repository-root `deploy.sh` (the only
deployment entry point); this directory holds the server-side half and the
per-product configuration. Video Studio and Dominion have their own cases in
`deploy.sh` because their host/bootstrap contracts differ.

## How it works

1. `./deploy.sh <product>` runs on your machine. It reads
   `products/<product>/product.env`, installs/updates the product's CLI
   dependencies over SSH, then ships `bootstrap-build.sh` plus the branch
   name and repo URLs to `<product>@<host>`. **It never builds anything
   itself** — no local go/node/npm required to trigger a deploy.
2. `bootstrap-build.sh` runs on the server. It takes a `flock` on
   `/srv/<product>/deploy.lock` (refuses a second concurrent deploy), clones
   fresh depth-1 checkouts of all three repos at the requested branch, then
   hands off to `build-and-activate.sh` **from that fresh checkout** — so the
   build logic always matches whatever is on the deployed branch, never a
   stale copy cached on the triggering machine.
3. `build-and-activate.sh` builds the four Go binaries and the frontend,
   assembles a new immutable release under `/srv/<product>/releases/<rev>-<timestamp>/`
   (with a `SOURCE_REVISIONS` file recording the exact commit of every repo),
   waits for the current release to drain in-flight turns, swaps the
   `current` symlink, restarts the three systemd units, verifies the
   running processes actually received the expected environment, health
   checks both loopback ports and the public domain, then prunes old
   releases (only ones with no process still referencing them, and only
   after a health check confirms the new release is up).

## Adding a new product

Copy `products/sparkquill/` as a starting point:

- `product.env` — ports, provider/model, which CLIs to install, and any
  `EXTRA_ENV` the systemd units need beyond what's already in
  `/srv/<product>/.env`. See the comments in `products/sparkquill/product.env`
  for what each field means and which ones are safe to leave at their
  defaults.
- `runtime-config.js` — the frontend's `window.__APP_RUNTIME_CONFIG__`.
- `mcp-servers.override.json` (optional) — the MCP catalog is the shared
  `agent_go/configs/mcp_servers_clean.json` for every deployment; this file
  holds only what the product does differently (an entry replaces the shared
  one, `null` removes one). Built by `deploy/common/build-mcp-catalog.py` into
  `configs/mcp_servers_<product>.json`.

Requirements this template assumes:

- The product's systemd units are already installed and enabled (one-time
  account/unit/Caddy-site bootstrap is out of scope for this pipeline)
  — `<product>-agent`, `<product>-workspace`, `<product>-gateway`,
  each `WorkingDirectory=/srv/<product>/current` and loading
  `/srv/<product>/.env` via `EnvironmentFile=`.
- `/srv/<product>/.env` already has whatever secrets and product-specific
  settings the deployment needs (`WORKSPACE_DOCS_PATH`,
  `AGENT_BROWSER_SHARED_PROFILE`, provider API keys, ...) — this pipeline
  only ever rewrites the `PATH=` line in that file (see the comment in
  `build-and-activate.sh` for why: `EnvironmentFile=` wins over a systemd
  drop-in's `Environment=` for the same key, so a stale `PATH=` there would
  otherwise silently outrank the managed one).
- Product-specific one-time migrations or catalog copies (Workflow Builder
  chat migration, playbook catalog) are optional per product via
  `RUN_WORKFLOW_BUILDER_MIGRATION` / `COPY_PLAYBOOKS` in `product.env` — leave
  both `false` for a fixed single-workspace product like SparkQuill that has
  neither concept.

## Usage

```
./deploy.sh sparkquill
./deploy.sh confida
```

Run those commands from the repository root; `deploy.sh` is the only
entry point (there is no separate per-directory deployer).

Env overrides (all default from `product.env`): `HOST_IP`, `SSH_PORT`,
`SSH_KEY_PATH`, `DEPLOY_BRANCH` (defaults to `main`).

## Managed Chrome under the shell sandbox

SparkQuill uses the `chrome-agentworks` launcher alongside its pinned Chrome
for Testing binary. Install it into the same version directory as `chrome`
and set the service environment to its stable symlink path:

```
AGENT_BROWSER_EXECUTABLE_PATH=/srv/sparkquill/tools/chrome/current/chrome-agentworks
```

The launcher uses a private, service-owned `/tmp/aw-browser-<uid>` directory
which survives individual commands. Shell commands retain their existing
per-command scratch cleanup. It also adds `--disable-dev-shm-usage`,
`--no-zygote`, and `--in-process-gpu` for the existing headless `--no-sandbox`
launch configuration. These avoid Chrome startup operations denied by the
Landlock policy without broadening `/proc` access or disabling Landlock.
Keeping the wrapper next to Chrome preserves the executable-directory read
grant already derived from `AGENT_BROWSER_EXECUTABLE_PATH`.

When upgrading Chrome, install this wrapper alongside the new binary before
switching `current`. On a new host, installing Chrome and the wrapper is still
a bootstrap step; `deploy.sh` does not download Chrome. Both `tools/` and this
`.env` setting survive application redeploys. After changing the environment,
wait for `/api/health` to report `drain.idle=true`, then restart the product's
workspace and agent services. Existing browser daemons must be closed before
they can adopt a new executable.

Run the regression check as the product account on the host:

```
python3 verify-managed-chrome.py sparkquill --port 23001 --cycles 20
```

It creates an isolated profile, runs 80 separate sandboxed browser commands,
requires the same Chrome PID throughout, checks a PNG screenshot, and verifies
localStorage survives close/reopen. It cleans up its successful test profile
and does not read or change a real user's login. Use this after browser upgrades;
a sequence that merely reports successful commands can conceal browser crashes
and automatic relaunches.
