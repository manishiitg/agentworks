# Rootless Linux product deployments

A repeatable redeploy pipeline for fixed-workspace products running as their
own isolated Linux account on a shared rootless-systemd host, generalized
from server C's original deployer. One host can run several products, each under
its own system account (`sparkquill`, `confida`, `dominion`, ...); this
pipeline only ever touches the one account and `$PRODUCT-*` systemd units
named on the command line.

Server C and SparkQuill use this shared pipeline. Its local half is the
`deploy_rootless_product` function in the repository-root `deploy.sh` (the only
deployment entry point); this directory holds the server-side half and the
per-product configuration. Video Studio and Dominion have their own cases in
`deploy.sh` because their host/bootstrap contracts differ.

## How it works

(This is the original on-server build path, still used by `DEPLOY_BUILD_MODE=server`; the default builds once, see
"Build once, deploy everywhere" below.)

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
   (with a `SOURCE_REVISIONS` file recording the exact commit of every repo, and
   the source of all three repos under `source/`, without `.git`, dependency or
   build folders, pruned with the release),
   waits for the current release to drain in-flight turns, swaps the
   `current` symlink, restarts the three systemd units, verifies the
   running processes actually received the expected environment, health
   checks both loopback ports and the public domain, then prunes old
   releases (only ones with no process still referencing them, and only
   after a health check confirms the new release is up).

## Build once, deploy everywhere (PLAT-426)

By default `./deploy.sh <excellence|confida|sparkquill|all-hetzner|rts>` no longer compiles on the target. The release is built
**once** on the Hetzner box and each server only copies and activates it:

1. `deploy/common/build-release.sh` (shipped and run over ssh as `root` by `deploy.sh`, under `systemd-run` limits: 800% CPU,
   12 GB, nice 10; set `BUILD_AS=<user>` to build as an unprivileged account that owns the builds folder) fetches the three
   repositories at the revisions `deploy.sh` resolves (head of `DEPLOY_BRANCH`, default `main`) and builds into
   `/srv/_builds/<builder-sha8>-<utc timestamp>/`: `bin/` (agent with cgo native STT and `bin/lib`, workspace, gateway, browser,
   landlock runner, slotctl, slottmux, mcpbridge, workspace-security.test), `frontend/`, `static/`, `downloads/`, `packages/`,
   `source/` (the three repos without `.git`), `SOURCE_REVISIONS` and `manifest.json` (revisions, os, arch, glibc, build time and
   sha256 of every file; `deploy/common/release_manifest.py`). The folder is world-readable, so every product account can copy
   from it. A build of the same three revisions is reused. Old builds are removed by default: after every deploy (and after each build)
   only the newest build is kept, plus pinned ones and any younger than 15 minutes (`./deploy.sh prune-builds` does it now;
   `BUILD_KEEP=2` keeps more). The products keep their own previous releases for rollback.
2. `bootstrap-build.sh` (given a `prebuilt` file instead of repository URLs) runs `build-and-activate.sh --prebuilt <build>` from
   the build's own `source/`. It first verifies the manifest: wrong CPU architecture, a build that needs a newer glibc than the
   host has, a missing, changed or unlisted file, or an agent that cannot load its libraries all **refuse** the deploy before
   anything is touched. Then it copies the files into the product's own `releases/<id>/` (binaries renamed `<product>-agent`,
   `-workspace`, `-gateway`; runtime-config, brand, MCP catalog and `downloads/version.json` are product-specific and made at
   this step) and runs the **same activation as before**: state dirs, standard runtime profile env, units and drop-ins, drain
   (`DEPLOY_DRAIN_SECONDS`), restart, `deployment_checks`, managed Chrome, profile report, release pruning.
3. RTS: `./deploy.sh rts` gets a trimmed copy of the same build (no `mcpagent`/provider source, no `downloads/`) onto RTS, checks it
   against the manifest hash read from the build host, and runs the unchanged server A activation without compiling. Server A downloads the copy
   itself from GitHub (next section); the old stream through this machine is the fallback.

```
./deploy.sh build                      # build (or reuse) the build of main's head; deploys nothing
./deploy.sh builds                     # list builds: name, age, build seconds, the three revisions, pinned
./deploy.sh <server>                 # build once if needed, then copy + activate
./deploy.sh all-hetzner                # Server B, server C, sparkquill in sequence from ONE build (never dominion)
./deploy.sh <server>                        # same build, shipped to server A
./deploy.sh <server> --build 7357c770   # deploy an existing build (name or any revision prefix); its three revisions must be
                                       # ancestors of origin/main, so a known-good older build can go out while main is held
./deploy.sh pin 7357c770               # keep that build from being pruned by later builds ("unpin" to release it)
DEPLOY_BUILD_MODE=server ./deploy.sh <server>   # the original path: the server clones main and compiles (fallback)
```

Build host settings: `BUILD_HOST`, `BUILD_PORT`, `BUILD_USER` (default root; host and port in the private deployments/deploy.env), `BUILD_SSH_KEY`, `BUILDS_DIR`
(/srv/_builds), `BUILD_CPU_QUOTA`, `BUILD_MEMORY_MAX`, `DEPLOY_FORCE_BUILD=1` (rebuild even if the revisions are already built).

Rehearse the copy without touching a product: `build-and-activate.sh <build>/source <product> --prebuilt <build> --stage-only` with
`DEPLOY_APP_ROOT=<scratch dir>` assembles the release in the scratch folder and stops before preflight, `current` and every service.
Dominion has its own script and is not part of this.

### Builds on GitHub (PLAT-426)

