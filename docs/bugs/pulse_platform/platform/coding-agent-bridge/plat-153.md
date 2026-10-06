[← platform / coding-agent-bridge](index.md)

# PLAT-153 — a structured pi turn had no overall ceiling, and could wedge internally for hours with nothing bounding it

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | P1 — a workflow step can occupy a group indefinitely with no |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `fixed and confirmed live` — hard turn ceiling shipped, a real group-kill bug found and fixed while proving it, and the ceiling has now fired on a real same-day production recurrence exactly as designed |
| Last synchronized | `2026-08-19` |

- **Priority:** P1 — a workflow step can occupy a group indefinitely with no
  automatic recovery. Not a defect in our own orchestration code (unlike
  PLAT-139, PLAT-150) — the root cause is upstream, in `pi` itself — but the
  blast radius (a stuck step blocking a group, previously unbounded) was ours
  to fix regardless.
- **Owner:** `multi-llm-provider-go/pkg/adapters/picli/picli_structured_adapter.go`

## This is not "pi CLI is broken"

Said plainly, because it is easy to over-read this ticket: pi is a real,
widely-used tool, and the overwhelming majority of turns in this deployment
complete normally — including every other run in the same session as the
incident below. What follows is a rare, edge-case internal wedge that pi's own
maintainers have documented hitting themselves, not a claim that pi cannot be
relied on.

## The incident

2026-08-19, workflow `check-form-26as-xspaces`, group `excellence`, step
`tax-retrieval-sequence`. A structured pi process ran for over an hour with
zero forward progress:

- Its last real tool call (`execute_shell_command`) completed in **28ms**, with
  nothing after it.
- The new stall log (from [PLAT-139](plat-139.md)) showed `stream_silent_for`
  climbing without bound — 6m → 14m → 22m47s and rising — with
  `terminal_event_seen=false` throughout, ruling out PLAT-139's exit-teardown
  deadlock.
- Neither pi nor its `mcpbridge` child held any TCP socket. Not waiting on the
  model API, not waiting on a tool's network call.
