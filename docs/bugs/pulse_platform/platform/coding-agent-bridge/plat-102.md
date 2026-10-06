# PLAT-102 — retained coding-agent messages must keep the fast live-input path

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | platform |
| Area | coding-agent-bridge |
| Summary | A follow-up sent to an already-running coding CLI has two possible shapes: |

| Field | Value |
|---|---|
| Status | `partially implemented` — cold-start diagnosis and both latency fixes landed; per-provider attribution and the E2E contract are outstanding (audited 2026-08-14) |
| Priority | P1 |
| Owner | coding-agent retained-turn transport |
| Reported | 2026-08-14 |
| Related | [PLAT-020](plat-020.md), [PLAT-035](plat-035.md), [PLAT-099](plat-099.md), [PLAT-103](plat-103.md), [PLAT-105](plat-105.md) |

## Problem

A follow-up sent to an already-running coding CLI has two possible shapes:

1. inject the message into the retained provider session; or
2. construct and run a complete `mcpagent` agent turn before delivering it.

Only the first shape is appropriate for a warm retained conversation. Repeating
agent construction adds seconds before the provider sees input even though no
database access, workflow mutation, or new coding process is required.

## Measured evidence

The same retained Codex session in `logs/server_debug.log` showed:

- direct live input: 15 ms paste / 27 ms confirmed on one message;
- direct live input: 40 ms paste / 57 ms confirmed, 90 ms total HTTP time on
  the next message;
- full agent path: session bookkeeping was immediate and agent construction
  completed at 189 ms, but the persisted tmux was explicitly unavailable
  (`attach_existing result=skip reason=tmux_unavailable`). The cold recovery
  therefore launched a new Codex tmux, whose launch-only call took 5.882 s;
  streaming opened at 6.333 s;
- the actual tmux delivery at the end of that full path still took only 27 ms.

A controlled live UI test on 2026-08-14 then sent two follow-ups to that same
already-running Codex tmux:

| Probe | Paste accepted | Submission confirmed | HTTP completed | Model reply |
|---|---:|---:|---:|---:|
| `latency-ready-1` | 129 ms | 167 ms | 263 ms | 4.7 s |
| `latency-ready-2` | 39 ms | 59 ms | 98 ms | 10.1 s |

The HTTP and submission measurements prove delivery is sub-second. The final
column is model inference time and must not be attributed to message sending.
The first probe's additional ~96 ms after confirmation includes server-side
turn bookkeeping/history persistence; the second needed ~39 ms.

The delay is therefore not Go execution or SQLite. In the measured slow case,
the agent was persisted but its tmux process was not live, so a cold recovery
was necessary. A genuinely warm message does not exhibit the multi-second
delay.

`mcpagent.DeliverAgentInput` is also a live-delivery API, not the full agent
turn path: it trims/validates the request, checks the provider transport
contract, and calls the same `SendCodingAgentLiveInput` provider operation.
Routing a warm message through that API should retain the measured sub-100 ms
common path. Calling agent `Start`/`Ask` is the operation that reconstructs
configuration and performs cold provider readiness; it is not required for
warm delivery.

## Required design

- A retained conversation uses the typed live-input transport directly.
- Sending must not construct an `Agent`, call `Start`, rebuild the tool index,
  rebuild MCP bridge configuration, or call `Ask` merely to deliver text.
- Success is returned only after paste and initial submit are accepted; slower
  repaint/retry verification may continue asynchronously where the provider can
  do so safely.
- Record queue, paste, submit-confirmation, and total request latency separately.
- Cold start and dead-session recovery remain full agent operations; this ticket
  does not bypass required setup when no live provider session exists.
- Structured output observation is independent and owned by PLAT-103.

## Acceptance

1. A warm retained message reaches every supported coding CLI without starting
   or reconstructing an agent session.
2. A real E2E records delivery latency independently from model response time.
3. The common uncontended path remains below 250 ms, with observed values and
   failure reasons logged rather than enforced by a brittle sleep-based test.
4. Rapid messages remain serialized per provider session and are never merged,
   duplicated, or delivered to a stale provider after an automation switch.
5. Cold/dead sessions fall back deliberately instead of pretending the fast
   retained path succeeded.

## Current state

AgentWorks already uses the correct direct transport for a live retained main
agent, and the 27–57 ms measurements prove its value. The remaining work is to
make this a pinned shared contract, add complete latency attribution, and ensure
no caller regresses to full per-turn construction for warm delivery.

