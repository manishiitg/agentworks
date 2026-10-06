[← platform / coding-agent-bridge](index.md)

# PLAT-139 — a workflow step held its caller for 65 minutes after its real work finished; root cause confirmed via a second live occurrence

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | coding-agents |
| Area | bridge |
| Summary | P1 — a genuinely successful run reports as stuck/never-completing |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed` — but the ORIGINAL diagnosis in this ticket was wrong and is corrected inline (see the ⚠️ CORRECTION block). The stderr-pipe defect it found is real and its fix (`multi-llm-provider-go@6d8e4e9`) is retained, but it did not explain this incident: pi's process was NOT gone, it was alive and failing to exit. Real cause and fix: the deleted `agent_settled` teardown, restored in `multi-llm-provider-go@3d9bcc6` |
| Last synchronized | `2026-08-19` |

- **Priority:** P1 — a genuinely successful run reports as stuck/never-completing
  and holds its caller indefinitely. Unattended paths (Pulse, schedules,
  workflow steps) have nobody watching to notice, and the run's own
  `run_metadata.json` can be left frozen at `"status": "running"`.
- **Owner:** unassigned — the layer is not yet identified. Candidate layers in
  "Where to look next" below.
- **Related:** [PLAT-116](plat-116.md) (same *shape* of symptom on the tmux
  path; its Pi structured section documents this incident's disproven first
  diagnosis in full)

## The incident

2026-08-18, workflow `ICICI-BANK-PARSING-v2`, group `manishiitg`, step
`statement-download`, execution `workflow-full-msy44vw303`.

- The step's **real work completed at 09:45** — statements downloaded,
  `logout_verified: true` recorded in its own output.
- The caller was then **held for ~65 minutes**, until the stack was stopped by
  hand. The scheduler sat polling
  `query_step(execution_id: workflow-full-msy44vw303)` for a completion that
  never arrived.
- Because the manual shutdown killed the workspace server before the
  orchestrator flushed terminal status, that write was swallowed and
  `run_metadata.json` is **still frozen at `"status": "running"`** — the run
  reads as live rather than failed. (That swallowed-terminal-write behaviour is
  its own defect, noted in PLAT-116 and not owned here.)

## What this is NOT — ruled out, with evidence

Recording these so the same ground is not re-tread. Each was believed and, in
two cases, shipped against.

### 1. NOT "pi never emits `agent_settled`"

The original diagnosis was that pi 0.84.2 stopped emitting `agent_settled`, so
the structured adapter's only teardown trigger was dead code. The supporting
measurement — *"0 occurrences of `agent_settled` against 57 `agent_end` across a
day of production logs"* — was a **logging artifact created by the measurement
itself**: the adapter *handled* `agent_settled` in a `case` arm that logs
nothing, while `agent_end` fell through to the `default:` arm whose only job is
`Debugf("pi: unhandled event type=%q")`. The grep compared a deliberately-silent
handled event against a deliberately-logged unhandled one.

Refuted by running the real CLI with a real key, a real stdio MCP server via
`.pi/mcp.json`, and the adapter's exact flags
(`--print --mode json --no-builtin-tools -e npm:pi-mcp-adapter --approve`):

```
   1 "type":"agent_end"
   1 "type":"agent_settled"     <-- emitted, and last
