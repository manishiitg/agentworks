[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-528 — The agent's "working" spinner sat at the top of its turn, out of sight on a long message, and had no text

| Coordination | Value |
|---|---|
| State | fixed on main; not deployed; owner to check locally |
| Date | 2026-10-05 |
| Owner | frontend-chat |

## Source

Owner, Upwork chat during a workflow run: the robot icon + small spinner next to "13 tool calls" was the only sign the run was still going; on a very large message it is scrolled out of view ("its hidden and its not clear"). The spinner was accurate (a new run had started at 19:19:09 with no completion event).

## Done

- `TerminalEventTranscript.tsx`: the working state is now a Footer inside the Virtuoso scroller (same pattern as the "Load earlier messages" Header), under the last message, with visible text:
  "Working…", "Background agent running…", "Waiting for your input" (from the runtime activity label). The footer row always has the same height (h-7), whether or not the agent is working, so it appearing/disappearing does not move the list.
- Removed the spinner from the turn header (one indicator, not two); headers keep their recorded duration.
- `TerminalEventTranscript.activity.test.tsx` now asserts the footer (the Virtuoso mock renders `components.Footer` with its context). Type-check clean; transcript tests 47/47; components + products suites 1691 pass.

## Left

- Owner check: during a long reply and a workflow run the status line is visible at the bottom; after a workflow switch it does not add to the scroll flicker (PLAT-527).
