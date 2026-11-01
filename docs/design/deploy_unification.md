# Unified release builds and product activation

Build a release once on the configured Linux build host. Each deployment target
copies that release and activates it with its private product configuration.
The triggering laptop does not compile the server or frontend.

## Build output

`deploy/common/build-release.sh` records the application, engine and provider
revisions, builds the binaries and complete frontend, and produces a release
manifest with file hashes and runtime requirements. Deployment activation checks
that manifest before installing the selected build.

The frontend is one complete generated directory: its HTML entry point, imported
chunks, public assets, dashboard runtime and document previews must agree.
Do not combine a new entry point with old chunks or copy one changed chunk into
an older bundle. Use `frontend/scripts/check-release-assets.mjs` against the
packaged directory.

## Per-target configuration

Hosts, credentials, runtime settings, branding and service paths live in the
private deployments repository. The public deployer reads that configuration;
it does not contain customer-specific host tables. Product activation uses the
existing target's data and services. Building or verifying a release alone does
not deploy it.

## Verification

`./deploy.sh verify <name>` runs the commit's build, type checks and selected
regressions as an unprivileged account on the build host with resource limits.
The normal deployment path remains `./deploy.sh <target>`. See
[the deployment guide](../../deploy/README.md) for current commands and
[the rootless pipeline](../../deploy/rootless-linux/README.md) for activation.
