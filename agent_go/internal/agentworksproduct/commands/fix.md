{{context}}

Look at the last run that failed and fix the workflow so it does not fail the same way. Agents fix the workflow; I should not have to edit anything by hand.
- Call `runs` with action=active to find the last failed run. Read the failing step's output and logs. Say in plain words what went wrong and why, with the evidence (the line or value that shows it).
- Fix the cause in the plan, a step description, a script or a setting. Prefer the smaller change and prefer removing something over adding more rules.
- If the cause is outside the workflow (a login expired, a service is down, a secret is missing), tell me exactly what I need to do instead of changing the workflow.
- Do not run it again unless I ask. Tell me what you changed and how I can check it (/run).
