[← goals / step-execution](index.md)

# PLAT-221 — workflows have a managed, auditable route to change their own SQLite schema

| Field | Value |
|---|---|
| State | closed |
| Priority | P0 |
| Product | goals |
| Area | step-execution |
| Summary | P0 — LinkedIn `PUL-B995BF46` / `PUL-3BD9F422` (per the |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented and reachable; user-scoped Work path regression fixed and deployed; direct migration retry pending` |
| Last synchronized | `2026-09-17` |

- **Priority:** P0 — LinkedIn `PUL-B995BF46` / `PUL-3BD9F422` (per the
  [2026-08-29 triage audit](../../../../audits/pulse-platform-triage-2026-08-29.md)).
- **Owner:** workflow-owned SQLite schema evolution.
- **Related:** [`pulse_fixer_sqlite_readonly_wal_and_schema_guessing.md`](../../../pulse_fixer_sqlite_readonly_wal_and_schema_guessing.md),
  which shipped `query_workflow_db`/`mutate_workflow_db` (2026-08-01/02) and
  explicitly deferred schema changes: *"Schema migrations are internal, not a
  third agent tool... belong to the builder/harness version-upgrade path"* —
  a path that was never actually built.

## Problem

Neither `query_workflow_db` (read-only) nor `mutate_workflow_db`
(INSERT/UPDATE/DELETE only, fail-closed on DDL) can create or change a
workflow's own tables, and raw `sqlite3` shell access to `db.sqlite`/`-wal`/
`-shm` is hard-blocked for every managed agentic session. An agent's only
sanctioned action when a step needs new schema is to author a migration
`.sql` file under `db/migrations/` (ordinary file access — not blocked) and
then stop, because nothing is authorized to execute it. This is exactly what
happened for LinkedIn: `db/migrations/2026-08-06-action-outcome-measurement.sql`
was written correctly on 2026-08-06 and had no way to run.

A backend primitive for exactly this already existed
(`POST /api/db/initialize` → `workspace/handlers/query.go:InitializeWorkflowDB`,
`agent_go/pkg/workspace/initialize_workflow_db.go`), but its own doc comment
said *"normal agents do not receive this capability as a tool"* — it was used
by exactly one caller, Video Studio's product runtime, with a fixed,
compiled-in migration list. It was never wired to anything a workflow's own
Fixer or Builder session could reach.

## Fix

**New tool: `apply_workflow_db_migration(migration_file)`**
(`agent_go/cmd/server/virtual-tools/workflow_db_tools.go`)

- Takes a bare filename only (`safeMigrationFileName`, no path separators
  representable at all); resolves it server-side to that session's own
  `Workflow/<name>/db/migrations/<file>`, the same way `query_workflow_db`/
  `mutate_workflow_db` resolve `db/db.sqlite` — the model never supplies a
  path. Never accepts inline SQL.
- Gated by the same fail-closed trust boundary as `mutate_workflow_db`:
  requires the session's `WORKFLOW_DB_ACCESS=read-write` exactly. Schema
  migration authority is not a separate role — Pulse Fixer and the main
  Builder session already share this session/trust boundary in the current
  architecture, and neither has any legitimate reason to need it beyond
  read-write.
- Reads the file through the existing FolderGuard-checked
  `ReadWorkspaceFile` client call, splits it into individual statements
  (quote/comment-aware, so a `;` inside a string literal doesn't create a
  spurious split), drops the file's own `BEGIN`/`COMMIT` transaction
  envelope (the backend already wraps every migration in one transaction),
  and validates each remaining statement against the same allow-listed
  shapes the backend enforces — failing closed with a clear per-statement
  error before the HTTP round trip if something doesn't match.
- Calls `client.InitializeWorkflowDB`, which does the actual work.

**Expanded `InitializeWorkflowDB` allow-list**
(`workspace/handlers/query.go`)

Widened from CREATE-only to the full schema-evolution set a workflow
actually needs, while keeping the two genuinely dangerous statement kinds
out entirely:

- `CREATE TABLE/INDEX IF NOT EXISTS` — idempotent, unchanged from before.
- `DROP TABLE/INDEX IF EXISTS` — idempotent (a bare `DROP TABLE` without
  `IF EXISTS` is rejected, so retrying a migration is always still safe).
- `ALTER TABLE ... RENAME TO / RENAME COLUMN ... TO ... / ADD COLUMN /
  DROP COLUMN` — SQLite has no idempotent form for `ALTER`, so a repeated
  `ALTER` fails loudly on retry instead of silently no-op'ing; that failure
  is safe, just not automatically idempotent.
- **`PRAGMA` and `ATTACH` remain permanently out of scope**, in any file,
  even after this change. `PRAGMA` can change database-wide behavior other
  concurrent readers/writers depend on (journal mode, foreign keys); `ATTACH`
  opens an arbitrary filesystem path entirely outside FolderGuard's
  authorization. Neither has a legitimate migration use case that isn't
  better served by one of the allow-listed shapes above.

**Automatic pre-migration backup for destructive statements.** There is no
human-approval gate on this route. Any statement that can remove an existing
table, column, or the name a caller resolves it by (`DROP`, `RENAME`,
`DROP COLUMN` — never `ADD COLUMN`, which can only add data) triggers a
`VACUUM INTO` snapshot of the live database *before* the migration runs,
written under `db/migrations/.backups/<nanosecond-timestamp>-pre-migration.sqlite`.
The response's `backup_path` is the recovery point if a migration turns out
wrong. Purely additive/idempotent-create migrations never pay this cost.

## Verification

```text
go test ./handlers/...                                    (workspace module)
go test ./cmd/server/... ./cmd/server/virtual-tools/...    (agent_go module)
```

New coverage: idempotent `DROP TABLE IF EXISTS` (including safe re-apply),
a destructive `ALTER TABLE DROP COLUMN` proving the pre-migration backup is
written and the dropped data is recoverable from it, `ADD COLUMN` proving no
backup is taken for the purely-additive case, `RENAME TO`/`RENAME COLUMN`,
`PRAGMA`/`ATTACH` staying rejected, a fresh-database destructive migration
needing no backup, and — through the production stdio MCP bridge, no
executor called directly —
`TestApplyWorkflowDBMigrationThroughMCPBridge`: an agent-authored migration
file is applied, the created table is visible to a fresh direct connection
to the live database (not just the connection the handler used), re-applying
is a safe no-op, and the migrated columns are visible through
`query_workflow_db describe`.

A real bug surfaced by the tests themselves during development: the first
backup-path implementation used second-resolution timestamps, so two
destructive migrations applied within the same second collided on the
backup filename and `VACUUM INTO` refused to overwrite it. Fixed with a
nanosecond-resolution name, matching the same collision-avoidance pattern
already used elsewhere in this codebase for exactly this reason
(`agent_browser_snapshot_<nanos>.txt`).

## Reverify

No live agent turn has called this tool yet through the deployed server —
the running dev server (`+dirty`, pre-existing before this session) was not
restarted as part of this work, since restarting a server actively executing
scheduled Upwork/LinkedIn/Sales Outreach runs is outside this ticket's scope
and needs its own explicit go-ahead. Deploy, then run one Pulse Fixer turn
that authors and applies a real migration end to end.

## Notes on the two originating findings

- **LinkedIn `PUL-B995BF46`** (the ready `2026-08-06-action-outcome-measurement.sql`
  migration) turned out to be **already resolved independently of this
  ticket** — `action_outcome_bindings` and `matched_action_outcome_comparisons`
  already exist in the live `Workflow/linkedin/db/db.sqlite` with real
  producer data dated as early as 2026-08-21, matching the migration file's
  exact schema. The `pulse_finding_details` row was never updated past its
  original `2026-08-11` filing, so the finding record itself is stale — the
  same "real when filed, already fixed, never re-verified" pattern as
  PLAT-191/195/201/202/210/212/213. How the tables were actually created is
  not established (not through this new tool, and not through any ledger
  this ticket adds); reclassify/close the workflow finding on this evidence.
- **LinkedIn `PUL-3BD9F422`** (immutable image-batch identity) is confirmed
  still genuinely open: `post_approval` still has only a single `image_path`
  column and `image_assets` still has no ordered batch/draft-binding
  identity. This is the concrete beneficiary of PLAT-221 going forward — its
  migration SQL has not been designed yet and is deliberately left as a
  separate follow-up, not bundled into this ticket. **Do not mark this
  ticket, or PUL-3BD9F422, "done" until that migration exists and applies.**

## Code review follow-up (2026-08-29)

An independent review of this ticket's original state caught four real
gaps, all fixed:

1. **P0 — the tool was registered and tested, but unreachable by any real
   agent.** `apply_workflow_db_migration` was added to
   `CreateWorkflowDBToolRegistry` (so it reaches the base tool pool via
   `tool_setup.go`) and to `WorkflowDBToolNames()`, but three separate,
   independent per-agent-type allowlists that gate what a session can
   actually call still only listed `query_workflow_db`/`mutate_workflow_db`
   by name: `prepareCustomTools` (ordinary agentic workflow steps,
   `controller_agent_factory.go`), the todo-task orchestrator's DB-tool
   ensure-list (same file), and `GetToolsForWorkshopMode`
   (`interactive_workshop_manager.go` — the path Pulse Fixer and the main
   Builder session both run through). The original E2E test injected its own
   tool schema directly into the bridge, which exercises the executor but
   never touches any of these three allowlists — so it passed while the tool
   remained genuinely unreachable in production. This plausibly explains why
   `PUL-3BD9F422` was never applied even after this ticket shipped: no real
   session could have called the tool that would apply it. Fixed by adding
   `apply_workflow_db_migration` to all three gates (gated the same way as
   `mutate_workflow_db` — read-write only), plus the "stores" guidance's
   tool-trigger list. `TestEveryRegisteredWorkshopToolIsAllowedInSomeMode`
   (a pre-existing systemic test) was extended to also scan
   `WorkflowDBToolNames()`/`WorkflowCostsToolNames()`, since its original AST
   scan only covered `RegisterCustomTool` calls and structurally could not
   see DB-registry-sourced tools at all — this is exactly why it didn't
   catch the gap the first time, and now can't miss a future one of the same
   shape. `TestPrepareCustomToolsMaterializesDBCapabilityFromDBAccess` was
   extended with a direct behavioral assertion for the same reason.
2. **P1 — destructive backups had no retention, and nothing durably
   recorded what ran.** Every `VACUUM INTO` snapshot before a destructive
   migration was a complete, unbounded-lifetime database copy; a log line
   was the only record of what migration applied, when, by whom, or whether
   it was destructive. Added `pruneMigrationBackups` (keeps the most recent
   `migrationBackupRetentionCount = 20` snapshots by modification time,
   best-effort, never blocks a successful migration) and a durable
   `schema_migration_log` table — created and written inside the *same*
   transaction as the migration's own DDL, so a ledger row exists if and
   only if that migration actually committed. Records migration filename, a
   SHA-256 hash of the applied statements (not the raw SQL — that already
   lives in the caller's own `db/migrations/*.sql` file), whether it was
   destructive, its backup path, who applied it, and when. `migration_file`
   threads end to end: tool executor → HTTP request
   (`InitializeDatabaseRequest.MigrationFile`) → ledger row, proven by a new
   assertion in the production bridge E2E test, not just a unit test.
   Documented as a new backend-owned table in `stores.md`.
3. **P2 — a leading SQL comment broke an otherwise-valid migration
   statement.** `-- Add outcome table\nCREATE TABLE IF NOT EXISTS ...` was
   rejected: neither this tool's client-side check nor the workspace
   service's own validator stripped comments before matching the anchored
   `^\s*CREATE...` shape. Added `stripLeadingSQLComments` (mirroring the
   workspace service's own `stripSQLCommentsAndSpace`, a separate Go module
   so duplicated rather than imported) so a naturally-commented migration —
   an ordinary human/agent authoring style — is recognized correctly. New
   unit tests cover multiple comment placements and confirm genuinely
   disallowed statements are still rejected, commented or not.
4. A fifth finding (wildcard multi-match `value_type` checks only
   inspecting the first result) was about PLAT-229's fix, not this ticket's
   own code; see [PLAT-229](../evaluation/plat-229.md))'s follow-up note instead.

Verification: `go test ./pkg/orchestrator/... ./cmd/server/...` (agent_go)
and `go test ./...` (workspace) both pass clean after all four fixes.

## Independent policy review (2026-08-29)

The follow-up implementation was reviewed against the product's intended
authority model. The migration tool is reachable by ordinary read-write
workflow agents as well as Builder/Pulse sessions, and destructive schema
operations plus the migration ledger remain agent-accessible. The user
explicitly confirmed that this broad authority is intentional: agents are
trusted and should receive more access rather than narrowly partitioned
schema privileges. Under that policy, the broad allowlist and agent-editable
ledger are accepted design choices, not outstanding defects.

Focused workspace-handler and workflow-orchestrator test suites passed during
the review. The separate LinkedIn `PUL-3BD9F422` migration remains undesigned,
as already recorded above; that workflow work is not evidence that the
platform migration route itself is unreachable.

## Main Builder session bypass follow-up (2026-08-31)

A live RTS Latency Builder session exposed a gap in the policy above. Session
`65043afa-d0d3-4d9a-af1e-eb3451afa4e6` successfully discovered and called
`apply_workflow_db_migration`, but the tool correctly denied it because the
long-lived main session had no `WORKFLOW_DB_ACCESS` capability. The model
misreported that permission denial as "the schema migration service is
unavailable" and then ran the migration directly with `sqlite3`, followed by
a second direct `ALTER TABLE`. Both raw writes succeeded.

This was not a migration-service failure. Two platform contracts had drifted:
the main Workflow Builder guard was rebuilt in two `server.go` setup/restore
paths with broad workflow-folder write access, but neither path called
`configureWorkflowDBSession`; and the rendered `stores.md` guidance still
explicitly told the Builder it could use `sqlite3 db/db.sqlite` directly and
that FolderGuard allowed it. Consequently:

- the dedicated migration tool saw `db_access=""` and failed closed;
- raw `db.sqlite`, `db.sqlite-wal`, and `db.sqlite-shm` were not hard-blocked;
- the direct migration bypassed the managed transaction and
  `schema_migration_log` audit path.

The fix exports one narrow `ConfigureManagedWorkflowDBSession` entry point and
calls it immediately after both main Builder folder-guard resets. Normal
Builder sessions receive managed read-write authority; read-only product users
receive managed read authority. Both shapes hard-block raw SQLite plus WAL/SHM
sidecars, while leaving `db/migrations/`, `db/README.md`, and `db/assets/`
available. Schema evolution therefore has exactly one execution path again:
author `db/migrations/<file>.sql`, then call
`apply_workflow_db_migration(migration_file)`.

The stale direct-SQLite Builder instruction is removed. The store contract now
also says that a managed-tool permission denial is terminal for that action:
report the exact missing capability instead of relabeling the service as
unavailable or attempting a raw SQLite fallback.

Regression coverage proves the broad Builder folder grant cannot read or write
the database or either sidecar, adjacent DB artifacts remain writable, the
planning write deny survives DB setup, read-only users stay fail-closed, and
both workflow-session setup/restore branches install the boundary.

## User-scoped Work path follow-up (2026-09-17)

A Crew/Work project reproduced `apply_workflow_db_migration` failure even with
`WORKFLOW_DB_ACCESS=read-write` and a valid migration file. The migration tool
correctly converted the physical project root
`_users/<user>/Chats/Work/projects/<project>` into the workspace API's public
`Chats/Work/projects/<project>` form. Its `ReadWorkspaceFile` client then
compared that public path against the still-physical session Folder Guard and
denied the read before the workspace API could restore the authenticated user
prefix.

This is the same two-namespace contract already recorded by
[PLAT-170](plat-170.md) and the Crew path-identity follow-ups in
[PLAT-324](../../crew/chat-reliability/plat-324.md): trusted runtimes and Folder Guards
use physical `_users/<id>/...` paths, while authenticated workspace APIs use
public user-relative paths plus `X-User-ID`. Earlier fixes normalized individual
call sites in one direction. They did not make the shared Go workspace client's
Folder Guard comparison understand both representations, so each new Work
surface could repeat the mismatch.

The workspace client now normalizes public per-user paths and all effective
guard paths to the same physical user-scoped representation before checking
read/write and blocked-path policy. The HTTP request remains public and the
workspace API still owns tenant selection; no folder moved and the API's
rejection of caller-selected `_users/` database paths remains unchanged.

Regression coverage uses the real MCP migration bridge with a physical
`_users/<user>/Chats/Work/...` project and public `Chats/Work/...` API path,
then verifies the migrated table through the live database. Separate checks
prove blocked subpaths, another project, and another user remain denied.

Implementation commit: `82821a8a7` (`Fix user-scoped workflow migration path checks`).
RTS release: `82821a8-20260917153631`. A direct retry of the original Work
migration remains the final runtime acceptance check.