The build host publishes each build to the public repo `github.com/manishiitg/agentworks-builds` (releases only; tag
`build-<builder8>-<mcpagent8>-<provider8>`; assets `build.tar.gz`, `build-rts.tar.gz`, `manifest.json`, `SHA256SUMS`; newest 8 kept;
never marked latest). `build-release.sh` does it after a build, best effort (`BUILD_PUBLISH=0` skips). It needs a write token on the
build host: a fine-grained token (only `agentworks-builds`, Contents read and write, 90 days) as the single line `GH_TOKEN=...` in
`/root/.config/agentworks/builds.env`, mode 600 (`install -d -m 700 /root/.config/agentworks`; a looser file is refused). Without it
publishing prints one message and is skipped, and deploys stream as before. To rotate: new token, overwrite the file, delete the old token.

```
./deploy.sh publish [build]            # upload the newest (or the named) build now; prints Published: <build> -> <tag>
./deploy.sh builds                     # also lists the builds on GitHub
DEPLOY_BUILD_TRANSPORT=github ./deploy.sh <server>   # Server A must download it (no fallback); "stream" forces the old path; default "auto"
```

`deploy/common/fetch-build.sh <tag> <asset> <manifest-sha256> <dest>` is what RTS runs: it downloads anonymously (resume, retries), refuses
unless `manifest.json` has the announced hash, and leaves nothing behind on failure. Tests: `deploy/common/test_publish_build.py`,
`test_deploy_transport.py` (a fake GitHub API, `fake_github.py`).

## Adding a new product

Real server configs live in the PRIVATE deployments repository (`runloop-workflows/deployments`), checked out next
to this one as `../deployments` (or `AGENTWORKS_DEPLOYMENTS_DIR`): `products/<name>/`. `deploy.sh` reads them from there
and ships the folder with each deploy, so customer names, hosts, endpoints and people never enter this public
repository. A dedicated host (ProxyJump `SSH_JUMP`, a root `HOST_SETUP_SCRIPT` kept in the product folder, a downloaded
build `PREBUILT_DELIVERY=fetch`, nginx, a Pi provider template `pi-agent/`) is configured the same way.

Copy `products/example/` into the deployments repository as a starting point:

- `product.env` — ports, provider/model, which CLIs to install, and any
  `EXTRA_ENV` the systemd units need beyond what's already in
  `/srv/<product>/.env`. See the comments in `products/example/product.env`
  for what each field means and which ones are safe to leave at their
  defaults.
- `runtime-config.js` — the frontend's `window.__APP_RUNTIME_CONFIG__`.
- `brand/` (optional) — the deployment's own logo, mark and favicon, served at
  `/brand/`. Point `runtime-config.js` at them: `appName`, `faviconUrl`,
  `markUrl` (sign-in card), `logoUrl` / `logoDarkUrl` (top bar) and
  `brandColor` (#rrggbb, the primary colour in every theme).
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
./deploy.sh <server>
```

Run those commands from the repository root; `deploy.sh` is the only
entry point (there is no separate per-directory deployer).

Env overrides (all default from `product.env`): `HOST_IP`, `SSH_PORT`,
`SSH_KEY_PATH`, `DEPLOY_BRANCH` (defaults to `main`).

## Slot-enabled servers: the slot self-test (PLAT-478)

On a host with per-user accounts (`SLOTS_ENABLED=true` in `product.env`, or a readable `/etc/agentworks/slots.json`; RTS too),
every deploy:

- sets `<app>/releases` to `0711` and gives the new release folder and its `bin/` search permission (`o+x`), so slot accounts
  can reach `bin/video-studio-landlock-runner` (`slots_release_traversal` in `deploy/common/slots.sh`; nothing becomes readable);
- after activation, runs `<release>/slotcheck.sh` as the service account. It is read-only. It checks `slotctl.json` (readable,
  `docs_root` = the service's docs root, `allowed_cwd` covers it, `allowed_exec` lists the launcher), the slot table (root-owned,
  the service's group, `0640`, readable), that every folder from `/` to the launcher is traversable by each slot, and then runs a
  real `pwd` through sudo + slotctl + the launcher with the shell tool's own grant builder, for each assigned slot and for one
  unassigned test slot (the highest-numbered free slot account), in the docs root, a workflow, a Crew project and a Code project
  (the user's own first project; the test slot uses its own state folder). It prints slot names and counts only, never who holds
  a slot. A secret admission scan (names only; selected secrets that exist nowhere, shared secrets without a Vault Platform grant)
  follows as warnings.
- A `FAIL` line says what failed and how to fix it, and the deploy exits 1. The release is already active: nothing is rolled
  back. Fix the cause (usually as root through `provision-slots.sh` / `deploy/aws-ec2/slots-admin.sh`) and re-run the check alone:

```
./deploy.sh slotcheck <server>      # any deployment target
```

Do not assign a slot to anyone on a server whose self-test fails. A slot command never receives a browser profile, a browser
socket folder or the app state (see `docs/DECISIONS.md`); it uses the project's browser through the platform.

## Managed Chrome under the shell sandbox

`build-and-activate.sh` now does this on every deploy (`install-managed-chrome.sh`): it installs the launcher beside the host's
Chrome (an existing `<app>/tools/chrome/current`, else `tools/chrome/system` with `chrome -> /opt/google/chrome/chrome` and
`current -> system`) and the standard runtime profile sets `AGENT_BROWSER_EXECUTABLE_PATH=<app>/tools/chrome/current/chrome-agentworks`.
The launcher also gives Chrome a writable HOME/XDG dir under its private temp dir: with the service's own HOME (read-only in the
sandbox) Chrome died at startup without writing DevToolsActivePort. The manual notes below remain accurate for a pinned Chrome.

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
