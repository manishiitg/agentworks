# Setup Consolidation

How Builder and Crew setup views were consolidated into shared Identity
and Integrations surfaces. Companion to `ui-design-guidelines.md`.

## Builder Setup

`WorkflowCapabilitiesPanel` serves two sections, in toolbar order
Identity → Integrations:

| View | Tabs | Contents |
|---|---|---|
| Identity | General, Secrets, File access, Models | `WorkflowIdentityPanel` (name, icon, soul, GitHub-style delete), `SecretSelectionSection`, `WorkflowFolderAccessView`, `WorkflowLLMConfigurationPanel` |
| Integrations | MCPs, Skills, Slack, WhatsApp, Gmail | Tool selection + `ConnectorsBrowser`, `SkillsManagerPanel`, `WorkflowBotsPanel` pinned per channel via `fixedChannel`, `WorkflowEmailPanel` |

The standalone Bots / Gmail / Secrets / Folders / LLM views were retired;
the UI-control contract was regenerated (18 views). The Setup toolbar is an
open/close `WorkspaceToolbarGroup`, matching Crew.

Each Integrations tab splits workflow picks from the shared shelf: a "This
workflow" group on top, "Platform connected" below (MCPs render two
`ToolSelectionSection` lists; skills use `splitSelectionGroups`). Ticking
moves a row between groups; empty groups hide. Search is per tab (below the
platform list on MCPs, above the list on Skills) and refresh lives only in
the header; the skills toolbar keeps Import plus an Ask AI install button.

## Crew parity

Crew mirrors Builder with project nouns (`frontend/src/products/work/`):

- `WorkIdentityPanel`: General (name, icon, role, instructions, delete
  request), Secrets, File access, Models (embedded `WorkModelsPanel` with
  `hideHeader`).
- `WorkIntegrationsPanel`: MCPs (server picker + `ConnectorsBrowser`),
  Skills, Slack, WhatsApp (`WorkflowBotsPanel` with `fixedChannel` and a
  Crew target), Gmail. Every inner panel joins the single tab scroll via
  `manageOwnScroll={false}`.
- `WorkWorkspaceView` is `dashboard | database | files | browser | costs |
  schedules | identity | mcp`. Legacy saved views and agent presentation
  ids (`bots`, `skills`, `secrets`, `models`, `email`, `folders`, `llm`)
  map onto the consolidated views in `WORK_UI_PRESENTATION_VIEWS`, so old
  preferences and agent commands keep working.

No server changes were needed for gating: `workViewGating.ts` reuses the
legacy feature panel ids (`mcp`, `skills`, `bots`, `secrets`, `folders`,
`models`). Identity is always reachable (General is the project's own
name and deletion); Integrations needs any of `mcp`/`skills`/`bots`;
inner tabs follow their own panel id. This also fixed Crew Gmail, which
had no server panel id and was previously gated off as a standalone view.

Crew identity edits persist through `updateProductProjectIdentity`
(`platform/chat/productProjects.ts`, mirrored by the `set_work_identity`
agent tool): omitted fields preserved, empty fields removed, stored in
`product.json`. Crew deletion reuses the surface confirm dialog, upgraded
to GitHub-style type-the-name confirmation.

## Custom icons

Both General tabs edit icons through the shared `IconUploadField`: an
emoji field plus an image upload that downscales to a ~128px thumbnail
(`utils/downscaleImage.ts`) stored inline as a data URL in the existing
icon field. All 16 render sites pick it up with no prop threading because
they funnel through `EntityIdentityIcon`. Server validation
(`agent_go/pkg/workflowtypes/icons.go`, used by workflow-manifest and Crew
identity validation) accepts emoji (≤8 chars) or image data URLs (≤100k
chars); prompt/tool rendering redacts image bytes. Creation dialogs stay
emoji-only; uploads live in Identity.

## Secrets UI

`SecretSelectionSection`, `SecretsManagerPanel`, and
`SecretSelectionDropdown` were migrated to the kit: theme tokens instead
of hardcoded palettes, kit `Badge` pills, `SettingsCard` blocks, the empty
list box hidden when there is nothing to show, and the dropdown's text `✕`
replaced with the X icon. Behavior is unchanged, including the native
`confirm()` dialogs pinned by contract tests. The amber bot-enabled toggle
is the one intentional semantic color, documented in its own copy.

## Card standard

Tab content follows the shared-knowledge-bases card, extracted into the
shared `SettingsCard` (`components/ui/SettingsCard.tsx`): primary icon +
semibold title + `SettingsCount` pill, optional right action, muted
explainer, content, `SettingsEmpty` dashed empty state. Adopted by both
General tabs, the secrets surfaces, and both folder views
(`KnowledgebaseSources` itself already matches and was left untouched).

## Verification

- `npx vitest run` (frontend; only pre-existing peer-owned provider /
  session-restore failures remain)
- `npx tsc -b`, `npx eslint` on touched files
- `go build ./agent_go/...`, `go test` on `pkg/workflowtypes`,
  `internal/workproduct`, manifest validation
- Kit adoption is pinned by `formsKitAdoption.test.ts`; Crew consolidation
  by `WorkWorkspacePane.test.ts` / `WorkWorkspaceToolbar.test.ts` /
  `workViewGating.test.ts`.
