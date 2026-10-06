[← coding-agents / claude](index.md)

# PLAT-052 — scheduled consecutive turns tore down the native Claude session

| Field | Value |
|---|---|
| State | closed |
| Priority | P1 |
| Product | coding-agents |
| Area | claude |
| Summary | Scheduler-to-coding-CLI session lifecycle |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-07` |

- **Priority:** P1
- **Owner:** Scheduler-to-coding-CLI session lifecycle
- **Source workflow:** Upwork scheduled upgrade / Pulse run
- **Related tickets:** PLAT-048 (bounded terminal lifecycle), PLAT-050
  (single continuing Pulse conversation)

## Problem and evidence

The scheduler intentionally sends known consecutive turns in one schedule
conversation: upgrade → normal run → Pulse. Its request was classified as
non-persistent, however, so the Claude adapter closed the native interactive
process before resuming the next turn. The raw terminal showed the adapter's
literal control sequence:

```text
/exit
Bye!
```

That was not agent output or a user command. It came from
`closeClaudeSessionForResume`, which sends `/exit` before starting a new
`--resume` process. Apart from noisy transcript/UI output, the teardown creates
an avoidable race and loses the continuity expected by ordered schedule turns.

## Fix

- Added `keep_native_session_alive` to the server query contract.
- The scheduler sets it on its known contiguous turns.
- The coding-agent mode allows that flag to retain the native CLI process, but
  only after rejecting child, typed-stage, and auto-notification requests. It
  cannot accidentally turn a background agent into a retained session.

## Verification

Focused mode tests prove that a scheduled continuation is persistent while a
background child remains non-persistent:

```text
go test ./cmd/server -run 'TestCodingAgentRequestAllowsPersistentInteractive|TestCodingAgentPersistentInteractiveFlags' -count=1
go test ./cmd/server -run '^$' -count=1
git diff --check
```

## Runtime acceptance / regression check

After the backend restart, run a schedule that has consecutive upgrade, normal,
or Pulse turns. The same session must advance without visible `/exit` / `Bye!`
between turns, and its terminal process must still be reaped at normal final
completion under PLAT-048. Seeing the adapter control sequence again, or a
leaked completed schedule process, reopens this ticket.
