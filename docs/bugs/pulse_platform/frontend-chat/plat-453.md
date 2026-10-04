[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-453 — Plan and Browser move into the workflow toolbar's Ops group

| Coordination | Value |
|---|---|
| State | fixed on `main`; not deployed |
| Date | 2026-10-04 |
| Owner | frontend-chat |

## Change (owner request)

In the workflow top toolbar the Views group showed Pulse, Needs you, Activity,
Plan and Browser. Plan and Browser now sit inside the collapsible **Ops** group,
so Views keeps only Pulse, Needs you and Activity. A Relay keeps its Graph (its
Plan view) in Views.

- `PRIMARY_TOOLBAR_VIEW_IDS` is `['pulse']`; `OPERATIONS_TOOLBAR_VIEW_IDS` gains `flow`
  and `browser`.
- Ops is collapsed by default, so the Ops label is highlighted when one of its
  views (Plan, Browser, Files, ...) is the current one (`WorkspaceToolbarGroup`
  `active` prop); otherwise nothing would show as selected.
- The panel guides (`workspacePanelGuides.ts`, AgentWorks surface only) list Plan
  and Browser under Ops; the Code and Crew toolbars are separate and unchanged.
- Walkthrough text and tooltips updated; the three tests that pinned the old
  placement updated.

## Left

- Plan is the usual landing view, so a workflow opens with Ops collapsed and the
  Ops label highlighted; opening Plan means expanding Ops first. If that is too
  slow, Plan can be pinned back in Views.
- `formsKitAdoption.test.ts` fails on current main, unrelated to this change.
