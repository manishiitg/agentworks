Brain stores shared skills, facts, notes, sources and files of any type (images, decks, spreadsheets; not programs) in nested folders.
Use six tools, each with an explicit action:
- brain_browse: folders or entries.
- brain_read: read (whole, line range, or heading section), or search.
- brain_update: create, update (diff/content/metadata), delete, or create_folder.
- brain_skills: list company skills, get one skill's files, or publish a skill package (SKILL.md plus references/, scripts/, assets) into a folder; publishing replaces the previous files, needs Editor, and scripts need Owner.
- brain_access: inspect for content connections. Writable unrestricted external connections may list/grant/revoke subject to live folder Owner checks. Use list to resolve an existing user's identity_id and inspect to obtain expected_acl_version; grant Reader for read, Editor for read/write, or Owner for access management. Grants inherit to descendant folders. Service-account changes and configure_backup (an existing GitHub/Git repository's HTTPS remote_url, username, optional pat, optional branch, stable request_id) require an administrator. The optional PAT is encrypted in KB private storage, never returned and requires no Vault integration. Omit pat to keep it or set pat to an empty string to remove it. Setup does not create the repository, commit/push or change an existing destination. SSH URLs require deployment configuration and cannot be set through MCP or app setup. External changes apply directly using expected_acl_version and stable request_id. App access chat still requires confirmation.

Read a current version and use expected_version for update/delete. Mutations need
a stable request_id; use different IDs for different actions, including commit and
push. Patches support large text files; send other files with content_base64 and read them back whole. Saves are immediately visible to permitted readers.
Git backup is run with git in Brain's folder by the people who own the whole Brain, not through these tools. Read-only connections expose only read actions.
Keep request IDs and receipts for safe retries. Folder grants and connection caps
are checked for every operation, including push. Do not request repository keys.

Workflows and Crews use Brain with their owner's folder roles, as Off, Read or Read & write; all calls check the execution identity and every output reader. For a deliberate owner-approved migration, use brain_update actions migration_preview, migration_import, migration_cutover, and migration_rollback with workspace_path and distinct request_id values. Review the preview and skipped_files, confirm skipped files explicitly, pause writers/schedules/triggers, and verify before cutover. Import preserves hierarchy and leaves source files in place. Rollback restores configuration while keeping imported entries. Git backup is a separate explicit operation.

To set a workflow/Crew's Brain access, an unrestricted KB write connection also needs project authoring authority (Workflow Builder or Crew write/read scopes) for that exact project. Use brain_access action=inspect_project with workspace_path, then set_project_access with mode (off, read, write), expected_manifest_version and request_id. This never grants permissions. Which folders a step reads and writes is written in its description. Steps receive content tools according to knowledgebase_access; read/none steps cannot write, and steps cannot change project access.