>>> pi EXITED on its own after ~5s      (no stale pi or MCP processes)
```

Re-verified twice more (2 consecutive runs, clean exit in 4s each, zero
leftover `pi` or MCP server processes).

`agent_settled` is also correct *by construction* in pi's source
(`dist/core/agent-session.js`): emitted from a `finally` block — unskippable on
success, error, or abort — exactly once per run, **after** the
`while (_handlePostAgentRun())` loop that drives multi-step tool use and
retries has drained. `agent_end` fires *inside* that loop, many times per run.

### 2. NOT fixable by treating `agent_end` as terminal

`multi-llm-provider-go@6609765` did this. It killed a healthy, still-working pi
process within hours — the 3s natural-exit grace expired mid-run:

```
11:56:27 [SHUTDOWN] natural-exit grace (3s) expired for pid 67763
11:56:27 [SHUTDOWN] SIGTERM attempt 1/3 to pgrp 67763
11:56:27 [SHUTDOWN] process 67763 exited after SIGTERM #1
```

surfacing as `pi run failed: exit status 143` (pi's print-mode SIGTERM handler
is `process.exit(143)`). Reverted in `@fd00585`.

### 3. NOT a child process inheriting and holding pi's stdout

A follow-up theory held that pi exits while a spawned child (the MCP bridge)
keeps stdout's write end open, so `<-scannerDone` never unblocks. A four-adapter
rework was built on this and **abandoned before commit**. The MCP SDK spawns
stdio servers with `stdio: ['pipe','pipe', stderr]` — the child gets its **own**
pipes and never inherits the parent's stdout. pi's own adapter additionally
passes `stderr: "ignore"` outside debug mode.

### 4. NOT a missing P0 certification

Also claimed and disproven. `RequiredP0CodingAgentCertificationIDs` early-returns
`nil` for `Transport != tmux`, which looked like "structured transport has zero
P0 coverage" — but **no provider is registered as `Transport: structured`**.
That field is the provider's *primary* transport; all four run tmux primary and
structured secondary, and structured behaviour is certified via capability flags
(`UsesPersistentSession`, `SupportsStructuredStreaming`). Verified by executing
the function:

```
provider=claude-code  transport=tmux  requiredP0=17  [structured_streaming structured_multi_turn]
provider=codex-cli    transport=tmux  requiredP0=17  [structured_streaming structured_multi_turn]
provider=cursor-cli   transport=tmux  requiredP0=16  [structured_streaming structured_multi_turn]
provider=pi-cli       transport=tmux  requiredP0=16  [structured_streaming structured_multi_turn]
```

Structured *completion* is covered specifically: each provider's
`CertStructuredMultiTurn` is a real, live-gated E2E (e.g.
`TestPiCLIStructuredTwoTurnResume`). A two-turn resume cannot pass unless turn
one completed and its process exited — so a genuine structured hang would fail
it. All four P0 enforcement tests pass.

## First root-cause attempt, confirmed live (2026-08-18) — real defect, WRONG incident

A second occurrence — `check-form-26as-xspaces`, `tax-retrieval-sequence` step,
session `c2cf7954-e686-4a18-818b-3730c56884cf` — reproduced the same shape
while the server was still running: real work had completed (form 26AS PDFs,
`tax_summary.json`, `downloads.json` all written 19:06–19:11 IST), the
session's own completion signal fired correctly at 19:43:26
(`[RETAINED_TURN] Settled retained main-agent turn from structured
unified_completion event ... state=completed`), and nothing progressed for
the next ~13 minutes with, it was believed, no live `pi` process anywhere on
the machine. **That last part was false** — see the CORRECTION below; the
search used could not match pi, which runs as `COMM=pi`.

Rather than theorize again, a **production goroutine dump** was pulled via
the already-registered pprof endpoint
(`GET /debug/pprof/goroutine?debug=2` — safe, read-only, does not touch the
running workflow) and found the exact blocked goroutine:

```
goroutine 73561 [syscall, 51 minutes]:
  ...
  os/exec.(*Cmd).Wait(...)
  picli.(*PiCLIAdapter).generateContentStructured(...)
      picli_structured_adapter.go:406   <- cmd.Wait() itself, NOT the earlier stdout wait
