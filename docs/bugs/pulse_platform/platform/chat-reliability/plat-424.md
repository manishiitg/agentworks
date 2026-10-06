[← platform / chat-reliability](index.md)

# PLAT-424 — CLI delivery health: notice duplicate sends, instant launch deaths and slow acceptance without waiting for a user report

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | platform |
| Area | chat-reliability |
| Summary | open (planned, not started). |

| Coordination | Value |
|---|---|
| State | open (planned, not started) |
| Severity | P2 (three Muse failures on 2026-10-04 were found only because the owner hit them) |
| Date | 2026-10-04 |
| Owner | chat-reliability |
| Related | PLAT-352 (durable submit acknowledgement, `docs/refactor/durable_ack_p0.md`), PLAT-417, PLAT-421, PLAT-422 |

## Problem

On 2026-10-04 three Muse faults reached the owner before any check noticed them:

1. PLAT-421: every confined launch died about 185 ms after the start (a bare `muse` not found in the slot's PATH); the chat only said "died while waiting for the TUI".
2. PLAT-422: one message ran three or more times (seven copies in the pane); Muse's own log held one accepted record per copy.
3. A first message took 98 s to be accepted after a resume; the cause is still not explained.

None of them is visible in a steady way today. The durable records that would show each of them already exist (the CLI's intake and answer records, the launch exit), but nothing compares them.

## Plan (all from durable records, none from screen text)

1. **Post-deploy CLI smoke test.** After each deploy, send "hi" to every CLI the server supports through the real app path and check: one accepted-message record, one answer, accepted within a time limit,
   no extra sends. The deploy prints pass or fail, like `verify-browser-matrix.py`; it must run against a throwaway session and clean up.
2. **Duplicate-delivery check, per turn.** After intake, count identical-text accepted records since the turn began; more than one raises a visible warning event with the session and count.
3. **Instant-launch-death check.** A terminal that ends within a few seconds of starting is counted and recorded with the launcher's first error line (`last-launch.stderr`; the text part is in provider `b629fb0`).
4. **Health row.** The runtime-health view shows the last smoke result per CLI, the slowest acceptance times of the day and the duplicate and launch-death counts.
5. **Audit the other CLIs against the durable-ack rule.** Claude's clear-and-resend (gated by a transcript check first) and Codex's Enter-again (gated by the durable oracle first, bounded) still use pane text to decide a resend; check them against "attempt once, observe-only" and fix only with a failing case.
6. **Slow first message after a resume.** Measure where the 98 s goes (cold TUI start or the resumed run re-attaching) and look for a structured "Muse is ready" record to wait on before submitting.

## Acceptance

A deploy that breaks a CLI's launch or makes it send twice fails the smoke test before the owner sees it; a duplicate or an instant death on a live server shows up in the health view the same day.

## Done

Nothing yet. Fixes already made today: PLAT-421 and PLAT-422.

## Register notes

[PLAT-424](plat-424.md), P2, open (planned, not started). Notice duplicate sends, instant launch deaths and slow acceptance without a user report.
