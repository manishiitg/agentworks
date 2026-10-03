[← Platform issue index](../../pulse_platform_issue_register.md)

# PLAT-398 — Relay output and workflow toolbar clarity

| Coordination | Value |
|---|---|
| State | fixed on main; deployment pending |
| Date | 2026-10-03 |
| Owner | frontend-chat |

## User decision and implementation

- Remove the Relay output selector from the graph toolbar (it also appeared
  over step details). Keep the authored output choice in workflow.json and
  configure it through Builder chat. The selected agent carries an Output badge.
- Authored agent details label ordered prompts User messages and user message,
  while keeping Exact system prompt above them. Ordinary workflow sequences
  retain their existing Agent instructions section.
- Workflow Views, Ops and setup groups stay open. Setup shows icons without
  its label. Move Automation into Ops; Relays call that same view Triggers.
  The existing views, click actions and product restrictions are reused.

## Verification

Frontend TypeScript compilation and all 16 targeted regressions passed across
workflow toolbar placement, responsive layout, scripted presentation and Crew
nodes. Existing layout expectations now assert Automation belongs to Ops and
there is no toolbar-group toggle state. Browser remained closed per the user.

## Remaining

Deploy the frontend through the normal release. No running app was changed.
