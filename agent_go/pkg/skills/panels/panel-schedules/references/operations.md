## Project schedules



- A project schedule contains exactly one message sent to this project's chat.
  It is not an AgentWorks workflow, route, phase, or execution.
- Use `list_project_schedules` before updating, deleting, or triggering. Use the
  exact returned schedule ID.
- For a recurring schedule, provide a clear name, one complete message, a
  standard five-field cron expression, and an IANA timezone such as
  `Asia/Kolkata`. Enable it by default unless the user asks to save it paused.

## Reminders ("check this in 15 minutes")

- A reminder is a one-time schedule: `create_project_schedule` with `in_minutes`
  (or `run_at` for an exact moment) and a self-contained message saying what to
  check and what to report. It runs once.
- Check first when it is cheap. If the thing can be checked right now (a deploy,
  a CI run, a reply), look once before scheduling. If it is already settled,
  report that instead of creating a reminder, and offer one if the user still
  wants it ("It already finished at 06:42; want a reminder anyway?").
- Never create a reminder and then delete it in the same turn. A reminder the
  user asked for stays unless they agree to remove it; if it turns out to be
  unneeded, say so and ask.
- When the reminder fires, do the check and report the result in the chat; do
  not schedule another one unless the user asked for repeated checks.
- Use `trigger_project_schedule` only when the user asks to run it now. Report
  the resulting session without inventing a workflow run.
