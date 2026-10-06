[← sandbox / confinement](index.md)

# PLAT-266 — Mount-namespace Landlock fallback treated every guarded path as a directory

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | sandbox |
| Area | confinement |
| Summary | sandbox/filesystem capability, severity high — unconditional |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `fixed, live-verified on the Dominion deployment` |
| Last synchronized | `2026-08-31` |

- **Priority:** sandbox/filesystem capability, severity high — unconditional
  `execute_shell_command` failure for any scope containing a plain file, on
  any deployment where the mount-namespace fallback is active.
- **Origin:** a scheduled workflow step on the Dominion Hetzner deployment
  (`Workflow/tectonicusadaytrading`, step `eval-signal-freshness`) reported,
  after four independent retries: "Every `execute_shell_command` call fails
  identically before my command even executes — the sandbox's mount setup is
  trying to bind-mount `schedule-runs.json` as a directory when it's a file,
  and that mount failure (`mount(2) system call failed: Not a directory`)
  aborts the call regardless of content." The agent's own diagnosis was
  correct; initial triage in this conversation briefly dismissed it as a
  reference to an earlier, already-fixed incident before the actual
  AUTO-NOTIFICATION evidence was located.

## Problem

`generateMountScript` (`workspace/security/isolator.go`) — the mount-namespace
fallback used whenever Landlock itself is unavailable or rejects a policy
shape it cannot express (see PLAT-261's own two folder-guard bugs and the
earlier `--user --map-root-user` fix on this same deployment) — assumed every
`ReadPaths`/`WritePaths`/`BlockedWritePaths` entry was a directory:
`mkdir -p "$absPath"` unconditionally creates a directory node (`absPath`
never pre-exists, since the tmpfs overlay already hides the whole `BaseDir`),
and bind-mounting a *file* source onto that freshly created *directory*
target fails at the kernel level with exactly `mount(2) system call failed:
Not a directory`. Combined with `set -e`, this aborted the whole script —
every `execute_shell_command` call failed unconditionally, regardless of the
command actually requested, whenever any configured path in scope happened to
be a plain file. The primary Landlock backend (`landlock_runner_linux.go`)
already stats each path and correctly masks directory-only rights for files;
only this fallback had the gap, and it is general — not specific to
`schedule-runs.json`, which was simply the first file-shaped path this
workflow's scope happened to include.

## Resolution

A shared `writeCreatePlaceholder` helper now branches on directory vs. file
(touching an empty file when the source isn't a directory), and placeholder
creation for every configured path now happens in one consolidated pass
strictly before any bind-mount runs — fixing a second, narrower ordering bug
found while testing: a file's own placeholder failed to write ("Read-only
file system") once its parent directory had already been bind-mounted
read-only by an earlier, unrelated entry in the same script. `writeBindMountOnly`
then does only the mount, assuming the placeholder already exists.

## Verification

- New live end-to-end test, `TestMountNamespaceFallbackHandlesFileReadPath`
  (`workspace/security/isolator_linux_test.go`), calls
  `executeIsolatedMountNamespace` directly (bypassing Landlock-first
  selection, since Landlock's own path handling was never buggy) and asserts
  a file-in-`ReadPaths` scope round-trips its real content through the
  fallback.
- Extensively verified live on the deployment host across many runs,
  standalone and interleaved with the sibling `TestMountNamespaceFallbackEnforcesLandlockRejectedOverlapPolicy`
  test, after a lengthy false trail: an apparently deterministic failure
  traced all the way to a bug in the *test's own* command invocation
  (double-wrapping `sh -c` through `generateMountScript`'s plain-space
  argument joining), not the fix itself — manual `unshare` reproductions
  outside the Go test harness kept succeeding while the harness kept
  failing, which is what eventually isolated the invocation bug rather than
  the production code.
- Portable `security` package suite passes unchanged; the two failures seen
  in an ad-hoc test-binary deployment on the server
  (`TestIsolatorOSDetection`, `TestEnvironmentIsolation`) are pre-existing —
  caused by that deployment not including the `video-studio-landlock-runner`
  binary alongside the test binary, unrelated to this change.
- Deployed to `trader.tectonicmarkets.com` (`dominion-workspace` rebuilt and
  restarted); `shell_sandbox.backend=landlock` confirmed healthy post-deploy.
