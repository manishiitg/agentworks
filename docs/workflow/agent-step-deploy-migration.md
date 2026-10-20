# Agent-step deployment batch

Contract v1.0.47 uses `agent` steps and `items`. For a reviewed offline deploy,
the new server binary offers:

```sh
agent-server server migrate-workflow-agent-steps /path/to/workspace-docs
agent-server server migrate-workflow-agent-steps /path/to/workspace-docs --apply --server-drained
```

The default is dry-run. Applying requires every execution and document writer to
be stopped, including workspace services and MCP/CLI authoring. Run as the
service user that owns the documents. Earlier contract upgrades through 1.0.46
and code layout 1 must already be complete. Never set the marker manually.

The batch preflights every live native plan before writing, uses the same Go
migration and v1.0.47 stamp gate as Builder, preserves unknown fields, saves
original plan/manifest backups, and writes audit receipts before the final
manifest stamp and contract-history entry. A canonical 1.0.47 workflow is a
no-op. Historical directories and Relay manifests are excluded. On failure,
keep the server stopped, reconcile the named plan, and rerun forward.

Use the same drained window for `scripts/migrate_scripted_step_types.py` when
normalizing remaining `regular` step names. If a retired `human_input` node is
found, ask Builder to replace it first; see [human input retirement](human-input-step-retirement.md).
Repeat dry-runs after applying and verify zero changes before starting the new
process. Server inventories, paths and activation instructions belong in the
private deployment runbook.
