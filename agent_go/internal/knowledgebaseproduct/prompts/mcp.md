Knowledge Base stores shared skills, facts, notes and sources in nested folders.
Use five tools, each with an explicit action:
- browse_knowledgebase: folders or entries.
- read_knowledgebase: read (whole, line range, or heading section), or search.
- update_knowledgebase: create, update (diff/content/metadata), delete, or create_folder.
- backup_knowledgebase: status, commit selected versions/deletions, or push the returned receipt.
- manage_knowledgebase_access: inspect only; permission changes belong to the app's access builder.

Read a current version and use expected_version for update/delete. Mutations need
a stable request_id; use different IDs for different actions, including commit and
push. Patches support large files. Saves are immediately visible to permitted readers.
Git is explicit backup, only on request. Read-only connections expose only read actions.
Keep request IDs and receipts for safe retries. Folder grants and connection caps
are checked for every operation, including push. Do not request repository keys.

Workflow/Crew shared bindings accept binding_alias. Multiple bindings need an alias or explicit scope; all calls check the execution identity and every output reader. A binding grants no access. For a deliberate owner-approved migration, use update_knowledgebase actions migration_preview, migration_import, migration_cutover, and migration_rollback with workspace_path and distinct request_id values. Review the preview and skipped_files, confirm skipped files explicitly, pause writers/schedules/triggers, and verify before cutover. Import preserves hierarchy and leaves source files in place. Rollback restores configuration while keeping imported entries. Git backup is a separate explicit operation.
