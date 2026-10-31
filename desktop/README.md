# AgentWorks — Desktop

Electron shell for the standalone Mac app. It bundles the `agent-server` and `workspace-server` binaries, managing their lifecycle automatically.

The combined installer also installs the bundled `agentworks` CLI. Release
packages include `mcpbridge`, `tmux` and its native libraries; end users do not
need Go or Homebrew to install AgentWorks. Provider CLIs and their sign-ins are
configured separately in the app. Source development still uses
`../run_agentworks` and Vite hot reload. The optional npm launcher delegates its
`dev` command to that same script. See [local installer testing](../packages/app/README.md).

Before packaging a release, run `bash scripts/build-desktop-helpers.sh` from
the repository root on the target Mac architecture. CI does this before
electron-builder. Publish each DMG with its `.sha256` file. An existing app is
only replaced with `--update`; `agentworks open` opens the app without updating.

The local app offers workflows (Goals) and Crew. Code, Relays, Brain, and Vault are server products and are excluded from local installations. The
DMG neither includes nor starts a Vault sidecar. Project MCPs and secrets remain
available within workflows and Crew. Existing workspace and Vault data are
not deleted during upgrades.

## Prerequisites

- Node 18+
- Go 1.22+ (to build backend binaries)
- macOS (Apple Silicon or Intel)

## Development Setup

The `dev-setup.sh` script builds the Go binaries and installs Node dependencies:

```bash
cd desktop
./dev-setup.sh
```

## Run Locally (Unpackaged)

```bash
cd desktop
npm start
```

This starts Electron; the main process will check ports **45678** and **45679**, spawn both servers from `resources/`, wait for health, then open a window at `http://127.0.0.1:45678`.

## Development with Docker

If you are already running the frontend and workspace services via Docker Compose, you can run the Electron shell as a wrapper without spawning internal servers:

1.  **Start your Docker services:**
    ```bash
    docker-compose up
    ```
2.  **Start Electron in "Dev Mode"** by pointing it to your local frontend:
    ```bash
    cd desktop
    DEV_URL=http://localhost:5173 npm start
    ```

An arbitrary `DEV_URL` wrapper does not enable connected servers. Use the local launcher below so the backend and Electron discover the same private local socket.

For connected-server testing with Vite, run the full local launcher yourself from a terminal. It compiles the backend locally:

```bash
cd agent_go
./run_server_with_logging.sh
```

The launcher builds the backend once and starts Vite and the workspace service. Electron starts the backend through the same private credential pipe as the packaged app. The left sidebar then shows **Local** and **Connect server**. No Mac package is required.

You can also keep backend and frontend in separate terminals. Run these yourself from `agent_go` (the backend launcher compiles locally):

```bash
# Terminal 1: backend and workspace
./run_server_with_logging.sh --with-workspace

# Terminal 2: Vite and Electron
./run_server_with_logging.sh --only-frontend
```

Connections work in both layouts. The split backend creates a private Unix socket for Electron, scoped to the checkout and backend port. Only Electron's main process receives connection proof; nothing is stored in browser storage or a credential file. Restart the backend independently on the same port and Electron gets fresh proof for its next connection request. The full launcher and packaged app continue using fd 3. Restart both launchers once after updating to enable the socket; future backend restarts can be independent.

## Build & Package (Distribution)

### Quick local build (unsigned, no GitHub upload)

```bash
cd frontend
npm ci
VITE_DEPLOYMENT_MODE=local npm run build
rm -rf ../agent_go/static
mkdir -p ../agent_go/static
cp -R dist/. ../agent_go/static/
cd ../desktop
./dev-setup.sh                                              # only first time / when Go code changes
CSC_IDENTITY_AUTO_DISCOVERY=false \
  npx electron-builder --mac dmg --publish never
```

Artifacts use the `AgentWorks-<version>-arm64.dmg` filename and contain `AgentWorks.app`. Install: `open desktop/dist/AgentWorks-*.dmg` → drag AgentWorks to Applications. First launch: right-click → Open to bypass Gatekeeper.

### Cutting a release via the release script

Releases are built by `.github/workflows/desktop-release.yml`. The `release` job runs on tag pushes (`v*`).

Use the repository release script as the only supported entry point:

```bash
scripts/desktop-release.sh --dry-run v1.25.111
scripts/desktop-release.sh v1.25.111
```

The script selects the `manishiitg` GitHub account, switches to `main`, requires a clean tree and exact parity with canonical `origin/main`, verifies the target is newer than the current Latest release, updates and commits both desktop version files, generates release notes, tags the exact commit, waits for the DMG workflow, publishes the draft, and verifies every updater asset.

Merge and push all product changes before running it. The script will not publish arbitrary local commits that are merely ahead of `origin/main`.

### Versioning notes

- Pull requests build one review artifact. Main pushes build one commit artifact. Tag pushes run the release job.
- The script commits package and lockfile versions before tagging. The workflow retains an idempotent version check as a defensive backstop.
- The dmg is **unsigned** (no `mac.identity` / no notarization). Existing users right-click → Open on first launch. To ship signed/notarized builds, add `mac.identity` to `package.json` and provide an Apple Developer ID + notarization credentials via repo secrets.

### What goes in the dmg

- `main.js`, `preload.js`, `settings.html`, `auth-prompt.html` (renderer/main)
- `resources/agent-server`, `resources/workspace-server` (Go binaries built by `dev-setup.sh` or CI)
- `resources/icons/`, `agent_go/configs/`, `agent_go/static/` via `extraResources` in `package.json`

### Host prerequisites

- Claude Code experimental mode requires `tmux` 3.x or newer as a local runtime dependency. The curl installer attempts `brew install tmux` when Homebrew is available; otherwise install it manually with `brew install tmux`.
- The curl installer ensures `mcpbridge` is installed to `~/go/bin` for Claude Code/Codex MCP bridge access. If Go is missing, it installs Go through Homebrew when available; otherwise it asks the user to install Go and rerun the same curl command.

## Runtime Details

### Window and Server Lifecycle

On macOS, closing the AgentWorks window keeps the app process and bundled servers running in the background so scheduled jobs can continue. Click the Dock icon or the AgentWorks menu bar icon to reopen the UI.

Right-click the menu bar icon and choose **Quit AgentWorks (Stop Servers)**, or use **Quit AgentWorks** / `Cmd+Q`, to fully exit the app. Quitting stops both bundled servers.

### Ports
- **45678**: Agent server (API + static frontend)
- **45679**: Workspace server

### Data & Logs
The app stores data in the system's Application Support directory:
`~/Library/Application Support/runloop-desktop/`

The historical data-directory name is retained deliberately so upgrading from
`Runloop.app` to `AgentWorks.app` does not create an empty workspace.

The data path is still legacy-compatible while the rename is in progress. Do not rename it blindly without a migration.

- **Logs:** `logs/agent.log`, `logs/workspace.log`
- **Database:** `chat_history.db`
- **Configuration:** `configs/mcp_servers.json`
- **Workspace Data:** `data/` and `workspace-docs/`

If ports are in use or servers fail to start, the app shows an error dialog and exits. Check the log files in the directory above for details.
