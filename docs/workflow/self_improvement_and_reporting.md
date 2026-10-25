# Goal progress, reviews and reporting

A Goal stores its purpose and success criteria in `soul/soul.md`. The owner sets
measurable targets through Builder, ordinary steps collect source-backed readings,
and the platform stores definitions and history in the Goal database. Goals &
metrics and dashboards show the same facts. See [Goal measurements](../goal-measurements.md).

AgentWorks can review a linked Goal through its authorized tools and ask its
Builder to make changes. It is an ordinary Crew agent with skills and schedules;
there is no separate built-in review engine, toggle, timer or finding ledger.
Review recommendations use existing owner decisions and their application rules.
Reading another product never grants its Builder permissions.

Normal schedules run the saved workflow. After-run backup, publish and notify
remain independent options. Failure notices, work-stopped decisions and the daily
digest use the Goal's saved notification settings. `notifications.wake_tab` can
notify a selected Crew tab after a failed run. Notifications do not transfer
accounts, tokens or extra authority to the receiving agent.

Build and share through the website or AgentWorks MCP with the same existing
Builder/Run rules. Existing metrics, costs, run logs and owner decisions remain
available when no reviewing agent or review schedule has been set up.
