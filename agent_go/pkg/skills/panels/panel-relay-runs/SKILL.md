---
name: panel-relay-runs
description: Relay runs panel. Inspect published Relay calls, traces, output and failures.
metadata:
  panel-tools:
    relays:
    - list_relay_runs
    - get_relay_run
    - get_relay_releases
---

# Relay runs

Use get_relay_run and the published run readers actually offered by this chat; the external MCP exposes list_relay_runs and run_relay. Inspect real INPUT, returned JSON and relay_trace.json through the existing Relay result contract. Running the draft uses test_relay; publishing uses publish_relay. Do not start a new execution just to read an old one. Keep existing sharing and run authority; the Relay runtime is separate from Goal routes and Crew chats.