```

A second goroutine, blocked in the **identical `IO wait` shape for the
identical 51 minutes**, was `os/exec`'s own internal stderr-copy goroutine
(`writerDescriptor.func1`, an `io.Copy` off a pipe the adapter never had a
handle to). The code did:

```go
var stderr bytes.Buffer
cmd.Stderr = &stderr
```

That form makes `os/exec` create and own an **internal** pipe; `cmd.Wait()`
blocks until that pipe's copy goroutine also reaches EOF, not just until the
process is reaped. This morning's entire investigation (see PLAT-116's
"structured completion" section) only ever considered **stdout**, via
`cmd.StdoutPipe()` — stderr, going through the assign-a-`Buffer` form, carried
the exact same class of risk the whole time and was never examined, because it
never went through code anyone had reasoned about.

**⚠️ CORRECTION (2026-08-19): the sentence that used to sit here was wrong, and
it was load-bearing for this ticket's conclusion.** It read:

> *"pi's own process was confirmed gone (no PID via `ps`, no zombie/`<defunct>`
> entry) — so this was never 'pi is stuck.'"*

pi's process was **not** confirmed gone. That check was
`ps aux | grep -E "[n]ode.*pi"`, which cannot match: **pi runs as `COMM=pi`,
not as a node process.** The search was incapable of finding what it claimed
was absent, so "no PID" was an artifact of the query, not a fact about the
system.

Verified the correct way on 2026-08-19 — listing the server's actual children
(`ps -eo pid,ppid,stat,etime,comm | awk '$2==<server pid>'`) — during a live
recurrence:

```
  PID   PPID  STAT   ELAPSED  COMM
 4697  91384  S        27:49  pi     <- matched the "24m0s" stall warning
96976  91384  S      01:05:38  pi    <- matched the "1h1m30s" stall warning
```

Both alive, elapsed times matching the stall warnings almost exactly, **zero
zombies**. So `cmd.Wait()` was blocked in `syscall.Wait4` for the correct
reason: the child genuinely was still running. This was "pi is stuck" after
all — the exact reading this ticket ruled out.

The stderr-pipe defect described above is real and the fix for it is correct
and retained. What is wrong is the claim that it explained *this* incident.

The actual mechanism, established on 2026-08-19: pi finishes its work and then
fails to **exit**. Its print mode returns without `process.exit()` and relies on
Node's event loop draining, and pi's own source warns that extensions can keep
a one-shot command alive. The MCP extension spawns `mcpbridge`, whose live
process handle keeps that loop from draining, while `mcpbridge` sits waiting on
a stdin pi never closes (mcp-go's stdio server does exit on EOF, so it is
genuinely waiting, not broken). Neither side breaks the deadlock. Confirmed on
the live processes above: idle, each with a live `mcpbridge` child, and **no
open network sockets** — so not waiting on the model API either.

The mechanism that breaks it is the `agent_settled` teardown
(`GracefulAfterNaturalExit`, 3s grace), which had been deleted from the pi
adapter and is restored in `multi-llm-provider-go@3d9bcc6`. Killing a stuck pi
by hand has the same effect — `cmd.Wait()` unblocks and the held run completes
instead of hanging — which is what the teardown now does automatically.

Not pi-specific in mechanism: `claudecode` (`:366` on `result`), `codexcli`
(`:316` on `turn.completed`) and `cursorcli` (`:578` on `result`) all arm the
same teardown on their own terminal events and were untouched. Only pi's copy
had been removed, which is why only pi hung — across both
`check-form-26as-xspaces` and `ICICI-BANK-PARSING-v2` (171 and 81 stall
warnings respectively), one bug rather than two.

**Fixed** in `multi-llm-provider-go@6d8e4e9`: switched to `cmd.StderrPipe()`,
symmetric to stdout — the adapter now owns the pipe, and `os/exec` auto-closes
it the moment the tracked process itself exits, regardless of what else may
still hold the write end. `cmd.Wait()` deliberately still has no hard
timeout — this workflow's tools are allowed up to 90 minutes
(`TOOL_EXECUTION_TIMEOUT`), so an aggressive kill risks ending legitimate
work, and there is no way to tell "stuck" from "genuinely still working"
without a bounded backstop that risks exactly that. What changed instead: an
abnormal wait now logs a clear, periodic `ERROR`-level line every 30s pointing
directly at this incident, so a recurrence is visible in `server_debug.log`
without needing another pprof dump.

**Verified, not guessed.** A new hermetic test
(`TestPiStructuredTerminatesWhenChildHoldsStderrAfterProcessExits`,
`picli_structured_process_exit_test.go`) reproduces the mechanism exactly: a
fake `pi` whose own process exits cleanly (stdout drains normally, matching
what "stdout already closed" looked like in production) while a lingering
child holds stderr's write end open. Run against the pre-fix, actually-deployed
code (`fd00585`), it **times out with the identical stack trace** as the
production incident (`os/exec.(*Cmd).Wait → ...writerDescriptor.func1 →
io.Copy → IO wait`) — a reproduction, not a hypothesis. Passes after the fix.

This also explains the original 09:45 ICICI-BANK-PARSING incident that opened
this ticket: same adapter, same `cmd.Stderr = &bytes.Buffer{}` code, same
class of hang. The original incident's logs had already rolled over, so its
exact trigger (what held stderr open that time) is not separately provable —
but the mechanism now proven live is sufficient to close this ticket rather
than leave it as an open mystery.

## What is still true and unexplained

Not fully closed: **what actually holds stderr's write end open** in the first
place has not been identified for either occurrence (unlike the earlier,
disproven MCP-child-inherits-stdout theory, which was checked directly against
the MCP SDK source and ruled out). Candidates not yet investigated: a
descendant of a bash-tool-spawned command inheriting fd 2 in some path not yet
audited, or pi's own runtime under specific conditions. Worth a fresh
investigation if it recurs, now that the adapter itself won't hang regardless
of the answer.

## Superseded — original "where to look next" (kept for the record)

Untested hypotheses, roughly in order of fit:

1. **A tool call that never returned.** The strongest remaining candidate. pi
   emits no events between `tool_execution_start` and `tool_execution_end`, so a
   hung MCP tool (this step is browser/banking automation) looks identical to a
   silent-but-healthy long call. This workflow runs `TOOL_EXECUTION_TIMEOUT=90m`,
   so a hang inside that window is bounded only by the tool itself. Check whether
   the last event before the silence was a `tool_execution_start` with no
   matching `_end`.
2. **The layer above the adapter**, i.e. the same `for chunk := range textChan`
   bridge PLAT-116 traces for the tmux path — it is provider- and
   transport-agnostic and has no timeout of its own.
3. **The orchestrator/scheduler poll loop** — whether `query_step` can keep
   reporting in-progress for an execution whose underlying turn has already
   returned.

## Evidence needed (was not captured this time)

The 09:45 window is **no longer in `server_debug.log`** — it had rolled over by
the time the disproven diagnosis was unwound, which is part of why this stayed
unresolved. For the next occurrence, capture before restarting anything:

- the raw pi JSON event stream for the step (specifically: last event before the
  silence, and whether a `tool_execution_start` is unmatched);
- `ps` for the `pi` process **and** its children at the moment of the stall
  (note `pi` is a `#!/usr/bin/env node` script, so match on the full command
  line, not the process name);
