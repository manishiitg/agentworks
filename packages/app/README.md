# AgentWorks local launcher

Installs the Apple Silicon Mac app and its bundled `agentworks` command, then
opens the app. Re-running opens the existing app. Only `update` installs a newer
version. Existing project data and app/CLI sign-ins stay where they are.

This package is prepared for npm distribution; it is not published by building
or testing it. A combined desktop release with checksums must exist before
advertising the public install command.
Until a combined Mac release is available, use `--dmg` with `--sha256-file`
to install a locally built app, as shown below.

After publication, install and open the app with `npx @manishiitg/agentworks`.
The app remains AgentWorks and its bundled CLI remains `agentworks`.

## Test the unpublished package

From the repository:

```sh
npm pack ./packages/app --pack-destination /tmp
npm exec --yes --package /tmp/manishiitg-agentworks-0.1.0.tgz -- agentworks-app --help
```

Install a real locally built DMG into a separate location, leaving your usual
app, CLI and running AgentBridge alone:

```sh
npm exec --yes --package /tmp/manishiitg-agentworks-0.1.0.tgz -- agentworks-app \
  --dmg /tmp/AgentWorks-test.dmg --sha256-file /tmp/AgentWorks-test.dmg.sha256 \
  --app-dir /tmp/agentworks-test/Applications --bin-dir /tmp/agentworks-test/bin \
  --no-open
```

For an isolated first launch, set `RUNLOOP_USER_DATA_DIR`, `RUNLOOP_DOCS_DIR`
and `AGENTWORKS_STATE_ROOT` to directories under the test folder before running
the test app executable. Do not point these at your normal data or sign-ins.

## Source development

```sh
node packages/app/bin/app.mjs dev --help
node packages/app/bin/app.mjs dev
# From an unpacked npm launcher:
agentworks-app dev --repo /path/to/agentworks
```

This delegates to `run_agentworks`, preserving its Vite hot reload, backend
startup and flags. Local development opens Electron by default; browser-only
development is an explicit `--without-electron` option. It suppresses automatic
Git pulls. It does not install the
packaged app or change provider sign-ins. Source development still requires the
development tools documented in `desktop/README.md`.

Startup shows progress and actionable warnings by default. Routine setup output
goes to the startup log printed at launch; service logs stay in their usual
locations. Add `--verbose` to show setup details in the terminal:

```sh
npx @manishiitg/agentworks dev --repo /path/to/agentworks --verbose
```

## Installed app

The installer links `~/.local/bin/agentworks` to the CLI in the app. If that
directory is not on PATH, it prints the line to add; it does not edit shell files.
An existing standalone CLI is kept unless `--update` was requested and it is
identified as AgentWorks. Unrelated commands are never replaced.

`agentworks open` opens the app. `agentworks start` still connects a computer to
a server. Installing does not sign in, attach a chat, publish, share folders, or
enable computer-use permissions. Use the app's Check for Updates action to
update the app and its bundled CLI together.
