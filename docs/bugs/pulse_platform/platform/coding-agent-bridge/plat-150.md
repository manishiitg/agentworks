[← platform / coding-agent-bridge](index.md)

# PLAT-150 — live-attach killed its tmux control client without reaping it, leaking a zombie PID and a goroutine per attach

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | P2 — nothing fails and no user-visible symptom appears, which is |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` — root cause confirmed from a live production goroutine dump; one-line reap added with the reasoning recorded inline |
| Last synchronized | `2026-08-19` |

- **Priority:** P2 — nothing fails and no user-visible symptom appears, which is
  precisely why it survived. It accumulates one unreapable process and one
  permanently-blocked goroutine per live-attach, forever, until the server
  restarts. A restart "fixes" it, which actively hides it.
- **Owner:** `agent_go/cmd/server/terminal_live_attach.go`
  (`liveAttachStream.runControlMode`)

## Symptom

Measured on the running server, 2026-08-19:

```
$ ps -eo pid,ppid,stat,lstart | awk '$3 ~ /^Z/'
39124 37110 Z  Wed Aug 19 10:40:47 2026  <defunct>
40325 37110 Z  Wed Aug 19 10:47:00 2026  <defunct>
43418 37110 Z  Wed Aug 19 10:54:36 2026  <defunct>
47766 37110 Z  Wed Aug 19 11:11:23 2026  <defunct>
50928 37110 Z  Wed Aug 19 11:20:07 2026  <defunct>
```

PID 37110 is the AgentWorks server itself
(`exe/main server --port 18743`). Five unreaped children, and the log shows
live-attach coming up at `10:35:08` — five minutes before the first one.

## Root cause

`runControlMode` starts the tmux control client with
`pty.StartWithSize(cmd, ...)` (an `exec.CommandContext`), and its only cleanup
was:

```go
go func() {
    <-ctx.Done()
    _ = ptmx.Close()
    if cmd.Process != nil {
        _ = cmd.Process.Kill()
    }
}()
```

**`Kill()` terminates a process; it does not reap it.** The child stays
`<defunct>` until its parent calls `Wait`. Because this is an
`exec.CommandContext`, os/exec additionally starts a `watchCtx` goroutine whose
send is only ever drained by `Wait` — so an unreaped attach leaked *both* a PID
and a goroutine, permanently.

The confirming correlation, from `GET /debug/pprof/goroutine?debug=2` on the
live server: **five zombies against exactly five `watchCtx` goroutines parked
on `chan send`**, four of whose `Start()`-calling goroutines had already
exited without ever reaching `Wait`.

## Fix

A `defer` at the point of successful start that closes the PTY, kills, and —
the actual fix — calls `cmd.Wait()`.

Deferred rather than folded into the existing `ctx.Done` goroutine because
`runControlMode` also returns on its own (tmux `%exit`, or a scanner error)
while the context is still live; that path has to reap too, and previously
did not even tear the PTY down — it waited for a cancel that might never come.
`Close`/`Kill` are safe to call twice, so both paths converge on one reap.

## Why this was hard to find

Recorded because the next occurrence of this class will hide the same way:

- **A leaked process is anonymous.** `<defunct>` has no name, no command line.
  `ps aux | grep <anything>` finds nothing. It is only visible by filtering on
  process *state* and then walking up to the parent PID.
- **The obvious heuristic reports clean.** Comparing `Start()` vs `Wait()`
  counts per file across `agent_go`, `mcpagent`, and `multi-llm-provider-go`
  returned nothing here. The cleanup code *looks* right — there is a goroutine
  doing `Close` and `Kill`. The bug is a missing POSIX step that reads as a
  no-op line.
- **Every other call site was fine**, which reinforced the false clean signal:
  this is the only `pty.Start` in all three repos, and every other
  `exec.CommandContext` uses `Run`/`Output`/`CombinedOutput`, all of which reap
  internally.
- **A louder unrelated bug masked it.** A workflow was simultaneously wedged in
  `syscall.Wait4` for 64 minutes ([PLAT-139](plat-139.md)'s class). Both involve
  `cmd.Wait()` and process lifecycle, so the natural read is one cause. They are
  unrelated.
- **Two plausible theories had to be eliminated first**, both by reading source
  rather than guessing: MCP client reconnect (`mcpclient/client.go`'s
  `connectOnce` already closes the previous client explicitly "to prevent
  subprocess leaks"), and mcpagent's OAuth browser launch (a real
  `Start()`-without-`Wait()`, but `exec.Command`, so no `watchCtx` goroutine —
  which is what ruled it out against the observed signature).
- **It never fails.** No error, no failed request, no degraded response. It just
  accumulates until restart.

The `watchCtx`-blocked-on-`chan send` signature is the transferable lesson: it
is a precise fingerprint for "a `CommandContext` child was started and never
waited on," and pairing its count against the zombie count is what turned a
plausible story into a confirmed one.

## Guard against the next one

The fix is one line; the class is what matters, because "start a process, forget
to reap it" is invisible until somebody takes a goroutine dump.
`TestEveryStartedExecCommandIsReaped`
(`agent_go/cmd/server/process_reap_guard_test.go`) walks the AST of every
non-test `.go` file under `agent_go` and fails when a variable assigned from
`exec.Command`/`exec.CommandContext` is started but never waited on in the same
function (nested closures included, since cleanup usually lives there).

It is AST-based rather than textual on purpose: the obvious heuristic —
comparing per-file counts of `.Start()` against `.Wait()` — was actually run
during this investigation and reported this very file **clean**, because counts
say nothing about *which object* was started. The guard tracks specific
variables instead, and treats `pty.Start`/`pty.StartWithSize` as starting their
first argument, which is exactly the shape that hid here (the process is started
by a call that is not a method on the command).

Verified fail-before/pass-after against the real bug: with the fix reverted it
reports `cmd/server/terminal_live_attach.go:runControlMode (cmd)` and fails;
with the fix it passes. In scope at time of writing: 35 `exec.Command*` sites
and 2 `pty.Start*` sites, so it is scanning meaningfully rather than passing
vacuously.

`Run`/`Output`/`CombinedOutput` reap internally and are correctly ignored.

## Verification

- `go build ./...` clean; `TestEveryStartedExecCommandIsReaped` and the
  register-integrity test both pass.
- Fail-before/pass-after confirmed for the guard (above).
- **Not verified live:** confirming zero zombie accumulation requires a server
  restart plus repeated live-attach sessions over time, and the running stack
  had active workflows. The mechanism is unambiguous from the dump, but the
  absence-of-regrowth check is still outstanding.

## Acceptance

- [x] Every `runControlMode` exit path reaps its child.
- [x] A regression guard fails on this class rather than relying on review.
- [ ] After a restart, repeated live-attach sessions leave no `<defunct>`
      children of the server process and no growth in `watchCtx` goroutines
      parked on `chan send`.
