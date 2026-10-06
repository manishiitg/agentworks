[← coding-agents / sessions](index.md)

# PLAT-030 — main coding-agent terminal is not a durable scrollable transcript

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | coding-agents |
| Area | sessions |
| Summary | Terminal Center live-attach geometry lifecycle |

| Coordination | Value |
|---|---|
| Assigned agent | `Codex` |
| Ticket state | `implemented` |
| Last synchronized | `2026-08-05` |

- **Priority:** P0
- **Owner:** Terminal Center live-attach geometry lifecycle
- **Evidence:** the Social Media main terminal connected at `117×43`, then a
  vertical layout change reconnected it at `117×38`. The Claude tmux remained
  healthy and continued receiving automatic completion notifications, but its
  native `history_size` was `0`. The reconnect reset xterm and seeded only the
  current tmux screen, so all browser-accumulated history disappeared and the
  pane appeared frozen/unscrollable.
- **Root cause:** there were two independent limitations.
  1. Every grid change used the destructive width-change path. A row-only
     layout change therefore reset xterm even though line wrapping had not
     changed.
  2. Claude's live tmux pane is currently in the alternate screen
     (`alternate_on=1`, `history_size=0`). The raw pane is a fixed-screen TUI,
     not an append-only conversation transcript. Even after eliminating the
     resize reset, it cannot provide historical scrolling by itself.
- **Implementation:**
  1. Grid changes are classified as `none`, `rows-only`, or `columns`. A
     row-only change fits xterm and sends `{type:"resize", cols, rows}` over the
     existing socket. Column changes retain the suspend → close → fit →
     reconnect → authoritative-seed path.
  2. Main coding-agent terminals now default to their structured event
     transcript, which is durable and scrollable and receives the same
     automatic notifications. The header retains a `Raw` control for exact TUI
     inspection. Child terminals still default to raw output.
- **Verification:** focused frontend tests pin `117×43 → 117×38` as row-only
  and prove only column changes require reconnection. TypeScript compilation
  passes. Real tmux tests also prove target-session closure and external-width
  changes still close their viewers correctly.
- **Acceptance:** main-agent history remains readable and scrollable as new
  automatic notifications arrive. Vertically resizing the Terminal Center or
  showing/hiding UI chrome does not interrupt the live stream. A true width
  change may re-seed raw output because preserving old hard-wrapped rows would
  corrupt the display. Users can still select `Raw` when they need to operate
  the full-screen CLI, without treating that buffer as durable history.
