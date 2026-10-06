[← brain / curation](index.md)

# PLAT-618: Brain curator: /organize, /dedupe and scheduled tidy-ups

| Field | Value |
|---|---|
| State | in progress |
| Priority | P2 |
| Product | brain |
| Area | curation |
| Summary | Brain curator: slash commands and schedules that deduplicate and organize Brain folders, run as the person who set them up |

## What happened

## Fix

## Left

## Why

Owner, 2026-10-06: let Brain writers/admins set up schedules "like other products", for example "a schedule to deduplicate or organize the brain better", and "we can also have slash command for this".

## Design (proposed, not built)

- One curator task, run two ways: now via a slash command, or on a schedule.
  - `/organize [folder]`: duplicates, misplaced notes, broken links and indexes, stale notes; reports what changed.
  - `/dedupe [folder]`: duplicates only.
  - `/schedule-organize [folder] [when]`: the recurring version ("every Monday").
- Reuse: product slash commands are prompt files (as Crew's `commands/*.md`); schedules reuse product schedules for singleton profiles (isolated conversation, `resolveIsolatedProductBinding`), listed with Crew/Code schedules.
- The Brain chat today has only access, backup and secret tools. The curator needs the content tools (`brain_browse`, `brain_read`, `brain_update`) limited to the folders the person may edit.
- Runs as the person who set it up, so it can never touch folders they cannot edit; access changes are never part of a scheduled run.
- Safety: Git backup commit before it starts; every change records its reason; deletes happen only as merges into a kept note; a summary of changes is posted; everything is undoable from version history.

## Built (part A, 2026-10-06)

- `/organize [folder, mode or layout notes]` ships with the Brain product (owner: "remove dedupe, organize should have dedupe"; it merges duplicates) (`internal/knowledgebaseproduct/commands/`). /organize follows a folder's own `readme.md` structure or a layout the person names, else a mode (by products, the default; by teams; by entities). Timeline and an Entities index are always kept as views (owner: "organize in different ways: entities, timeline, teams and products"; "users can pick their own also"). The Brain chat's slash menu loads it from the profile, as Crew does.
- The Brain chat now has `brain_browse`, `brain_read`, `brain_update` and `brain_skills`. They act with the person's own folder roles, like their MCP connection: `isBrainChatWorkspace` recognises the person's own `Chats/Knowledgebase` session from server state, and the runtime policy treats it like a session-less MCP call. Another person's Brain chat folder gets no tools. Prompt: the chat edits content only when asked (for example by a command).
- Applied directly when the person runs a command (an explicit ask); the commands carry the safety rules (backup status first, merges are the only deletes, report every moved path).
- Tests: `TestBrainChatCuratesWithThePersonsOwnRoles`; the Brain builder gate test now admits the content tools and still refuses shell, files, database and browser.

## Next (part B)

Schedules that run /organize weekly or every N days.

## Open (owner)

1. Apply changes directly with the summary, or propose first for the first few runs?
2. Who may schedule it: Editors, or only folder Owners and admins (merging deletes the other copies)?

## Part B done (schedules)

- Built-in Brain schedule `organize` ("Organize Brain"): isolated (its own history), weekly (`cadence_hours: 168`), **off by default**; each person turns it on for themselves and it runs as them, so it only changes folders they can edit.
- Per-person cadence: `cadence_hours` override in the person's schedule state (1h to 90 days); `POST /api/scheduler/jobs/{id}/cadence`; the Brain chat's `brain_schedule` tool (status, enable, disable, set_cadence, run_now). The Brain tab's Schedules view lists it.
- The organize procedure lives in the Brain prompt, so `/organize` and the scheduled run follow the same rules.
- Pinned by `TestOrganizeBrainScheduleCadenceIsPerPerson`. Not deployed.

Still open: owner check of a real scheduled run (apply directly vs propose first).
