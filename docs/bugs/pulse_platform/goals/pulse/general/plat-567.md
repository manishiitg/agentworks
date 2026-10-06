[← goals / pulse / general](index.md)

# PLAT-567: get_pulse_state module view is oversized

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | goals |
| Area | pulse/general |
| Summary | get_pulse_state module view returned 320k to 384k characters; the agent view now caps the ledger and focus history |

## What happened

A Plan Drift session on Upwork (2026-10-06) read `get_pulse_state(view=module)` as its first call and received
384,349 characters, about 100k tokens. Its context compacted within minutes (236k to 24k tokens). Earlier calls in the
logs were 185k to 316k. On a copy of Upwork the view measured 320,760 characters: `impact_ledger` 202,541 (63%),
`review_focus_selections` 42,880, the reference map 9,191 (3%).

## Fix

The module view is read by the agent only. It loaded the 100 most recent interventions (plus the last 120 points per
active goal metric, kept on purpose for before/after windows) and 50 focus selections. It now loads the 20 most recent
interventions and 10 focus selections (`pulseModuleViewLedgerLimit`, `pulseModuleViewFocusSelectionLimit`); older
rows stay in the database and the UI route is unchanged. The ledger note says only recent rows are shown.
Measured on the Upwork copy: 320,760 to 198,759 characters (-38%).

## Left

- The view is still about 200k characters. What remains is history the roles legitimately read: assessments (33k),
  interventions (30k), observations (44k), the focus history (11k) and the modules block (11k). Cutting more means a
  summary with pointers to detail views, which changes what Goal Work and Strategic see; design first.
- Confirm live: the size of the next Drift or Technical pass's first `get_pulse_state` call in `server_debug.log`
  (`result_length=`).
