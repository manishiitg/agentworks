[← Platform issue index](../../pulse_platform_issue_register.md)

# PLAT-426 — Build a release once and deploy it everywhere (deploys take 5–7 minutes per product because each server builds the same source)

| Coordination | Value |
|---|---|
| State | fixed on `main` (2026-10-04); not deployed; the first real deploys are for the owner's session |
| Severity | P3 (speed and consistency: the same commit is compiled up to six times, and each build can differ) |
| Date | 2026-10-04 |
| Owner | scheduler-runs (deploy tooling; see PLAT-405) |
| Related | PLAT-405 (deploy behaviour and unification) |

## Problem

Every deploy clones the three repositories on the target server and builds there (Go with cgo for native STT, the AgentWorks CLI downloads, the frontend). Measured on 2026-10-04:
Excellence 4 min 57 s, 4 min 57 s, 5 min 0 s, 5 min 5 s; RTS 7 min 3 s and 6 min 54 s (RTS has 2 cores, the Hetzner box 16).
Excellence, Confida, SparkQuill and Dominion share one Hetzner box, so a release of all four builds the same source four times. The server also needs the full build toolchain and network access to clone and install.

## Facts that make one build possible

Hetzner box and RTS are both x86_64, Ubuntu 24.04, glibc 2.39. A binary built on one runs on the other.

## What was built

All of it is on `main`; `deploy/rootless-linux/README.md` has the commands.

1. **`deploy/common/build-release.sh`** (run by `deploy.sh` over ssh as root on the Hetzner box under `systemd-run`, 800% CPU, 12 GB,
   nice 10; `BUILD_AS=<user>` runs it as an unprivileged account instead). Fetches the three repositories at the revisions `deploy.sh`
   resolves (head of `DEPLOY_BRANCH` via `git ls-remote`) and builds once into `/srv/_builds/<builder-sha8>-<utc>/`: `bin/` (cgo agent
   with `bin/lib` sherpa-onnx + onnxruntime, workspace, gateway, shared-browser, landlock runner, slotctl, slottmux, mcpbridge,
   workspace-security.test, update-coding-clis), `frontend/`, `static/`, `downloads/` (CLI, 4 targets), `packages/`, `source/`,
   `SOURCE_REVISIONS`, `manifest.json`. It reuses the existing build steps (`build-linux-agent.sh`, `slots_build`, the same go/npm
   commands) and reuses an existing build of the same three revisions. Old builds are pruned by default (owner, 2026-10-04): only the newest is kept, plus pinned ones and any younger than
   15 minutes (an activation or the RTS shipment may still be reading it); `build-release.sh --prune-only`, run by deploy.sh after every successful deploy
   (once at the end of all-hetzner) and by `./deploy.sh prune-builds`; `BUILD_KEEP` raises the number. Test `test_prune_builds.py` (run on the box as the unprivileged `agents` account). Builds into `<name>.partial` and renames, so a folder without a trailing `.partial` is complete. Go is installed pinned
   (1.27.1, checksum-verified, as `bootstrap-build.sh` does) into `/srv/_builds/.toolchain` because root has none.
2. **`deploy/common/release_manifest.py`**: `manifest.json` = revisions, os, arch, glibc, build seconds, node/go versions, sha256, size
   and executable bit of every file and every symlink target. `verify` refuses a build for another architecture, one that needs a
   newer glibc than the host's, a missing / changed / unlisted file, a changed symlink, or a manifest whose own hash differs from the
   one announced by the build host. `list` and `find` back `./deploy.sh builds` and `--build`.
3. **`deploy/rootless-linux/build-and-activate.sh --prebuilt <build>`** (given by `bootstrap-build.sh` when `deploy.sh` ships a
   `prebuilt` file): verifies the manifest first (plus `ldd` on the agent), copies into the product's own `releases/<id>/` (binaries
   renamed `<product>-agent|-workspace|-gateway`; runtime-config, brand, title patch, MCP catalog, playbooks, `downloads/version.json`
   stay product-specific and are made at this step) and then runs the same activation as before (state dirs, runtime profile env, units
   and drop-ins, drain, restart, `deployment_checks`, managed Chrome, profile report, pruning). The compile steps are an `else` branch,
   so the on-server build is intact as the fallback (`DEPLOY_BUILD_MODE=server`). Copies use plain `cp -R`, not `-a`: the existing
   carried-over-asset cleanup deletes by file age, and an old build's assets must not be deleted for the build's age.
   `--stage-only` (with `DEPLOY_APP_ROOT=<scratch>`) assembles the release and stops before preflight, `current` and any service.
4. **`./deploy.sh`**: `excellence|confida|sparkquill` build once or reuse, then activate prebuilt; `all-hetzner` builds once and
   activates the three in sequence, stopping at the first failure; `build` (build only, deploys nothing), `builds` (list: name, age,
   seconds, the three revisions, pinned), `pin|unpin <build>`, `--build <name|sha>` (deploy a chosen existing build). Dominion's script and
   case are untouched and `all-hetzner` does not include it.
5. **RTS**: `./deploy.sh rts` ships a trimmed copy of the same build (no mcpagent/provider source, no `downloads/`, which RTS never
   had; about 180 MB compressed, streamed build host -> this machine -> RTS) with the manifest hash read from the build host. The RTS
   `build-and-activate.sh --prebuilt` verifies it, then only the compile step differs: Secrets Manager `.env` merge, `systemd-run`
   limits, units reinstalled every deploy, preflight, hyperframes browser, CloudFront usage print are unchanged. RTS can safely use the
   prebuilt build: same x86_64, Ubuntu 24.04, glibc 2.39, and the agent's shared libraries (libstdc++, libgcc_s, libc, libm) are plain Ubuntu
   packages plus the shipped `bin/lib`; the verification refuses it on any mismatch and `ldd` on the agent must resolve.

