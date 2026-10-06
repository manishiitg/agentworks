[← sandbox / confinement](index.md)

# PLAT-283 — A sandboxed step could never install a Python/Node package, on any Linux deployment

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | sandbox |
| Area | confinement |
| Summary | P1 — a hard capability wall, not a bug in one workflow: any |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Sonnet 5 |
| Ticket state | `fixed`, live-verified on the Dominion deployment |
| Last synchronized | `2026-09-05` |

- **Priority:** P1 — a hard capability wall, not a bug in one workflow: any
  `execute_shell_command` step on any Linux deployment that needed a package
  not already in the run image had no way to get it, full stop.
- **Owner:** `workspace/security/environment.go` (`buildNativeEnvironment`),
  `workspace/security/isolator_linux.go` (`landlockCommand`,
  `packageManagerCacheEnv`, `writableCacheBase`).
- **Related:** PLAT-281 (the same shape of bug — a real capability gap that
  looked like a fundamental sandbox limitation but was actually one missing
  grant — for `exec`, not filesystem cache writes) and PLAT-118 (Landlock
  fail-closed design generally).

## What was found

The Dominion trading workflow's agent reported, and had "tested and
documented" back on 2026-08-31, that installing a Python package inside its
sandboxed shell was not viable at all: `pip3 install yfinance` was refused
because Python is externally-managed (PEP 668); forcing it past that with
`--break-system-packages` then hit a raw permission error; and even
`python3 -m venv` failed the same way. The conclusion on file was that only
what ships in the run image (standard library + a few extras) is usable —
a permanent architectural limit.

That conclusion was wrong, and provably so: Landlock as configured in this
codebase (`landlock_runner_linux.go`) restricts filesystem access only —
there is no `LANDLOCK_ACCESS_NET_*` rule anywhere, and Dominion (like every
other Linux deployment here) has no egress firewall either
(`deploy/dedicated-vm/dominion-hetzner.md` confirms no firewall rules are
part of the deploy). PyPI/npm are reachable. The actual failure was two
separate, both-fixable gaps stacked on top of each other:

