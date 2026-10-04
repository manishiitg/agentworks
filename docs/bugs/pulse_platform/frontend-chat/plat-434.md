# PLAT-434 — Code and Crew pages could not connect their panel on multi-user servers ("project's browser is disconnected")

Status: fixed on main; deployment pending. Found 2026-10-04 on Excellence, when Codex found `perform_ui_action`
(PLAT-429) and the call was refused with "browser disconnected".

## What was wrong

`uiContractForScope` (ui_control.go) chose the Crew/Code view list only when the scope did **not** start with
`_users/`. On a multi-user server a project lives under `_users/<id>/Chats/Code/projects/<p>`, so it got the *workflow*
view list. The Code page's register/renew call reports its current view (`database`, `memory`, `skills`, `secrets`,
`folders` exist only in the project contracts); the server answered `409 invalid_state`, never stored the binding,
and the agent's action then saw no browser. The tool call itself used the Code contract directly, so it reached the
server and failed at "no binding". Log signature: `[UI-CONTROL] ... http_status=409 code=invalid_state` right after a
`session_not_active` burst on the project's session.

## Fix

`uiContractForScope` asks `isProjectWorkspacePath`, which already reads a project with or without its `_users/<id>/`
prefix. Who may bind is still checked by the route (`isOwnedWork`: own prefix or no prefix), so no access changes.
Test `TestUIContractForScopeIgnoresUserPrefix` (fails without the fix, for Code and Crew).

## Left

The same mistake keeps coming back for the same reason: see PLAT-435. After deploy, ask Codex on Excellence to open
the Costs panel with the Code page open.
