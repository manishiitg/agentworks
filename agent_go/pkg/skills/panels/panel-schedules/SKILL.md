---
name: panel-schedules
description: Schedules panel in Goals and Crews. Create, change, pause, resume, run or inspect a scheduled caller.
metadata:
  panel-tools:
    work:
    - list_project_schedules
    - create_project_schedule
    - update_project_schedule
    - delete_project_schedule
    agentworks:
    - list_schedules
    - create_schedule
    - update_schedule
    - delete_schedule
    - trigger_schedule
    - get_schedule_runs
  external-tools:
  - manage_schedules
  - list_schedules
  - get_schedule_runs
  - trigger_schedule
---

# Schedules

List before editing. Crew uses list_project_schedules and the create/update/delete project schedule tools; Goal uses list_schedules/create_schedule/update_schedule/delete_schedule, trigger_schedule and get_schedule_runs. Discover exact schemas before changing time or target. Preserve timezone, intended target and existing settings not requested to change. A real run can have effects; inspect its result. Do not create and immediately delete a reminder in the same turn.

For this panel’s full setup and operational contract, Read [the detailed procedure](references/operations.md).
