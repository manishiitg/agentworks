---
name: panel-relay-code
description: Relay code and publishing panel. Edit, test, publish or inspect a Relay draft and immutable versions.
metadata:
  panel-tools:
    relays:
    - get_workflow_config
    - update_workflow_config
    - test_relay
    - get_relay_run
    - get_relay_releases
    - publish_relay
    - execute_shell_command
    - diff_patch_workspace_file
  external-tools:
  - test_relay
  - publish_relay
  - get_relay_releases
---

# Relay code and publishing

Load relay-builder before editing relay.py or its configuration. Use test_relay with the person’s supplied INPUT and get_relay_run for real results. Use get_relay_releases and publish_relay only for requested publication. The graph is derived from the existing Python source; do not create a Goal plan or add Goal steps. A published API version and a dashboard are separate artifacts. Callers keep their selected immutable version while later Builder edits remain a draft.
