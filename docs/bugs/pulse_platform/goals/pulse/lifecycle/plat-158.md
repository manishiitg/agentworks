[← goals / pulse / lifecycle](index.md)

# PLAT-158 — Legacy `post_run_monitor` makes ordinary workflow runs launch Pulse even when a dedicated Pulse schedule exists

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P0 |
| Product | goals |
| Area | pulse/lifecycle |
| Summary | P0 — it makes expensive review work run at the wrong time, extends |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented` — focused verification passed; restart/live reverify pending |
| Last synchronized | `2026-08-19` |

- **Priority:** P0 — it makes expensive review work run at the wrong time, extends
  ordinary schedules by hours, and defeats the operator's explicit Pulse cadence.
- **Owner:** workflow manifest, scheduler lifecycle dispatch, schedule tools and
  Pulse setup guidance.

## Evidence and RCA

Social Media already had an enabled dedicated `pulse_review_only` schedule at
12:00 and 22:00 IST. Its ordinary 10:00 execution nevertheless appended the
full Pulse Gate at 13:42. The manifest simultaneously carried
`post_run_monitor=true`, `post_run_monitor_mode=periodic`, and the dedicated
schedule.

The system therefore had two sources of truth for one lifecycle:

1. a workflow-level boolean/mode that made the scheduler append Pulse after an
   ordinary run; and
2. an enabled dedicated schedule that started Pulse independently.

`periodic` reduced which review modules were due; it did not disable the inline
Pulse lifecycle. The name implied separation while the runtime still entered
the same Gate/Review+Fix path. This is why the operator saw Pulse running after
an ordinary workflow despite having already moved it to its own schedule.

## Fix and reasoning

1. An enabled `pulse_review_only` schedule is now the sole recurring-Pulse
   configuration and cadence authority.
2. `post_run_monitor` and `post_run_monitor_mode` were removed from the manifest
   model, update API, workflow tool schema, and builder guidance.
3. An ordinary workflow schedule never launches Gate or Review+Fix. When a
   dedicated Pulse schedule exists, it receives only the short backup, report
   publication, and run-summary finalizer needed for that workflow run.
4. A dedicated/manual Pulse launch still runs the full Gate → Review+Fix →
   Finalize sequence against retained evidence.
5. Existing manifests were migrated. Workflows that previously relied on the
   boolean received an explicit review schedule; a workflow with all schedules
   disabled did not get a newly enabled review schedule.
6. Contract upgrade and `/pulse-setup` guidance now create the explicit
   execution and review schedules rather than setting a hidden second toggle.

The scheduler still owns deterministic sequencing and lifecycle receipts. What
was removed is the duplicate configuration and implicit inline review, not the
agentic review/fix work performed inside the dedicated Pulse turn.

## Acceptance

1. No manifest/API/tool schema exposes either legacy field.
2. An ordinary run with an enabled Pulse schedule cannot construct a Gate or
   Review+Fix message.
3. That ordinary run can still back up, publish its report, and notify the user.
4. The dedicated Pulse occurrence runs the complete review lifecycle.
5. Manual one-off Pulse works even when no recurring review schedule exists.
6. Existing enabled workflows have at most one enabled dedicated Pulse schedule.
7. Disabled workflows are not silently re-enabled by migration.
