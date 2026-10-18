package server

// Retain the existing contract marker; its retired per-schedule Pulse modes
// are converted once on manifest read, not recreated by this upgrade.
const upgradeExplicitSchedulePulse = `WORKFLOW CONTRACT UPGRADE: EXPLICIT AFTER-RUN OPTIONS FOR EVERY SCHEDULE.

Do only this migration. Load read_skill(skills=[{"name":"builder-reference","path":"references/schedules.md"}]). Read workflow.json and list_schedules, including disabled schedules and calendar schedules. The server preserves retained legacy choices in canonical after_run on read. Preserve those choices; if any ordinary schedule still lacks explicit backup, publish and notify booleans, use update_schedule(after_run={...}) to save its intended housekeeping. Webhooks do not run housekeeping. Pulse is workflow-wide, independent of normal runs; do not create per-schedule Pulse fields or change its setting, cadence or token cost.

Do not change cron expressions, timezones, route selections, schedule IDs, enabled states, messages, plan steps or destinations. Do not execute the workflow, run Pulse, publish, send notifications or perform public actions. Do not create schedules. Re-read the stored settings and confirm all unrelated fields are preserved. A workflow with no schedules needs no invented schedule. If saving fails, explain the blocker and do not stamp. Only after verification call set_workflow_contract_version(version="1.0.41") and stop. The stamp checks only canonical after_run; no pulse_mode or reason is required.`
