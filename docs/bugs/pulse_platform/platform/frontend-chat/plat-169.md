[← platform / frontend-chat](index.md)

# PLAT-169 — the tool-selection checkbox creates duplicate hyphen/underscore MCP server entries, which the manifest validator then permanently blocks

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | platform |
| Area | frontend-chat |
| Summary | P1 — once a workflow's manifest has this duplicate, the user |

| Coordination | Value |
|---|---|
| Assigned agent | unassigned |
| Ticket state | `implemented` — build/test verified, live reverify pending |
| Last synchronized | `2026-08-21` |

- **Priority:** P1 — once a workflow's manifest has this duplicate, the user
  cannot save *any* workflow settings change (even unrelated ones) until it is
  manually edited out of the JSON file by hand. There is no in-product way to
  recover.
- **Owner:** `ToolSelectionSection.tsx` (server checkbox state), workflow
  manifest save path (`ModePresetBar.tsx`).
- **Related:** none found — a fresh, unticketed bug.

## Evidence and root cause

A user's `sheet-analysis` workflow save failed with:

```
Failed to save configuration: Failed to write manifest: manifest validation
failed: duplicate capabilities.selected_servers entries "google-sheets" and
"google_sheets" resolve to the same MCP server
```

`workspace-docs/Workflow/sheet-analysis/workflow.json`'s `capabilities`
already contained both:

```json
"selected_servers": ["google-sheets", "google_sheets"],
"selected_tools": ["google_sheets:*"]
```

`google-sheets` (hyphen) is the only currently-registered server id
(`agent_go/configs/mcp_servers_clean_user.json`); `google_sheets` (underscore)
is a legacy spelling that resolves to the same server only through the
hyphen/underscore alias tolerance `MCPConfig.GetServer` already applies at
runtime. The backend's manifest validator
(`agent_go/cmd/server/workflow_manifest.go:468-482`, added 2026-08-02,
commit `f9f8ac35c`) normalizes `selected_servers` the same way
(`strings.ReplaceAll(trimmed, "_", "-")`) and correctly **rejects** the
duplicate — but it only rejects; it never repairs the array, and the manifest
write path (`handleUpdateWorkflowManifest` /
`mergeWorkflowCapabilitiesUpdate`, `cmd/server/workflow_manifest_routes.go:172-315`)
does a full replace of `capabilities` from whatever the frontend sends, not a
merge. So once a manifest is in this state, no future save — of anything —
can succeed until it's hand-edited.

**How the duplicate actually gets created**, traced in
`frontend/src/components/ToolSelectionSection.tsx`: every comparison against
`selectedServers` is an exact string match —

- `handleServerToggle` (`:159`): `selectedServers.includes(serverName)`
- the same function's remove branch (`:163`): `s !== serverName`
- the checkbox's own `checked` state (`:305`, rendered at `:328`):
  `selectedServers.includes(serverName)`
- the selected-first sort comparator (`:296-297`)

`availableServers` is rendered from the canonical MCP catalog (always the
current spelling, `google-sheets`). If a manifest was saved under the old
spelling (`google_sheets`, from before the server was renamed/re-registered
with a hyphen — `sheet-analysis`'s manifest is dated 2026-07-21, predating
the Aug 2 validator entirely), the `google-sheets` checkbox renders
**unchecked**, because no entry in `selectedServers` exact-matches it. A user
who — reasonably, since the checkbox tells them it isn't selected — clicks it
gets `[...selectedServers, "google-sheets"]`: the old spelling stays, the new
one gets appended alongside it. The user never sees anything wrong until the
*next* save, which the validator now rejects with a message that gives no
indication of which field or which workflow instance caused it.

## Decision

