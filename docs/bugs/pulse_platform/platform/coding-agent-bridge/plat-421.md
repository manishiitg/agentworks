[← platform / coding-agent-bridge](index.md)

# PLAT-421 — Every confined Muse launch ended at once: the sweep prelude could not find `muse`

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | fixed on `main`; deploy to Excellence and RTS in progress. |

| Coordination | Value |
|---|---|
| State | fixed on `main` (provider `a3ecd73`, pinned here); deployed to Excellence and RTS once the deploy below is done |
| Severity | P1 (Muse unusable in Code on Excellence after the deploy that carried PLAT-417) |
| Date | 2026-10-04 |
| Owner | coding-agent-bridge |
| Related | PLAT-417 (Muse fixes that introduced the prelude) |

## Problem

After the Excellence deploy of 2026-10-04 (`agents-c584adde`) every Muse message in Code failed with
`muse tmux session "mlp-muse-product-..." died while waiting for muse TUI to settle`, about 185 ms after the start. Muse itself is unchanged (1.4.2, installed 2026-10-01) and runs
fine inside the slot's sandbox.

## Cause

A trace of the live launch (execve only) showed the chain `bash -ilc` → `sudo -n -u slot03 slotctl exec` → `video-studio-landlock-runner` → `env GIT_CONFIG_... sh -c <sweep script> muse-sweep <tmp> muse --trust-workspace ...`.
The sweep's `exec "$@"` looked for a bare `muse` in sudo's secure PATH (`/usr/local/sbin ... /bin:/snap/bin`, not `/srv/agents/tools/bin`), found nothing, and the tmux pane command exited, so the session never existed.
A second effect: the Landlock launcher grants the CLI's install folder by looking at the first real argument, which was now `sh`.
The failure message ("died while waiting for the TUI") hid a plain command-not-found.

## Fix

- `musePreludeArgv` passes the path this process resolves for `muse` (the service's PATH has the install folder); unchanged when it cannot be found.
- `clisandbox.executableDirs` looks past a leading `sh -c <script> <name> <arg>` prelude.
Tests: `musecli_prelude_path_test.go` (PATH without the muse folder), `clisandbox/executable_dirs_test.go`.

## Left

- Verify on Excellence and RTS after the deploy: a Muse message in Code answers; New chat then an immediate message works.
- The error text still says "died while waiting for muse TUI to settle" for any launch failure; including the first line of `last-launch.stderr` would have named the cause at once.
- A temporary forwarding script in `/usr/local/bin/muse` was considered and not installed.

## Register notes

[PLAT-421](plat-421.md), P1, fixed on `main`; deploy to Excellence and RTS in progress.
