[← platform / security-sandbox](index.md)

# PLAT-281 — Landlock had no grant for a deployment's own installed CLI tools, so every exec of one failed silently as "not signed in"

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | sandbox |
| Area | security |
| Summary | P1 — live, user-reported, financial-adjacent. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Sonnet 5 |
| Ticket state | `fixed, live-verified on the Dominion deployment` |
| Last synchronized | `2026-09-04` |

- **Priority:** P1 — live, user-reported, financial-adjacent. Dominion's
  `tectonicusadaytrading` workflow had placed zero paper trades since
  2026-08-31; every scheduled run's `place-paper-trades` step failed
  launching the `alpaca` CLI, silently, with no trade submitted and no
  error surfaced anywhere the user would see it.
- **Owner:** `workspace/security/landlock_runner_linux.go`
  (`landlockSystemReadPaths`), `workspace/security/landlock_runner_linux_test.go`.
- **Related:** [PLAT-266](plat-266.md) — a different Landlock defect (the
  mount-namespace fallback's directory-vs-file assumption), also first
  found live on Dominion via a scheduled step's own self-diagnosis, same
  discovery pattern as this ticket. [PLAT-267](plat-267.md) — likewise a
  Dominion-only defect invisible without a live deployment to reproduce
  against.

## What was found

The user reported the trading workflow's own report: *"Your paper trading
account has no saved login, so no trades have been placed since 27
August."* That framing pointed at a missing OAuth session, and the first
fix (copying the working local `alpaca profile login` session to the
server) looked complete — `alpaca doctor` and `alpaca account get`, run
over a plain SSH session, both succeeded immediately.

The trading workflow's own agent then reproduced the *actual* failure
directly and reported it precisely: running the same command from inside a
real workflow step gave `rc=126: Permission denied` trying to launch the
`alpaca` binary at all — not a login error. Every recorded occurrence back
to 2026-08-31 (when the CLI was first installed on the box) shows the
identical `rc=126`, regardless of whether a working login was present.

Root cause, confirmed by reading `RunLandlockLauncher` and
`landlockSystemReadPaths`: every `execute_shell_command` call that runs
through a workflow step is Landlock-sandboxed, and Landlock is
allow-list-only by kernel design — once `LANDLOCK_RESTRICT_SELF` is called,
anything not in an explicitly granted path is denied outright, with no
"default allow" mode. The sandbox's baseline grant
(`landlockSystemReadPaths`) is a fixed list of standard system directories
(`/bin`, `/usr`, `/lib`, `/lib64`, a handful of `/etc`/`/proc`/`/dev`
entries) plus, dynamically, the configured browser binary's directory. It
had no entry for a deployment's own custom tool directory — Dominion
installs `alpaca` (and `claude`, `surge`) under `/srv/dominion/tools/bin`,
already on every service's `PATH` via its own systemd unit, but outside
every Landlock-granted path. The binary was always *found* (PATH
resolution happens before exec) and always denied *execution*.

A second, related gap surfaced once the first was fixed: with execute
rights granted, the CLI could launch but still reported "authentication
required" — its config lookup (`$HOME/.config/alpaca`, hardcoded, no
override flag) also fell outside every granted path. This layered on top
of a separate, already-diagnosed detail from earlier the same day: the
sandboxed `dominion-workspace` service's own `HOME` (`/srv/dominion/home`,
set via its systemd unit) differs from a plain SSH session's `HOME`
(`/srv/dominion`), which is why the first login copy — verified only over
SSH — looked complete but wasn't visible to a real step. `HOME` itself
resolves correctly into a landlocked step (native-mode
`buildNativeEnvironment` inherits the process environment unchanged), but
env var resolution and Landlock's file-access grants are entirely
independent mechanisms; a correct `HOME` does not make the path it points
at readable.

**Why this was never seen locally:** Landlock is Linux-only
(`landlock_runner_linux.go` is `//go:build linux`) and doesn't exist on
macOS at all. The macOS sandbox path in this codebase
(`isolator.go`/Seatbelt `sandbox-exec`) is structurally the opposite of
Landlock for a normal (non-`StrictAllowlist`) workflow step: its default
profile is `(allow default)` with specific paths *denied*, so an
unlisted directory like Homebrew's `/opt/homebrew/bin` is reachable by
default. Landlock, as used here, has no equivalent permissive default — an
unlisted path is always denied. Identical local/server setups therefore
behave oppositely for a tool installed outside the codebase's
own baseline.

## What shipped

`SANDBOX_EXTRA_SYSTEM_PATHS`, a colon-separated environment variable
`landlockSystemReadPaths()` reads and appends to its fixed baseline —
same pattern as the existing single-path `AGENT_BROWSER_EXECUTABLE_PATH`
case, generalized to a list. A deployment grants its own tool/config paths
without a code change per deployment. Dominion's `/srv/dominion/.env` (read
by both `dominion-workspace` and `dominion-agent` via `EnvironmentFile=`)
now sets:

```
SANDBOX_EXTRA_SYSTEM_PATHS=/srv/dominion/tools:/srv/dominion/home/.config
```

covering both the tool binaries and the credential directory their configs
live under.

## Verification

Linux-kernel-specific code (`//go:build linux`) that cannot run on macOS,
so every check below ran on the actual target OS rather than being
cross-compiled and assumed:

- `GOOS=linux GOARCH=amd64 go vet` and `go test -c` both clean.
- 4 new tests (`landlock_runner_linux_test.go`) — extra-path inclusion,
  multiple colon-separated paths, a missing extra path silently dropped
  (matching existing baseline-entry behavior), and the unset case leaving
  the baseline unchanged — compiled and **run directly on the Dominion
  box**, all passing, alongside every previously-passing test in the
  `security` package (two pre-existing, unrelated failures in that same
  run are a `video-studio-landlock-runner` path-lookup quirk of invoking
  the test binary standalone outside its normal deployment layout, not a
  regression from this change).
- **Full end-to-end reproduction against the real shipped binary**, not
  just unit tests: invoked `video-studio-landlock-runner` directly with a
  policy scoped like a genuine workflow step (`read_paths:
  ["/srv/dominion/data/docs"]`, deliberately *not* including
  `/srv/dominion/tools` or the config directory through the normal
  per-workflow grant) and the same environment a real service would
  provide. Before the fix: `rc=126`. After: `alpaca account get` returned
  the real paper account (`PA3GN6F2QG54`, `ACTIVE`, equity `$96,911.32`)
  end to end through the actual sandbox.

**Not yet done:** live reverify via the next scheduled `place-paper-trades`
run (cron-driven; next occurrence proves it against the real workflow, not
a reproduction). Worth checking separately whether `claude` or `surge` —
the other two tools in the same `/srv/dominion/tools/bin` directory — were
ever invoked from inside a landlocked step and hit the same wall; not
confirmed either way, since no evidence surfaced either was called through
that path rather than by the trusted top-level agent process directly.
