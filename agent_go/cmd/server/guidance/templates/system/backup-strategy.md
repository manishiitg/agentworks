# Back up the Crew's work

Inspect the existing backup configuration and destination before changing it.
Use `panel-dashboard`'s Backup reference and the current file/database tools.
A Crew and its existing Git repository are the working unit; do not create a
separate repository for every task path in its Python workflow. Reuse the
person's established repository, layout and approved destination.

## Scope and credentials

Back up authorized source, typed definitions, ordinary helper files, selected
project instructions and artifacts. Keep credentials, `.env`, OAuth tokens,
private CLI sign-ins, key files and secret values out of Git and generic object
storage, even for a private repository. A private remote does not authorize
copying secrets. Use only a supported, explicitly approved secret recovery flow;
never stage plaintext credentials. Report any unresolved recovery dependency by
name only. Private chats and personal information need their own approved scope.

A missing destination is a setup choice: ask which account, repository/location
and visibility before creating one. Verify visibility and the actual remote;
never infer privacy from its name. Keep approved non-secret backup work moving
when a separate secret-recovery dependency is missing.

## Local checkpoint and database

A local Git commit is a checkpoint, not an off-device backup. Never put a bundle,
archive or nested repository snapshot inside its own source tree: tracking each
new snapshot can recursively include earlier snapshots. If an archive is
explicitly requested, resolve an absolute destination outside the source under
the permitted backup root and verify the artifact before reporting success.

For database coverage, use `database action=snapshot` and its returned managed
snapshot/checksum. Never read, copy, stage, unlock or modify live SQLite/WAL/SHM
files. If a repository already tracks them, remove only their index entries and
ignore the runtime files; do not delete the live database. A failed snapshot means
database backup is partial or failed, not healthy. Restore only the requested
revision/scope through the supported operator procedure; do not bypass a file
refusal to overwrite a running database.

## Large files and evidence

Exclude regenerable caches and installed dependencies from new backups. Keep
large binaries, logs and explicitly included chat archives in an approved
large-file destination rather than duplicating them in Git history. Preserve
existing recovery content; exclusion is not authorization to delete it.

Keep settings separate from results: `backup/config.json` records approved
scope/destinations, `backup/status.json` records each actual attempt, including
partial failures. Preserve the existing schema and store names only, never secret
values. A backup is not automatic merely because a workflow finished; use the
requested backup action or existing schedule.

Verify the commit and remote revision or destination receipt; check the actual
snapshot/checksum and round trip where appropriate. A clean tree alone proves
no remote backup. Never reset unrelated work, force-push shared history or claim
that another destination succeeded because one did. Report what was backed up,
where, when and what remains unavailable, without disclosing private values.
