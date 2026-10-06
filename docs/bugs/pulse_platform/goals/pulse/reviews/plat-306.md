[← goals / pulse / reviews](index.md)

# PLAT-306 — reviewers should save reasoning once without mandatory Markdown bookkeeping

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | goals |
| Area | pulse/reviews |
| Summary | replaces mandatory review Markdown bookkeeping with an optional concise SQLite note on the existing result call. |

| Coordination | Value |
|---|---|
| Ticket state | Implemented and validated; deployment and live overhead measurement pending |
| Last synchronized | 2026-09-10 |

## Decision

The user explicitly requested removing mandatory Markdown maintenance so review
agents spend their time investigating and improving workflows. PLAT-137/138
introduced compact working files to retain reasoning across phases and compaction;
PLAT-163 kept SQLite authoritative, and PLAT-217/220 rejected free-text lifecycle
parsing and file-based backlog management. Preserve those integrity properties
without requiring two representations or a reporting-only turn.

## Implementation

- Extend existing `record_pulse_result` with optional `review_note`. The existing
  reason is the short conclusion; notes carry only new reasoning, limitations
  or future questions. No mandatory sections, new completion handshake, or new tool.
- Notes live in the workflow's SQLite database, keyed by workspace/module/run.
  The backend records their identity and timestamp. Completion and a supplied
  note commit together with the existing audit and finding dispositions.
  Independently recorded findings remain saved if a later completion fails.
- Optional `note_only=true, result=running` saves working context only for a long
  investigation. It requires an unresolved due module for the exact run, rejects
  mixed completion/disposition payloads, and never clears runtime recovery state.
  It is not prescribed after each phase. It replaces that run's working note.
- `get_pulse_state(view="review_notes", module=...)` returns the latest three
  notes/conclusions by default, with optional exact run and a bounded limit.
  Existing audit-only reviews return their recorded conclusion, not invented prose.
  Incomplete notes explicitly lack terminal completion; they may still be active.
- Pulse's report reader renders the same stored note, conclusion and evidence.
  Historical Markdown reports remain accessible. No file export or second report
  generation is required. API history retains up to 50 notes per module, so a busy
  Health module cannot hide the latest Strategy/Architecture note.
- Scheduled Technical/Architecture/Strategy/Drift prompts remove mandatory files,
  per-phase notebook updates and separate persistence-only work. Existing finding,
  decision, impact and verification tools keep their authority and safeguards.
- Runtime keeps incomplete-run tracking. New recovery entries need no file path;
  legacy checkpoint paths remain optional evidence pointers. No automatic claim
  of completion and no new recovery/reporting agent is introduced.

## Limits and rollout

This does not infer test coverage, goal progress or health from tool success.
Existing runtime logs remain separate; automatic semantic extraction of arbitrary
shell changes or all MCP side effects is not claimed. Notes are current per-run
working/final reasoning, not immutable versioned evidence (PLAT-047/089).

Old files are not deleted or bulk-migrated. New guidance applies to newly started
review runs after deployment; active sessions may retain their old instructions.
Measure actual reporting calls, repeated writes and validation retries on live
reviews before claiming an overhead reduction. No live production measurements
have been taken for this change.

## Validation

- Full server, guidance and workflow-executor Go package suites pass.
- Tests cover working-note versus terminal status, exact-run/module/workspace
  isolation, note preservation on retries, atomic rollback on note storage failure,
  no-file API rendering, legacy audit-only conclusions and legacy Markdown access.
- 13 frontend tests pass, including opening an incomplete SQLite note without any
  file request, full-screen reading, closing, and historical file retry behavior.
- Production frontend build and release asset checks pass. The existing bundle
  warning threshold is exceeded (971.08 kB gzip); the hard budget passes.
- Prompt tests reject the old mandatory checkpoint and persistence-phase phrases.

## Register notes

[PLAT-306](plat-306.md) replaces mandatory review Markdown
bookkeeping with an optional concise SQLite note on the existing result call.
The same note serves future reviews and the UI; old reports remain readable.
Runtime interruption tracking remains independent of agent-authored notes.
Deployment and measured live reporting overhead are pending.
