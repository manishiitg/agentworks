# One-off Team folder selection conversion (PLAT-746-1020)

Teams now share explicit chosen folders, not the entire Crew. There is no old-format runtime reader. Run this conversion after the path-identity conversion (976) and before starting the new server. Remove both one-off converters when every server and Mac has moved.

The Mac app runs both conversions in its existing pre-server-start hook. The folder conversion backs up each changed Team record and each affected `team-working/**/binding.json` next to the original before writing any changed Team. Bindings include the file baselines: their bytes, mount IDs and paths are kept unchanged. The existing identity converter has its own plain backup too.

For an offline server conversion, with the service stopped:

```sh
# Plan with the new binary: actual runtime owner/storage resolver and folder policy.
# This command starts no services and changes no state.
"$NEW_AGENT_BINARY" server team-folder-selection-plan > "$PLAN_FILE"
# Preview only; prints IDs, root branch, selected folder names/counts, never file contents.
node desktop/lib/teamFolderSelectionMigration.js --state-root "$STATE_ROOT" --docs-root "$DOCS_ROOT" --plan "$PLAN_FILE"
# Apply only after approval and after checking the preview.
node desktop/lib/teamFolderSelectionMigration.js --state-root "$STATE_ROOT" --docs-root "$DOCS_ROOT" --plan "$PLAN_FILE" --apply
```

Use the actual service state/doc roots. `--local` is only for a local desktop state directory, where owner-admitted registry roots refer to this computer. Do not use it on a server to guess Mac paths.

Whole-sharing records become the source Crew's current top-level ordinary directories. Root files, private state, personal memory, credentials and symlink directories are not selected. A remote/offline Mac source uses `team/` and a Team setup note asking its owner to choose folders. An empty source also uses `team/`. Previously selected-folder records retain their selections. Conversion chooses at most 20 folders, matching the server limit; omitted folders are counted and a Team note asks the person to review the selection. Store IDs, existing folder/path IDs, people and agent choices do not change. There is no nested `Share.whole` handling: scans found no such field in real bindings. Member Macs do not own an authoritative Team store; cached non-owner records have retired keys removed without inventing a folder selection, and the next server read supplies the selection. The local marker `member_of` and the owner registry distinguish those records.

Each converted record logs `[TEAM_FOLDER_SELECTION_MIGRATION] project=... from=whole|folders root=found|missing|remote|unchanged folders=N omitted=N outcome=converted|keys_removed`. Validation or backup failure prevents the new desktop server from starting. The converters run sequentially: if the second phase refuses, an already completed identity conversion remains backed up; do not claim an atomic cutover.

What changes: later root folders are no longer shared automatically; add them explicitly through Team settings or the same chat/MCP folder selection. At least one folder is required. Only the owner may remove an existing shared folder; members may add.

What could break: a server or Mac still using the old format cannot exchange Whole-sharing metadata with the new reader. Check `root=found` in the preview for every existing server Crew; investigate missing/remote branches before applying. Convert the approved server state before testing the new Mac release against it. Client-server conversion and Mac release need their own owner approval. This document does not authorize either.
