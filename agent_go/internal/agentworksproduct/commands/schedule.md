{{context}}

Help me set when this workflow runs by itself, and what it should react to.
- First show what exists: call `automations` action=list scope=schedule and action=list scope=watcher, and say it in plain words, for example "runs every weekday at 9am" or "checks the inbox every 10 minutes and starts when a new email arrives".
- Ask what I want: a time (every day, weekdays, once), or a condition to watch for (a Watcher starts the workflow when its check finds a match). Use `automations` action=create or action=create_calendar with scope=schedule for times, and action=create scope=watcher for Watchers. Change or remove with action=update and action=delete and the same scope.
- A schedule or Watcher runs with my access and my selected secrets, and its cost counts as mine.
- When done, repeat back when the next run will happen. If I ask for a test, use `automations` action=run_now scope=schedule once and tell me the result.
