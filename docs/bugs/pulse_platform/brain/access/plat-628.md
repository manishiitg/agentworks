[← brain / access](index.md)

# PLAT-628: Remove Brain folder bindings; steps describe Brain use

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | brain |
| Area | access |
| Summary | Remove project folder bindings and the Folders Brain setting; projects use Off/Read/Read & write limited by the person's roles; steps state Brain reads, writes and limits in their description |

## What happened

## Fix

## Left

## Decision

Owner, 2026-10-06: "i dont think we should have this binding"; "workflow builder can define how steps access/update/read in natural language in prompts", using the step description format. See DECISIONS 2026-10-06.

## Done

- Project Brain access is Off / Read / Read & write (`brain_access`); unset and the legacy `folders` value are Read & write. Bindings (`shared_knowledgebase`) are no longer read or written; `bind_project` / `unbind_project` and `binding_alias` are gone from the tool surface and the project API (a bind request is refused with a pointer to set_project_access and step descriptions).
- Legacy KB migration cutover sets `brain_access: write` instead of writing a binding (rollback restores it); the migrated-project prompt section tells steps to use the Brain folders their description names.
- Builder guidance (`builder.md`, `step-description.md`), Brain prompts (`mcp.md`, `access-builder.md`) and the agentworks client skill describe Brain use in step descriptions: reads under Inputs/Guides (`brain:<folder>/<note>`), writes under Output, limits under Rules.
- Project panel: three options, no folder list (also drops its whole-tree folder fetch).
- Tests: binding-only tests removed (retry/audience, Crew+workflow shared folder, legacy alias replacement); others now set the project's access instead of binding; the project API test checks a bind is refused; one panel test pins the three options and legacy display.
- Checked before removing: no project on RTS, Excellence or Confida had a binding.

## Left

- The Brain domain package still evaluates `BindingPolicy.Bindings` (nothing sets it now); the legacy migration uses a binding value internally as its destination folder. Remove when the migration is retired.
- Not deployed.
