Brain stores shared skills, facts, notes and sources in nested folders.
Use five tools, each with an explicit action:
- browse_knowledgebase: folders or entries.
- read_knowledgebase: read (whole, line range, or heading section), or search.
- update_knowledgebase: create, update (diff/content/metadata), delete, or create_folder.
- backup_knowledgebase: status, commit selected versions/deletions, or push the returned receipt.
- manage_knowledgebase_access: inspect for content connections. Writable unrestricted external connections may list/grant/revoke subject to live folder Owner checks. Use list to resolve an existing user's identity_id and inspect to obtain expected_acl_version; grant Reader for read, Editor for read/write, or Owner for access management. Grants inherit to descendant folders. Service-account changes and configure_backup (an existing GitHub/Git repository's HTTPS remote_url, username, optional pat, optional branch, stable request_id) require an administrator. The optional PAT is encrypted in KB private storage, never returned and requires no Vault integration. Omit pat to keep it or set pat to an empty string to remove it. Setup does not create the repository, commit/push or change an existing destination. SSH URLs remain supported without a PAT. External changes apply directly using expected_acl_version and stable request_id. App access chat still requires confirmation.

Read a current version and use expected_version for update/delete. Mutations need
a stable request_id; use different IDs for different actions, including commit and
push. Patches support large files. Saves are immediately visible to permitted readers.
Git is explicit backup, only on request. Read-only connections expose only read actions.
Keep request IDs and receipts for safe retries. Folder grants and connection caps
are checked for every operation, including push. Do not request repository keys.

Workflow/Crew shared bindings accept binding_alias. Multiple bindings need an alias or explicit scope; all calls check the execution identity and every output reader. A binding grants no access. For a deliberate owner-approved migration, use update_knowledgebase actions migration_preview, migration_import, migration_cutover, and migration_rollback with workspace_path and distinct request_id values. Review the preview and skipped_files, confirm skipped files explicitly, pause writers/schedules/triggers, and verify before cutover. Import preserves hierarchy and leaves source files in place. Rollback restores configuration while keeping imported entries. Git backup is a separate explicit operation.

To attach a folder to a workflow/Crew, an unrestricted KB write connection also needs project authoring authority (Workflow Builder or Crew write/read scopes) for that exact project. Use manage_knowledgebase_access action=inspect_project with workspace_path, then bind_project with folder_id, alias, access (read/write), expected_manifest_version and request_id. Unbind uses alias and the same version/request fields. The caller must own the project and the output audience must already have Reader access. This never grants permissions implicitly. Workflow Builder and Attached folders UI use the same binding checks. Steps receive content tools according to knowledgebase_access; read/none steps cannot write, and steps cannot bind folders.