1. Native-mode shells (`NATIVE_WORKSPACE=true`, the standard mode per this
   session's STT-engine and Landlock work) never set
   `PIP_BREAK_SYSTEM_PACKAGES=1` — only the Docker environment builder did.
   That's exactly the "externally-managed-environment" refusal.
2. pip, npm, and `venv` all default to caching under `$HOME`
   (`~/.cache/pip`, `~/.local`, a `$TMPDIR`-based build scratch directory).
   A step's Landlock write grant is scoped to its own workspace folder, not
   the real `$HOME` — on Dominion, `HOME=/srv/dominion/home`, entirely
   outside every step's grant. `--break-system-packages` genuinely bypasses
   the PEP 668 check and then hits Landlock's real, correct denial the
   first time pip (or venv) tries to write its cache. That denial looked
   identical to "installs don't work here" from the agent's vantage point,
   but it's a routing gap, not a wall.
3. Found only by running a *real* `pip install` against the shipped
   launcher after gaps 1–2 were fixed (the synthetic cache-write probe
   passed while the real thing still failed): the Landlock read baseline
   granted an enumerated dozen `/etc` entries (ssl, resolv.conf, passwd,
   ld.so.*, fonts, ...), and pip reads two more — `/etc/debian_version`
   (its vendored `distro` module, building the HTTP User-Agent before any
   network call; it catches `FileNotFoundError` but Landlock denies with
   `EACCES`, so the `PermissionError` is fatal) and then
   `/etc/mime.types`. Granting just the distro files moved the failure to
   `mime.types`; granting all of `/etc` read-only made the full install
   succeed end-to-end. Enumerating files is whack-a-mole; every future tool
   that reads one more config file would be its own ticket. One subtlety,
   also found live: the existing explicit entries must stay *alongside*
   the `/etc` grant, not be replaced by it — a Landlock rule on `/etc`
   covers what lives under it, not what a symlink there points at. On
   systemd-resolved hosts like Dominion, `/etc/resolv.conf` →
   `/run/systemd/resolve/stub-resolv.conf`, and a build that granted
   `/etc` alone failed every DNS lookup in the sandbox ("Temporary failure
   in name resolution"); `existingCanonicalPaths` resolving each explicit
   entry individually is what grants the target.

## What shipped

- `buildNativeEnvironment` (`environment.go`) now also sets
  `PIP_BREAK_SYSTEM_PACKAGES=1`, matching what `buildDockerEnvironment`
  already did — closing gap 1 for every native-mode deployment, not just
  Dominion.
- `landlockCommand` (`isolator_linux.go`) now appends
  `PIP_CACHE_DIR`, `XDG_CACHE_HOME`, `PYTHONUSERBASE`, `npm_config_cache`,
  and `TMPDIR`, all redirected into a `.cache`/`.local`/`.tmp` subtree of a
  directory the step can actually write to — closing gap 2. The base
  directory is chosen defensively: the step's own `WorkDir` when it falls
  inside a granted write path (the normal case), otherwise the first
  granted write path, otherwise no redirection at all (so a step with no
  write grant fails exactly as it did before, rather than pointing pip at
  somewhere Landlock will also deny). This applies to every Linux
  deployment using this Landlock launcher, not a Dominion-specific
  env-var carve-out — the fix the user explicitly asked to have "generic
  for linux" rather than a one-off patch.
- `landlockSystemReadPaths` (`landlock_runner_linux.go`, i.e. the
  `video-studio-landlock-runner` binary itself) now grants all of `/etc`
  read-only in addition to the enumerated entries (which must stay — see
  the `resolv.conf` symlink note above; a regression test pins the
  resolved target) — closing gap 3. Why this
  is safe, and the one rule it imposes: it is read-only (the write
  baseline is untouched); DAC still applies, so it grants nothing the
  service user cannot already read outside the sandbox — verified on
  Dominion with `find /etc -type f -readable ! -perm -o=r` returning
  nothing, i.e. no `/etc` file is readable by the service user without
  already being world-readable; and the baseline's "keep these narrow"
  rationale is about `/proc` (other processes' environments and
  credentials), which stays narrow. Because Landlock rules are additive, a
  deployment cannot carve a secret back out of this grant, so
  service-readable secrets must stay out of `/etc` — Dominion keeps them
  in `/srv/dominion/.env`, which is where they belong anyway.

## Verification

- 5 new tests: `TestNativeEnvironmentAllowsPipInstallOnExternallyManagedPython`
  (`environment_test.go`); `TestPackageManagerCacheEnvPointsIntoTheGrantedWritePath`,
  `TestPackageManagerCacheEnvFallsBackWhenWorkDirIsNotWritable`,
  `TestPackageManagerCacheEnvNoOpsWithoutAnyWritePath`, and
  `TestLandlockCommandCanInstallIntoItsOwnCache` (`isolator_linux_test.go`).
- Cross-compiled for Linux and run live on the Dominion box against the
  real, deployed `video-studio-landlock-runner` — all 5 new tests pass, and
  `TestLandlockCommandCanInstallIntoItsOwnCache` reproduces the exact
  incident shape (write `$PIP_CACHE_DIR/probe.whl` inside a Landlock
  sandbox with only the step's own workspace folder granted) and confirms
  the write now lands inside the granted path.
- Full `workspace/security` suite run live on Dominion, both before and
  after this change: one pre-existing failure
  (`TestLandlockEnforcesExternalFolderAccess`) reproduces identically
  against a clean pre-change binary built from the same `origin/main` tip,
  confirming it predates this work and is not a regression from it.
  Everything else passes unchanged.
- Real installs run live inside the shipped `video-studio-landlock-runner`
  on Dominion, with `HOME` left at the unwritable `/srv/dominion/home` and
  only a scratch directory granted for writes (the exact shape of a
  workflow step): with all of `/etc` readable, `python3 -m pip install
  --user yfinance` completed end-to-end — PyPI over HTTPS, numpy/pandas/
  lxml/curl_cffi and the rest downloaded through the routed cache,
  installed into the routed user-site, `pip show` reporting yfinance
  1.7.0. With only the three distro-id files added instead, the same
  install got past `debian_version` and failed on `mime.types`, which is
  what settled enumerate-vs-grant-`/etc`. The Debian side is fully
  provisioned (`python3-venv`, `python3-pip`, and the bundled pip/
  setuptools wheels are installed), and the same `venv` creation succeeds
  outside the sandbox, so every failure here was Landlock, not the image.
- Verification gotcha worth knowing for the next person: the launcher
  consumes its `--config` policy file (it is gone after one run), so a
  hand-staged policy is single-use — stage one copy per invocation, or the
  second run fails with `SANDBOX_UNAVAILABLE: read Landlock policy` before
  doing anything.
- Still pending after this ticket: the first real trading-workflow step
  that needs a package outside the run image is the production reverify.
