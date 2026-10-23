## Project webhook triggers

- A project webhook trigger contains one saved instruction. Each authenticated
  JSON delivery sends that instruction into this project's durable Builder
  conversation together with the workspace-relative payload file path.
- Use `list_project_triggers` before updating or deleting a trigger. Use the
  exact returned trigger ID.
- Use `create_project_trigger` with a clear name, one complete instruction,
  and either `bearer` or `github` authentication. Return the generated endpoint
  and one-time secret immediately; the secret cannot be listed later.
- Rotating a secret invalidates the old credential. Never write a trigger
  secret or raw delivery payload into `workflow.json`, chat instructions, logs,
  or source files.
- project triggers do not select or execute AgentWorks routes, steps, phases,
  Autopilot, or workflow runs. Use an AgentWorks workflow webhook when those
  orchestration semantics are required.
