[← brain / curation](index.md)

# PLAT-618: Brain curator: /organize, /dedupe and scheduled tidy-ups

| Field | Value |
|---|---|
| State | open |
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

## Open (owner)

1. Apply changes directly with the summary, or propose first for the first few runs?
2. Who may schedule it: Editors, or only folder Owners and admins (merging deletes the other copies)?
