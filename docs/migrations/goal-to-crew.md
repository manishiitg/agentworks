# Move one Goal into a Crew

This operator procedure belongs to the held unified-Crew refactor. It is not a startup migration or a request to migrate a running installation.

A selected standalone Goal becomes a goal and workflow inside the destination Crew. Create that Crew normally first, or choose an existing one. Other Goals are untouched. Several Goals can be converted into the same Crew in separate operations.

## Inspect first

Use the workspace directory and state directory for the **same installation**. The destination must have the current Crew private-storage layout.

```sh
python3 scripts/migrate-goal-to-crew.py \
  --docs /path/to/workspace-docs --state /path/to/state \
  --workflow source-folder --crew destination-crew-id
```

This reads files and SQLite databases without changing them. It reports the selected workflow, destination, file moves, chat mapping and counts. The workflow folder name can differ from its manifest ID; both are retained.

Resolve any reported access, input or data conflict before applying. In particular:

- Destination owner, owners and sharing audience must match. Old editors, Team policies, individually shared destination agents and paired/synced destination Crews need an explicit mapping first.
- An old enabled group batch must become one chosen input preset per entry. Function input schemas must map unambiguously to workflow inputs.
- Saved Python must use `agentworks_db` for managed database access. Code, reports or database objects directly using measurement tables must use the Crew measurement API instead.
- Existing named outcomes must not be collapsed into one goal; multiple such outcomes need their own mapping first.
- Source, goal or measurement ID collisions need resolution. Measurements are never silently merged or renumbered.
- Human transcripts need their original owner and personal conversation registry, including prior sessions. Orphan human chats require repair. Automated run/bot transcripts stay with their run history.

This is intentional inspection, not a claim that every historical configuration converts automatically.

## Apply with this installation stopped

Stop the installation's backend/workspace writers. Keep other installations running. Use a backend binary built from **this refactor**, which includes `server import-goal-chats`; it is needed when an old chat has a JSON archive but no display journal.

```sh
python3 scripts/migrate-goal-to-crew.py \
  --docs /path/to/workspace-docs --state /path/to/state \
  --workflow source-folder --crew destination-crew-id \
  --agent-binary /path/to/refactor-backend --apply
```

The converter makes a private backup before writing. It snapshots affected files and SQLite databases, including WAL contents and the conversation journal. It then:

1. Moves definitions to `.agentworks/workflows/<workflow-id>/` and authored code to `.agentworks/code/<workflow-id>/` in the Crew's project folder.
2. Creates a goal in the Crew and transfers its metric definitions and observations into the Crew ledger. Original observation IDs/timestamps remain; run references gain the workflow ID to avoid collisions. The source measurement tables are removed. Business tables stay where they are.
3. Maps saved human chats into distinct Crew tabs, retaining session/conversation IDs and each person's ownership. JSON-only chats are imported once through the existing dialogue projector. Existing display journals remain canonical. The next agent turn starts fresh with saved conversation context in its new Crew.
4. Keeps schedules, functions and webhooks on their existing identities and URLs, including enabled/disabled state. Each points at a saved Crew chat. Existing legacy entry/input converters operate only on this selected Goal.
5. Registers the trusted Crew/workflow binding and writes a completion receipt.

Runs, costs, business databases, reports, skills, knowledge and secret references remain in the workflow's managed runtime namespace. Existing reports are available from the Crew dashboard using the original workflow database scope. No dashboard SQL is copied into the parent database.

A completed conversion can be invoked again: it reports **already migrated**, without changing data. If an apply fails, keep that installation stopped and restore the affected files and database snapshots from the reported backup before retrying. `plan.json` and `metadata-paths.json` identify the affected paths. Remove only this conversion's new destinations/binding/receipt when restoring; this is an operator restore, not an automatic rollback mechanism.

## Check after restart

- Open the Crew's Goals panel: verify outcome, targets and saved evidence. Status starts Active; success is never inferred from old runs.
- Open imported chat tabs and the dashboard. Confirm their actual contents and the correct person/account.
- Run one real invocation through an existing function or webhook; inspect its outputs, run/cost records and the assigned chat's result.
- Check one saved timer with the configured coding program. Disabled timers stay disabled; an explicit manual trigger can test delivery. A failed provider turn is not a successful workflow test.
- Confirm unrelated Goals and the destination Crew's earlier goals/chats still work.

Old grouped run folders remain readable at their original paths. Their retired execution layout is not resumable: start a new run. Older standalone Goal code remains for Goals that have not moved yet; this converter does not remove the whole product at once.