- A native `sample` of the actual process (not a Go goroutine dump — this is
  Node's own runtime) showed the main thread parked in `uv__io_poll` →
  `kevent`, every worker thread blocked on `uv_cond_wait`, no thread in any
  syscall corresponding to active work. The event loop was alive and idle,
  consistent with something inside pi's own JS awaiting a Promise nothing will
  ever resolve.

## Root cause: nothing bounded the turn at all

Traced why nothing intervened. `TOOL_EXECUTION_TIMEOUT`
(`agent_go/cmd/server/agent_tuning.go`) only wraps **individual tool calls**
made through the bridge (`ToolRuntimeConfig.Timeout` →
`context.WithTimeout(ctx, toolTimeout)` around one `Tool.Execute`, in
`mcpagent/agent/conversation.go:1320`) — and this deployment did not even have
it set, leaving the real per-tool default at 5 minutes
(`agent_go/pkg/agentwrapper/llm_agent.go:463-464`), not the 90 minutes an
earlier fix in this session claimed without checking (that comment is
corrected as part of this fix).

Nothing placed a deadline on the ctx passed into `generateContentStructured`
itself — the whole turn, i.e. the pi subprocess's own lifetime. A pi that gets
internally wedged, not blocked on any one tool call but simply making no
progress, could run until the server itself restarted.

## Not speculation — pi's own issue tracker

- **[earendil-works/pi#8004](https://github.com/earendil-works/pi/issues/8004)**
  — filed by pi's own maintainers after real freezes of **5.5 and 8.7 hours**.
  One matches this incident's shape almost exactly: *"a subagent tool call
  spawned a nested pi process that completed its work but never exited (loaded
  extensions kept the node event loop alive)."* Quote: *"Any tool call that
  hangs freezes the entire session indefinitely. There is no general tool-call
  timeout... the only escape is the user pressing Esc."*
- **[#5778](https://github.com/earendil-works/pi/issues/5778)** — `pi-agent-core`
  wedging on unresponsive streams or a tool's `execute()` promise never
  resolving; references an unmerged patch adding `streamTimeoutMs`/
  `toolTimeoutMs` config that does not exist in our installed 0.84.2 (`pi
  --help` shows no timeout flags at all).
- **[#8331](https://github.com/earendil-works/pi/issues/8331)** (open),
  **[#7636](https://github.com/earendil-works/pi/issues/7636)** (closed) —
  same class from other angles (stalled provider stream; a bash tool hanging
  when a spawned daemon inherits stdio).

Several of these are auto-closed as new-contributor reports rather than
genuinely resolved (`earendil-works/pi`'s bot auto-closes all new-contributor
issues by default) — so this is not something safe to wait out upstream.

## Fix

`piMaxTurnDuration()` — a hard ceiling on one structured pi turn, default
**45 minutes**, overridable via `PI_STRUCTURED_MAX_TURN_DURATION`. Derived from
`ctx` via `context.WithTimeout`, so the caller's own cancellation is still
honored; this only adds a floor under it. 45m is a reasoned default (well above
every turn duration observed in this deployment today), not a measured one —
no turn-duration history exists yet to derive it from properly.

## A second, real bug found while proving the first fix

`exec.CommandContext`'s **default** cancellation is `cmd.Process.Kill()` — the
single tracked pid only. `Setpgid: true` was already set on this command
specifically so a killer *could* target the whole process group, but the
default kill does not do that. A grandchild pi forks off (an MCP bridge,
anything else) would survive as a reparented (`ppid=1`) orphan even though the
tracked process died.

Caught by the test itself, the honest way: an early version checked only the
tracked process's own recorded pid and **passed** while a real grandchild
leaked underneath it, three separate times, confirmed with `ps -eo
ppid,stat` showing live `ppid=1` orphans after the "fixed" run. Strengthened
to record and verify both the script's own pid and a deliberately backgrounded
grandchild's pid before trusting green.

Fixed with `cmd.Cancel`, overriding the default with
`syscall.Kill(-cmd.Process.Pid, syscall.SIGKILL)` — the same group-kill
primitive `procshutdown.GracefulAfterNaturalExit` already uses correctly
elsewhere in this same package. Cancellation for any reason — this ceiling, or
the caller's own context — now reaps the whole group.

## Verification

- Both fixes verified fail-before/pass-after against a fake pi that genuinely
  never exits on its own (backgrounds `sleep 999999`, matching the real
  incident's "alive, idle, zero progress" shape — not a script that just exits
  late):
  - Ceiling absent: the call runs until the caller's own 30s test timeout.
  - `cmd.Cancel` absent: the tracked process dies, the grandchild survives as a
    live, reparented orphan.
- Full `picli` + `utils` suites pass; zero zombies, zero leaked processes after
  the run (`ps aux | grep sleep` / zombie count both checked directly, not
  assumed).
- **Verified live in production, 2026-08-19, same-day recurrence.** A fresh
  `excellence` pi wedged again after the fix shipped (same signature: zero
  tool calls, `terminal_event_seen=false`, growing `stream_silent_for`).
  Confirmed by process evidence from BOTH sides of the pipe, not inference
  from one side: a native `sample` of pi itself showed a completely idle Node
  event loop (main thread parked in `uv__io_poll`/`kevent`, no thread in any
  syscall), and a native `sample` of `mcpbridge` showed it correctly idle
  too — two threads blocked in `read()` waiting for pi's next request, **no
  thread blocked in `write()`** — ruling out "our own bridge silently failed
  to deliver a response" as an alternative explanation for the same symptom.
  `mcpbridge.log` confirmed the bridge's last real tool call succeeded
  (`status=200`) well before the wedge began.

  The ceiling fired at exactly the configured default:
  `LLM RESPONSE RECEIVED - Duration: 45m0.029002916s`. The group-kill reaped
  the wedged process, the failed turn was reported back to the orchestrator,
  its retry logic started a fresh pi call immediately (`20:56:39`), which
  completed normally in 1m9s, and the group resumed executing tool calls
  within two minutes of the wedge being cut loose.

## Deliberately out of scope

- The other three coding-agent adapters (`claudecode`, `codexcli`, `cursorcli`)
  likely have the same "no overall turn ceiling" gap — their
  `generateContentStructured`-equivalents were not audited for this. Only pi
  has live evidence today. Worth a follow-up pass once this pattern is proven
  in production.
- Root-causing *why* pi's event loop actually wedges (which Promise, which
  code path) — not possible from outside pi's bundled dist without its own
  debug tooling, and not something we can fix regardless since it is upstream.

## Acceptance

- [x] A structured pi turn cannot run unbounded regardless of what the
      caller's context does or does not bound.
- [x] Recovery from the ceiling firing reaps the whole process group, not just
      the tracked pid.
- [x] Confirmed live in production: the ceiling fired on a real recurrence at
      exactly 45m0.029s, group-killed cleanly, and the retry path recovered
      the workflow within two minutes.
