[← chat / reliability](index.md)

# PLAT-613: Confida QA reports a recurring forty-minute reply delay

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | chat |
| Area | reliability |
| Summary | Confida QA reports a second roughly forty-minute wait for a reply; the matched screenshot proves a terminal visibility bug and an interrupted later turn, but does not yet identify the forty-minute interval. |

## What happened

Reported 2026-10-06 for `confida-qa-testing` on Confida (Excellence host).
The user confirmed the screenshot was from today around 17:08 IST. The screenshot
matches Saurabh Khatri's durable Pulse review answer at 17:08:24 in
`pat-2ecd5c737adc95f6-d6a714c0-45c5-4707-9beb-674c2f92a230`.

That answer followed a `hi` at 16:56:21, took about twelve minutes and made 99
captured HTTP tool calls. The agent investigated and repaired Pulse data during
the turn; those operations are real work, not proof of a transport stall.
The next request, at 17:09:33, was confirmed in native history, made tools through
17:12:30 and has no final response before the forced deployment restarted the
server at approximately 17:12:54. A new `hi` at 17:24:15 returned at 17:25:12.

The confirmed missing main terminal has its own fix in [PLAT-612](plat-612.md).
The examined records do not establish a continuous forty-minute turn. Do not
claim that terminal registration fixes the reported delay or replay the
interrupted document-update request automatically: it already ran tools.

## Fix

Investigation only. No speculative timeout, cancellation or transcript repair.

## Left

Identify the other delayed occurrence with its message/session and start/end
time; correlate native step timestamps, provider/model activity and canonical
completion delivery. Verify the user's next live reproduction after terminal
visibility is repaired. Preserve the unresolved status until there is evidence
for the delay's cause and a verified remedy.