- whether the platform's own completion logging
  (`[STREAMING_LIFECYCLE] StreamWithEvents completed`, `[COMPLETION] Updating
  session ... completed`) ever fires;
- goroutine dump of the server, to see which layer is actually parked.

## Acceptance

- [x] The stall is explained by evidence from a real occurrence, not by
  theory. NOTE: satisfied only on the second attempt. The first explanation
  (stderr pipe) came with a goroutine dump and a matching hermetic test and
  was still wrong about THIS incident, because one input to it — "pi's
  process is gone" — was never checked with a query capable of finding pi.
  Evidence being real does not make the conclusion right if a premise is
  false.
- [x] The layer responsible either completes or logs clearly instead of
  holding its caller indefinitely and silently — the `agent_settled` teardown
  restored in `@3d9bcc6` (the mechanism that actually ends a stuck pi), plus
  the `cmd.StderrPipe()` fix in `@6d8e4e9` (a real, separate defect), plus
  stall logging that now records the pid and whether the terminal event was
  seen (`@000b917`) so the two cases cannot be confused again.
- [ ] A run whose work genuinely finished never reports as permanently
  `running` — not addressed here; `run_metadata.json` freezing at
  `"status": "running"` on a swallowed terminal write is PLAT-116's
  separate, still-open item.

## Process note

The first diagnosis was reached by reading logs and source, defended across
three rounds of further source-reading, and shipped twice — before anyone ran
the CLI. Two live runs (~2 minutes) refuted it. For this class of bug, **run the
real thing early**; absence of a log line is not absence of an event, especially
when the code under investigation is what decides whether that event is logged.