## Choosing an older build (owner's addition, 2026-10-04)

While main is held back (PLAT-428 contract 1.0.45 must not go out), `./deploy.sh <server> --build <name|sha>` deploys an existing
build selected by build name or any revision prefix (`./deploy.sh builds` lists them). The build's three revisions must each be an
ancestor of `origin/main` of the corresponding repository (checked locally with `merge-base --is-ancestor` after a fetch), otherwise
it refuses; RTS's "main only" rule is relaxed to that ancestry rule only for a chosen prebuilt build. `./deploy.sh pin <build>` keeps a
known-good build from being pruned by later builds. `./deploy.sh build` only builds.

## Measured (2026-10-04, Hetzner box, 16 cores, build limited to 800% CPU / nice 10 while the products ran)

- Build once, cold caches (first ever: Go module download, npm cache empty, Go toolchain installed): **314 s**. Binaries 131 s, CLI downloads 47 s, frontend 122 s.
- Build once, warm caches (a forced rebuild of the same revisions): **176 s** (binaries 47 s, CLI downloads 5 s, frontend 115 s, manifest 4 s).
  Reusing an existing build of the same revisions: about 1 s. Two deploys of the same commit therefore cost one build.
- Manifest verify (7,917 files, 486 MB) plus copy into a product release, stage only, on the box: sparkquill 1.4 s (refused a build
  dirtied by a stray `.pyc`, now prevented), Excellence 2.8 s, Confida 12.8 s (the playbook validators, as before).
- Estimate for a deploy of one product with a prebuilt build: the product's existing tool installs and activation (drain 0, restarts,
  health waits) plus about 3-13 s of copy; the compile (about 5 min per product before) is gone. `all-hetzner` = one build (about 3 min
  warm) + 3 copies instead of 3 x 5 min. The coding-CLI and tool update step of `deploy.sh` runs on every deploy as before and was not measured.
- RTS: 180 MB compressed tarball (9 s to produce on the box); the transfer time depends on the link of the machine running `deploy.sh`.
  RTS no longer compiles at all (it was 7 min with 2 cores).

## Not verified

- No server was deployed, restarted or stopped, and no product's `.env`, units or releases were touched (owner: no deploys for now).
  The activation was rehearsed with `--stage-only` into scratch folders (Excellence, Confida, SparkQuill products); the restart,
  drain, deployment_checks and verification part is the unchanged code but has not run on a prebuilt release.
- RTS prebuilt activation was never run (RTS was not touched, `aws` not used): its copy step is covered by tests (`prebuilt_copy_bin`,
  trimmed-tarball verification) only. The first RTS deploy should be watched; a failure before the `current` swap removes the new
  release and leaves the old one running.
- The frontend is built once with the build host's system Node (v22), not each product's pinned Node 24 (Confida, SparkQuill).
  The output is plain Vite static files; if a difference ever shows, pin Node in `build-release.sh`.
- Builds are not byte-reproducible across rebuilds (no `-trimpath`; the build path is in the binary); one build per commit is what is
  shared, so this does not matter for the "same binary everywhere" goal.

## First real deploys (2026-10-04)

- Excellence, prebuilt from build `a78c5e41-20261004051939` (build 180 s): manifest verified (7,927 files, x86_64, glibc 2.39), live in 4 min 50 s including the build, 0 differences from the profile, site 200, units active.
- Confida failed before touching anything: the first playbook validation ran in the shared build's read-only `source/` as the `confida` account, and the playbook tests create a temporary folder beside the playbooks (`PermissionError`). The old path ran it in the product's own clone.
  Fix: with `--prebuilt` the shared source is not validated in place; `build-release.sh` validates the playbooks once in a scratch copy and each product validates its own release copy. Test `test_playbook_validators_never_write_into_the_shared_build_source`
  (run on the box as the unprivileged `agents` account: it passes, and against the old script it fails with the same PermissionError).
- SparkQuill: `ssh sparkquill@host` is refused for the deploy key (`id_ed25519`, which works for `agents@`); the "Too many authentication failures" message came from SSH offering several agent keys. `deploy.sh` now sets `IdentitiesOnly=yes` when the named key file exists, so the real error shows. (A first version set it always and broke Confida: its default key file `~/.ssh/confida_deploy` does not exist on the owner's Mac, so ssh must stay free to use the agent's key; the Confida redeploy aborted at its first connection, before any change.)
  SparkQuill needs its own deploy key (or the deploy key added to its `authorized_keys`); the owner is handling that with another agent.

## Left

- A dedicated unprivileged builder account (`BUILD_AS`): `npm ci` runs package install scripts, today as root on the shared box.
- Build per commit in CI so a deploy never waits for a build (optional, as planned).
- Hashes in the drift report: print the running release's manifest hash in `profile_report.py` so identical binaries across the four
  Hetzner products and RTS are visible in `./deploy.sh report`.
- Dominion stays on its own script (owned by another session).
- Measure the product tool-install phase (coding CLIs, gog, slack CLI) of `deploy.sh`, now the larger part of a prebuilt deploy.

## Acceptance

One build per commit; `./deploy.sh excellence` with a prebuilt release spends about a minute (copy + activation) after the build;
all Hetzner products and RTS run the byte-identical binaries of one build (same manifest). Tests: `deploy/common/test_release_manifest.py`,
`test_build_once.py`, `deploy/rootless-linux/test_prebuilt.py` (the stage-only copy test runs on Linux only).
