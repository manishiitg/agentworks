---
name: panel-pulse
description: Pulse panel in Goals and Crews. Inspect Pulse, change whether it runs or start a requested check.
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

# Pulse

Use manage_pulse status for actual state and run_now only when requested. update_workflow_config pulse_enabled/pulse_outward_actions or settings changes the saved choice. Load builder-reference/references/working-with-pulse.md before interacting with Pulse; ask_pulse talks through the existing channel. OFF starts no Pulse activity. Preserve existing permissions, private conversation boundaries and the person’s paused choices. Verify saved state and any actual run separately.

Goals & metrics shows goals, measurements and evidence. Use Talk to Pulse to open the full continuing chat; there is no separate activity transcript in the panel. Workflow playbooks, Publish, Notify and Backup are in Setup. Pending decisions stay in the Goal’s Decisions panel.
