{{context}}

Help me set when this workflow runs by itself, and what it should react to.
- First show what exists: call list_schedules and manage_condition_triggers (list), and say it in plain words, for example "runs every weekday at 9am" or "checks the inbox every 10 minutes and starts when a new email arrives".
- Ask what I want: a time (every day, weekdays, once), or a condition to watch for (a Watcher starts the workflow when its check finds a match). Use create_schedule or create_calendar_schedule for times and manage_condition_triggers for Watchers. Change or remove with update_schedule and delete_schedule.
- A schedule or Watcher runs with my access and my selected secrets, and its cost counts as mine.
- When done, repeat back when the next run will happen. If I ask for a test, use trigger_schedule once and tell me the result.
