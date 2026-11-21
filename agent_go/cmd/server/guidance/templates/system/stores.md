# Files, knowledge and durable data

Read `panel-data` and the relevant reference before changing a store. Inspect the
Crew's current instructions, selected skills, permitted Brain notes, table
contracts and relevant run evidence rather than inventing a parallel store.

- Standing rules belong in the Crew's instructions; verified project facts and
  decisions belong in its memory. Reusable procedures belong in a narrowly
  scoped `skills/<name>/SKILL.md`, with long detail in supporting files. Change
  a reusable procedure when the person asks; do not save raw chats or secrets.
- Shared Brain content uses Brain tools and the project's current mode and
  folder roles. Do not shell-read the service's data directory or revive retired
  local `knowledgebase/` paths, aliases or upgrade recipes. Existing knowledge
  readers may return historical content only where the live tool permits it;
  a missing or refused source is not permission to create a replacement.
- Structured results and cross-run state use the managed database. Inspect with
  `database action=describe/query`; mutate through its admitted actions. Parent
  workflow Python may use `agentworks_db`. Native agents use database tools or
  return an artifact to parent Python; their helper scripts cannot import the
  parent-only module. This grants no access to an arbitrary external database.
- Never open, copy, stage or modify live SQLite/WAL/SHM files with shell or a
  library, even for an additive change. Use managed schema operations and
  `database action=snapshot` for backup. A refused database call stays refused;
  do not rebuild the same request by hand or invent a raw database fallback.
- Declare table primary keys, writers, consumers and upsert rules in the
  existing `db/README.md`. Prefer parameterized upserts that preserve unrelated
  rows; never drop/recreate or replace an entire shared table to refresh data.
  Backend-owned concern, human-input, audit and schema tables are not workflow
  output tables. Historical evaluation rows are not new evaluation targets.
- Durable files use the Crew's permitted artifact paths with metadata pointing
  to them where needed. Keep temporary run outputs separate from persistent
  records. Pass ordinary Python values between functions rather than inventing
  old plan `context_output`/dependency contracts. A persistence failure remains
  a failure even when analysis succeeded.

Parent Python's `agentworks.files.read` uses the authorized own-Crew resolver;
it is not a general Brain reader. Native agents use file tools. Report scripts
have their own permissions and do not inherit parent file helpers or durable
workflow data access. Dashboards use the current managed query/get/run APIs;
read `panel-dashboard` before writing one.

For caller-specific project lists, `{"$current_user_id":true}` in managed SQL
parameters binds the requesting person. Preserve creator attribution on updates.
This is a list filter, not a new private workspace or row permission.
