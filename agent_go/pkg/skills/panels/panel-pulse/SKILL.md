---
name: panel-pulse
description: Autopilot panel in Goals and Crews. Inspect Autopilot, change whether it runs or start a requested check.
metadata:
  panel-tools:
    agentworks:
    - manage_pulse
    - ask_pulse
    - update_workflow_config
    - settings
  external-tools:
  - manage_pulse
---

# Autopilot

Use manage_pulse status for actual state and run_now only when requested. update_workflow_config pulse_enabled/pulse_outward_actions or settings changes the saved choice. Autonomy (pulse_outward_actions) has three levels: ask (asks before anything goes out and before plan changes), allow (posts and messages go out within existing limits; plan changes ask) and decide (also changes the plan, steps and schedules itself, recording why and how to undo). Spending, security, the goal (soul.md), new kinds of outreach and owner-paused schedules always ask the owner; rules in soul.md can ask for more. Load builder-reference/references/working-with-pulse.md before interacting with Autopilot; ask_pulse talks through the existing channel. OFF starts no Autopilot activity. Preserve existing permissions, private conversation boundaries and the person’s paused choices. Verify saved state and any actual run separately.

Goals & metrics shows goals, measurements and evidence. Use Talk to Autopilot to open the full continuing chat; there is no separate activity transcript in the panel. Workflow playbooks, Publish, Notify and Backup are in Setup. Pending decisions stay in the Goal’s Decisions panel.
