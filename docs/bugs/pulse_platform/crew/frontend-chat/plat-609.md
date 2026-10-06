[← crew / frontend-chat](index.md)

# PLAT-609: Remove an attached Crew template from its setup row above chat

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | crew |
| Area | frontend-chat |
| Summary | One-click template removal from its chat setup row; deployed on Confida. |

## What happened

The owner wants an easy way to undo adding a Crew template. Attached templates
show a setup row above the chat, but that row offered no removal action.

## Fix

- Add a visible Remove action directly on each owned template's setup row,
  including while chat is still opening or setup needs attention. No extra dialog
  is needed because removal preserves the Crew's files and chats.
- Read the current product and runtime manifests, deselect the removed template's
  skills unless another attached template needs them, then remove its receipt.
  Keep unrelated skill selections, capabilities, schedules, identity and purpose.
- Update the Crew list/cache and existing project chat tabs' selected skills;
  mark their runtime dirty for the next turn. Run readers cannot remove templates.
- Serialize removals per project. Failed saves retain the row and a persistent
  error; retry is idempotent even if skill deselection already succeeded.
- Keep skill files, setup progress, chat history and project work. Installation
  reuses existing template files so reattachment cannot overwrite user edits.

## Verification

- React integration drives the real production setup row and removal helpers with
  an in-memory planner-file transport. A failed final save keeps the row and
  error; retry removes only that template, persists across manifest reload, and
  leaves another template and all existing files unchanged. Reattachment retains
  edited skill files/progress. Shared-reader removal is rejected.
- Concurrent-removal regression keeps shared skills until their last template
  is detached and prevents two row removals from undoing each other.
- 74 tests across setup, removal, Work sessions and project manifests passed.
  TypeScript, targeted lint (including WorkSurface), full frontend build, release
  asset validation and bundle budget passed.

## Deployment

- Fix commit `aaaedbeb32a724093dc173c1ad799002a27e277c` is on `origin/main`.
- Deployed 2026-10-06 to Confida as `confida-aaaedbeb-20261006134215`,
  from shared build `aaaedbeb-20261006113603`.
- Verified source revision and removal callback, and hashes of the public main
  and current Crew JS assets against the release files. The Crew bundle
  referenced by the current entry contains the Remove action.
- Public health returned 200; deployed configuration checks passed. Full slot
  self-test: 77 passed, 0 failed, 35 skipped.
- Behavioral verification used the real React row/removal helpers above;
  an authenticated production browser session was not available.

## Left

No implementation or Confida deployment work remains. Refresh once to load the
new frontend, then use Remove on an attached template's row above the chat.
