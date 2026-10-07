[← crew / commands](index.md)

# PLAT-661: Crew custom commands not listed

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | crew |
| Area | commands |
| Summary | Custom commands created in a Crew chat never showed: /api/commands refused every shared Crew/<id> path with 400 |

## What happened

## Fix

## Left

## What happened

Excellence, 2026-10-07 (Shashi, Crew "browser journey qa analyst"): a custom command made from the Crew chat (`scrape-website`) was saved to `Crew/browser-journey-qa-analyst-dcf92d01/commands/custom/`, but the chat's command list stayed empty. Every `GET /api/commands?workspace_path=Crew/...` returned 400 "workspace_path must identify the current project or workflow": the route knew `Workflow/` and `_users/.../Chats/...` paths, not the shared Crew root that Crews moved to (PLAT-442). Code works because its projects are still under `_users/.../Chats/Code/`.

## Fix

`commandPathForRequest` resolves a shared `Crew/<id>` path: Crew readers list and read its commands, only the owner creates, edits or deletes them, and the path is the same `Crew/<id>/commands/custom` the chat tool writes.