On 2026-08-14 the Codex interactive adapter gained stage-level startup timing.
New `codex interactive startup timing` log records separate `build_args`,
`runtime_paths`, `start_tmux`, `acquire`, `prompt_ready`, and `launch_finalize`
durations. `launch_finalize` further separates `MarkReady`, terminal snapshot,
and provider-session handle construction. Reused sessions also report
`reuse_lock`, which exposes waits behind an in-flight turn instead of
mislabeling them as process startup. This closes the observability gap in the
measured 5.882-second cold start; the next cold recovery can identify the exact
stage rather than attributing the whole interval to `Start`.

The first instrumented run then isolated two concrete waits. Cold `Start` took
5.325 s: tmux/config acquisition was only 49 ms, Codex prompt readiness was
2.831 s, and the launch-only terminal snapshot took 2.445 s. The snapshot was
unnecessarily building a status line by recursively scanning historical Codex
rollouts before any new turn existed. Launch-only rendering now captures the
pane without that status scan.

The same run showed warm input pasted in 19 ms and was visibly accepted in
31 ms. Codex's answer was captured after 7.047 s, but `Ask` returned after
13.350 s because the generic trailing-pane grace window waited for two stable
two-second polls. That protection is required before destroying bounded
workflow terminals, but not for a persistent chat terminal that remains live
and independently streamed. Persistent Codex sessions now skip that trailing
wait; bounded sessions retain it.

## Audit 2026-08-14 — what is actually in the tree

A code audit confirmed every implementation claim above and found that none of
the ticket's own acceptance criteria are yet met. The ticket is therefore
`partially implemented`, not `implemented`.

**Verified present.** All seven Codex startup stages exist in
`codexcli_interactive_adapter.go` (`build_args`, `runtime_paths`, `start_tmux`,
`acquire`, `prompt_ready`, `launch_finalize` separating
`mark_ready`/`snapshot`/`handle`, and `reuse_lock` on reuse). The launch-only
snapshot no longer scans historical rollouts. Persistent sessions log
`trailing capture skipped … reason=persistent_session` while bounded sessions
retain the grace window.

**Outstanding — three items, none large:**

1. **Pi has no latency instrumentation.** `LATENCY_DEBUG` exists in the Codex,
   Claude Code, and Cursor interactive adapters but not in `picli`. Pi is a
   supported live-input provider (`SupportsLiveInput: true`, and it has its own
   retained-turn sidecar reader), so acceptance 1 ("every supported coding CLI")
   and acceptance 3 ("observed values logged") both fail for it.
2. **Total request latency is never recorded.** The Required design asks for
   queue, paste, submit-confirmation, and total request latency *separately*.
   The adapters cover paste and submit-confirmation only; every `[LIVE INPUT]`
   log line in `server.go` carries no timing at all. The two measurements that
   would substantiate the sub-250 ms claim are the two that are missing, and
   the numbers in this ticket came from a manual live-UI probe rather than from
   logs.
3. **Acceptance 2 has no coverage.** No `*_test.go` in `multi-llm-provider-go`,
   `mcp-agent-builder-go`, or `mcpagent` references the latency instrumentation,
   so nothing records delivery latency independently from model response time.

The recorded fields are also not uniform: Codex logs `pasted`/`confirmed` while
Claude Code logs `pasted`/`handoff`/`confirmed`/`retries`. Normalizing that is
the same problem [PLAT-105](plat-105.md) addresses one layer up at the delivery
acknowledgement, so the two should be sequenced together — PLAT-105 now owns
pinning the shared contract that this ticket's "Current state" left open.

## 2026-09-03 addendum

A live RTS `hi` on the locked Cursor provider measured 52 s to first token, of
which 38.4 s was platform time before the coding agent was launched — two
passes of nested MCP connect retries against an unauthenticated connector, a
cost this ticket's traces never showed because their connectors were signed
in. Filed and fixed as [PLAT-275](plat-275.md); after it the pre-launch cost is
0.4 s and the remaining ~14 s is cursor-agent's own startup, which is the
"cold coding-agent startup" wait this ticket already names.

## 2026-09-18 — Permission admission is required before the fast path

Local implementation/regression verification; deployment and live latency measurement pending. See [PLAT-262](../security-sandbox/plat-262.md) and [PLAT-324](../../crew/chat-reliability/plat-324.md).

An unchanged AgentWorks workflow policy still reaches the existing SDK-owned warm delivery without reconstructing an Agent, tools or bridge. Fresh permission/manifest lookup and the existing policy key comparison precede delivery. A changed or unverifiable admission is an intentional reconstruction boundary, just like a changed definition; stale permissions must not be retained for latency. No new per-provider send path is introduced.

Early interruption applies only to eligible retained-delivery requests. Automatic notifications, explicit new-turn requests and manifest configuration overrides retain their normal setup/lane behavior. Live latency has not been measured for this additional authorization lookup; acceptance below 250 ms remains an explicit post-deployment check, not inferred from passing unit tests.