Fix the actual defect (the checkbox's exact-string comparisons), and add a
save-time safety net so any workflow already in this state self-heals on its
next save instead of requiring a hand edit.

1. **`ToolSelectionSection.tsx`**: add a small `serverNamesMatch(a, b)`
   helper using the identical normalization the backend validator already
   uses (trim, then `_` → `-`), and use it everywhere `selectedServers` is
   compared against a server name — the toggle's `isSelected` check, its
   remove-branch filter, the checkbox's own `checked` prop, and the
   selected-first sort. Also make the "remove this server's tools" filter in
   the same handler (`selectedTools.filter(t => !t.startsWith(...))`)
   alias-aware, so unchecking `google-sheets` correctly clears a
   `google_sheets:*` entry too, not just an exact-prefixed one.
2. **Toggling on no longer blindly appends.** Even with (1) making `isSelected`
   correct, add defense in depth: the "add server" branch filters out any
   alias-equivalent entry already present before appending the canonical
   name, so a stray legacy duplicate can never survive a toggle interaction
   involving that server.
3. **`ModePresetBar.tsx`'s `handleSavePreset`**: immediately before building
   the `selected_servers` payload (both the workflow-manifest branch and the
   multi-agent/DB-preset branch — both reuse the same
   `ToolSelectionSection` component and take the same `selectedServers`
   parameter), collapse any alias-equivalent duplicates to one entry (first
   occurrence wins). This is the actual fix for a workflow that already has
   this problem sitting in its manifest today: the very next successful save
   silently normalizes it, with no separate migration step and no need for
   anyone to hand-edit JSON.

## Non-goals

- Not changing the backend validator's behavior — it should keep rejecting a
  genuine duplicate; the fix is to stop constructing one in the first place,
  plus proactively deduping before it ever reaches that validator.
- Not writing a one-off migration/scan across all existing workflow
  manifests for this same latent issue — the save-time self-heal (item 3)
  fixes any affected workflow the next time its owner saves it, which is
  proportionate; a bulk scan is not justified by the evidence gathered here
  (one confirmed instance).
- Not touching `selected_tools`' broader tool-id format beyond the
  `<server>:` prefix comparison directly implicated here.

## Acceptance tests

1. A manifest with `selected_servers: ["google_sheets"]` (legacy spelling
   only) renders the `google-sheets` checkbox as **checked**, not unchecked.
2. Clicking that (correctly-checked) checkbox removes the server — the
   resulting array no longer contains `google_sheets` or `google-sheets` in
   any form, and any `google_sheets:*`/`google-sheets:*` tool entries are
   also removed.
3. Loading a manifest that already has both spellings and saving it
   unchanged (no server toggled) produces a `selected_servers` payload with
   exactly one entry for that server — the save succeeds instead of hitting
   the backend's duplicate-rejection.
4. A manifest with only the canonical spelling is unaffected by any of the
   above (no behavior change for the common case).

## Verification

Frontend: new tests for `serverNamesMatch` (or the equivalent behavior) and
for `handleServerToggle`'s checked-state/remove/add paths against a
legacy-spelled `selectedServers` array; a `ModePresetBar` (or the dedup
helper, whichever is more directly testable) test proving the pre-existing-
duplicate payload gets collapsed to one entry before being sent. `go build`
is unaffected (this ticket makes no backend changes). `npx tsc --noEmit` and
`npx vitest run` clean, zero new failures versus a clean baseline.

## Implementation (2026-08-21)

Built as designed, plus one extraction: the alias-matching logic moved into
a new shared module, `frontend/src/utils/mcpServerAlias.ts`
(`normalizeServerAlias`, `serverNamesMatch`, `isSelectedServer`,
`toolBelongsToServer`, `hasServerTool`, `dedupeServerNames`), rather than
staying private to `ToolSelectionSection.tsx` — `ModePresetBar.tsx` needs
`dedupeServerNames` too, and a shared, independently-testable module beats
duplicating the same normalization in two files.

`ToolSelectionSection.tsx`: every `selectedServers`/`selectedTools`
comparison against a server name now goes through the shared helpers —
`handleServerToggle`'s selected-check, remove-filter, and add-filter; the
selected-first sort comparator; the checkbox's own `checked` prop;
`areAllServerToolsSelected`'s "all tools" marker check and per-tool
completeness check. The "add server" branch also strips any stray
alias-equivalent entry before appending, so a toggle can never leave both
spellings present even if `selectedServers` already held a legacy one.

`ModePresetBar.tsx`'s `handleSavePreset`: `dedupeServerNames` runs once,
immediately after resolving `selectedGlobalSecretNames`, and the result
(`dedupedSelectedServers`) feeds both the workflow-manifest save branch and
the multi-agent/DB-preset branch — both consume the same `selectedServers`
parameter from the same `ToolSelectionSection`, so both get the same
self-healing.

**Verified:** `npx tsc --noEmit` clean. New tests in
`frontend/src/utils/mcpServerAlias.test.ts` (16 cases) cover every exported
helper, including the exact reported scenario
(`dedupeServerNames(['google-sheets', 'google_sheets'])` → one entry) and its
reverse-order variant. No dedicated `ToolSelectionSection` render test was
added — this component has no existing React Testing Library harness to
extend (same situation as `CostsPopup.tsx` in PLAT-166/167), and the actual
logic under test is now the pure, directly-tested `mcpServerAlias.ts`
module — a render test would mostly be re-testing wiring, not new logic.
`npx vitest run` — full suite, 706/706 passing, zero regressions (and the
one previously-known pre-existing failure, `PulseWorkspace.test.tsx`, is now
also passing — fixed by other work already merged to `origin/main` ahead of
this branch, not by anything in this ticket).

**Not done:** live reverify — confirming in the actual running UI that a
legacy-spelled manifest now renders its checkbox correctly and self-heals on
save. Flagged, not claimed. No backend changes were needed or made.

## Immediate unblock (2026-08-21, done ahead of the code fix)

The user's actual `sheet-analysis` manifest
(`workspace-docs/Workflow/sheet-analysis/workflow.json`) was hand-edited to
drop the duplicate — `selected_servers` now has only `"google-sheets"`, and
`selected_tools`' `google_sheets:*` entry was renamed to `google-sheets:*` to
match. This is local workspace data, not part of this repository, so it is
not part of this ticket's diff; noted here only so the fix's own acceptance
tests aren't mistaken for what actually unblocked the user in the moment.
