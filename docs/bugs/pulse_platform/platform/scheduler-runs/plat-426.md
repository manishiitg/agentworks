[← platform / scheduler-runs](index.md)

# PLAT-426 — Build a release once and deploy it everywhere (deploys take 5–7 minutes per product because each server builds the same source)

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P3 |
| Product | platform |
| Area | scheduler-runs |
| Summary | fixed on `main`; not deployed. |

| Coordination | Value |
|---|---|
| State | fixed on `main` (2026-10-04); GitHub publish/download added 2026-10-04, on `main`, not deployed; publishing from the box starts when the owner places the token (below) |
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
   had; about 180 MB compressed; first streamed build host -> this machine -> RTS, now downloaded by RTS from GitHub, see below, with the stream as fallback) with the manifest hash read from the build host. The RTS
   `build-and-activate.sh --prebuilt` verifies it, then only the compile step differs: Secrets Manager `.env` merge, `systemd-run`
   limits, units reinstalled every deploy, preflight, hyperframes browser, CloudFront usage print are unchanged. RTS can safely use the
   prebuilt build: same x86_64, Ubuntu 24.04, glibc 2.39, and the agent's shared libraries (libstdc++, libgcc_s, libc, libm) are plain Ubuntu
   packages plus the shipped `bin/lib`; the verification refuses it on any mismatch and `ldd` on the agent must resolve.

## Builds on GitHub: servers download them (owner's decision, 2026-10-04)

Streaming the build box -> owner's Mac -> RTS took 5+ minutes. The box now publishes each build to the PUBLIC repo
`github.com/manishiitg/agentworks-builds` (releases only, no source) and RTS downloads it itself, with no credential.

- `deploy/common/publish-build.sh <build-dir>` (python3 + tar + gzip only; called best effort at the end of `build-release.sh`, and by `./deploy.sh publish [build]`)
  creates the pre-release `build-<builder8>-<mcpagent8>-<provider8>` with `build.tar.gz` (the whole folder), `build-rts.tar.gz` (without
  `source/mcpagent`, `source/multi-llm-provider-go`, `downloads/`), `manifest.json`, `SHA256SUMS` (uploaded last); the body holds the three revisions
  and `manifest-sha256`. Idempotent; a half-uploaded or stale release (different manifest hash, e.g. after `--force`) is repaired by re-uploading its
  assets; keeps the newest 8 build releases (`BUILDS_KEEP_RELEASES`) and deletes older releases and their tags; never `latest`. Before the first upload it creates and deletes a draft release
  to prove the token really can write to that one repo (a clear message if not); it never writes to any other repository. `--list` (anonymous) backs `./deploy.sh builds`.
- Token: `GH_TOKEN` in the environment or `GH_TOKEN=...` in `~/.config/agentworks/builds.env` (root on the box); a file readable by group/world, or owned by another user, is refused. With no token the step prints one
  message and exits 0: a build or deploy never fails because of it.
- `deploy/common/fetch-build.sh <tag> <asset> <manifest-sha256> <dest>` runs on the target: curl with resume, retries and timeouts, extracts into a scratch folder next to `dest`,
  refuses unless `manifest.json` hashes to the value `deploy.sh` read on the build host, then moves it to `dest`. Any failure leaves nothing behind. The activation's manifest verify is unchanged.
- `./deploy.sh rts`: publish if needed, then RTS downloads `build-rts.tar.gz`. `DEPLOY_BUILD_TRANSPORT=auto` (default) falls back to the old stream with a one-line reason
  when the release is missing or the download fails verification; `github` never falls back; `stream` is the old path only. Hetzner products still copy from `/srv/_builds`.
- Tests: `test_publish_build.py` (fake GitHub API in `fake_github.py`: create, upload, idempotent rerun, half upload, stale manifest, prune to 8, loose token file, no token, write preflight, anonymous
  fetch, hash mismatch, tampered archive, truncated and resumed download), `test_deploy_transport.py` (github vs stream vs fallback, `publish`, `builds`). Run on the Hetzner box as `agents`: 22 pass.
- Real check from the box (2026-10-04, owner's scoped token, tiny fake build `0badc0de-...`, not a real build): release created with 4 assets, rerun left it alone, anonymous API and
  `fetch-build.sh` as an unprivileged user with an empty environment downloaded and verified it, a wrong hash was refused with nothing left, `releases/latest` stayed 404; the test release and tag
  were deleted (the repo holds only its README).

### Owner setup for the token (one time)

1. GitHub > Settings > Developer settings > Fine-grained personal access tokens > Generate new token. Resource owner `manishiitg`; repository access: only `agentworks-builds`; permission Contents: Read and write; expiry 90 days.
2. On the box as root: `install -d -m 700 /root/.config/agentworks`, then a file `/root/.config/agentworks/builds.env` with the single line `GH_TOKEN=<token>`, `chmod 600` (create it with `umask 077`, do not paste the token into a shell history line you keep).
3. Check: `./deploy.sh publish` (uploads the newest build; prints `Published: <build> -> <tag>`).
4. Rotate before the 90 days end: create a new token the same way, overwrite the file, delete the old token on GitHub. Expired or wrong tokens show as `HTTP 401` or the write-check message; deploys then fall back to the stream.

### Not verified / left

- A real upload of a full-size build (about 500 MB uncompressed, a few hundred MB compressed per asset) and RTS downloading it itself: RTS was not touched. The first `./deploy.sh rts` after the token exists should be watched.
- Publishing adds the upload time (box to GitHub) to every fresh build, also for Hetzner-only deploys; `BUILD_PUBLISH=0` skips it.

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

## Build a pinned commit (2026-10-04)

`DEPLOY_SHA_MCP_AGENT_BUILDER_GO`, `DEPLOY_SHA_MCPAGENT` and `DEPLOY_SHA_MULTI_LLM_PROVIDER_GO` (40-hex) make `./deploy.sh` build that commit instead of main's head, so a
release can leave out work still landing on main (PLAT-435 was mid-migration when PLAT-437 had to go out). The commit must be an ancestor of `origin/main`.

## Register notes

[PLAT-426](plat-426.md), P3, fixed on `main`; not deployed. `deploy.sh` builds once on the Hetzner
box (`/srv/_builds`, manifest with revisions, arch, glibc, file hashes; about 3 min warm) and Excellence, Confida, SparkQuill
and RTS only copy and activate it after verifying the manifest; `--build` deploys a chosen older build.
