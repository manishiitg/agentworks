[← chat / reliability](index.md)

# PLAT-612: Resumed AGY chat shows an empty main terminal while the agent works

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | chat |
| Area | reliability |
| Summary | AGY launch-only warmup returned a live tmux handle without publishing a terminal frame; retained turns ran while the main terminal showed its not-started placeholder. |

## What happened

Confida `confida-qa-testing` (saved folder `Workflow/confida-login`), Saurabh
Khatri's chat, 2026-10-06. The supplied screenshot's Pulse review table exactly
matches the durable final reply at 17:08:24 IST. The follow-up requesting an
update for `PUL-9EB2AFD3` was confirmed in AGY's native SQLite conversation at
17:09:33 and produced tools through 17:12:30. Its terminal showed “The live view
appears once the agent starts working”. The AGY transport had been resumed via
launch-only at 16:55:48–16:56:21. That adapter branch returned an idle native
session handle before `streamAgyTerminal`, so warmup emitted no terminal frame.
Session-owned follow-ups use structured progress instead of that adapter stream.

The follow-up has no durable final reply. The Crew template deployment restarted
the busy server at approximately 17:12:54 (deploy log: `drain: still busy after
0s; restarting anyway`). AGY's native record ends with a completed tool rather
than an assistant final response. This interruption is separate from the absent
terminal. The recurring reported forty-minute delay remains [PLAT-613](plat-613.md).

## Fix

Publish one actual captured tmux terminal frame during AGY launch-only warmup,
before returning its session handle. The frame carries the same terminal type,
tmux identity and source as the normal stream; the host's existing terminal
event listener can register its main pane before live input starts. Warmup does
not send a user prompt or emit a generation answer. A failed or empty capture
fails warmup instead of reporting a usable invisible transport.

Provider source: `2a1f7dff8f053f9bf17aa17f16a8209c38fb5e69`, pinned in
`agent_go/go.mod` as `v0.7.4-0.20261006120923-2a1f7dff8f05`.

Verification: the adapter regression uses a real retained tmux pane, calls
`GenerateContent` with launch-only, and checks the terminal frame, idle handle,
native identity and empty response. It fails on the previous code (“live session
without its terminal frame”) and passes with the fix. The full AGY package tests,
provider lint and application's existing main-terminal HTTP tests pass.
The same real-tmux adapter regression also passed as the `confida` service
account on the production Linux host; its disposable pane and test binary were
removed afterwards. No live user session was used for this test.

Deployed on Confida as `confida-15cefc0a-20261006142014` from shared build
`15cefc0a-20261006121420`. The running release's `SOURCE_REVISIONS` confirms
application `15cefc0a3af868bc34567be8cbe59ba5ee006f2c` and provider
`2a1f7dff8f053f9bf17aa17f16a8209c38fb5e69`; the shipped provider source contains
the launch-only terminal seed. Release asset checks passed, public health
returned 200, and slot self-tests reported 77 passed, zero failed, 35 skipped.
Activation waited for idle (`drain: agent idle, restarting`).

## Left

User refresh/reproduction of the resumed AGY terminal after deployment. Keep
the forty-minute delay investigation open in PLAT-613; this terminal fix does
not prove that latency is resolved.
