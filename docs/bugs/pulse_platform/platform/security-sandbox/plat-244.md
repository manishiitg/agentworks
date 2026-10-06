[← platform / security-sandbox](index.md)

# PLAT-244 — Retired provider-media tools could still be reintroduced through legacy workspace registries

| Field | Value |
|---|---|
| State | closed |
| Priority | - |
| Product | platform |
| Area | security-sandbox |
| Summary | harness_issue, severity medium. |

| Coordination | Value |
|---|---|
| Assigned agent | Codex |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-29` |

- **Priority:** harness_issue, severity medium.
- **Findings:** No workflow finding is linked yet. This is a platform-surface
  defect discovered while narrowing the supported provider-backed tools to
  `generate_text_llm` and `search_web_llm`.

## Root cause

The primary workspace tool registry had stopped exposing provider media tools,
but several independent legacy paths could still reintroduce them:

- `pkg/workspace/advanced_tools.go` retained a `read_image` definition;
- `pkg/workspace/tools.go` retained a `read_image` executor;
- `workspace_image`, `workspace_image_gen`, and `workspace_image_edit`
  remained selectable built-in categories; and
- old workflow manifests containing a `workspace_image*` entry were migrated
  in a way that still auto-enabled the broad workspace tool category.

This split the apparent tool surface from the actual selectable surface. It
also left stale provider-media guidance in active builder instructions.

## Fix

Removed the legacy shared-agent image definition, executor, and media category
registrations; category expansion no longer recognizes `workspace_image`; and
old `workspace_image*` workflow entries are explicitly dropped as retired.
The active shared-agent provider surface is now limited to
`generate_text_llm` and `search_web_llm` (alongside shell and diff tools).
Active guidance no longer presents media configuration as callable.

This deliberately does **not** remove Family Server's independent uploaded
image-reading capability or the separate Video product. Those are distinct
product features, not routes in the shared MCP-agent workspace registry.

## Verification

- `TestCreateWorkspaceToolRegistryIncludesOnlyActiveTextAndSearchTools`
  asserts that every media tool is absent from definitions and executors and
  that `workspace_image` resolves no tools.
- Focused `go test` passed for `cmd/server/virtual-tools`, `pkg/workspace`,
  and `pkg/orchestrator`.
- Focused `golangci-lint` passed for the same packages.

## Reverify

Start a fresh Builder/Workshop agent with both a current and a legacy workflow
manifest. Confirm its MCP tool list contains neither `read_image` nor any
image/video/audio/music tool, and that a legacy `workspace_image*` selection
cannot cause a media provider credential/setup prompt.

## Correction (2026-08-29, Claude Code)

The ticket's own "Verification" section covered `cmd/server/virtual-tools`,
`pkg/workspace`, and `pkg/orchestrator` directly, but not
`pkg/orchestrator/agents/workflow/step_based_workflow` (a distinct
subpackage) or `cmd/server/guidance` — both were left red by this change:

- `TestWorkshopCLIPromptUsesProjectedWorkspaceToolReference` and
  `TestPhaseChatWorkshopSelectsWorkspaceToolGuidanceByTransport`
  (`step_based_workflow`): `pkg/instructions/workspace_special_tools.go`'s
  rewrite dropped the `read_skill(...path":"references/workspace-media-tools.md")`
  pointer entirely from both `GetSpecialWorkspaceToolsInstructions` and
  `GetSpecialWorkspaceToolsPointer` — a real regression, since that skill
  doc still exists and still carries live guidance for the two active tools
  (e.g. PLAT-234's `search_web_llm` timeout guidance). Restored the pointer
  in both functions, scoped to the narrowed tool surface.
- `TestMaterializedReferenceSkillIncludesConfigToolOnlyDocs`
  (`cmd/server/guidance`): the rewritten `workspace-media-tools.md` dropped
  its `set_provider_auth` mention entirely — a real content gap, since
  agents reading this doc still need to know how to store credentials for
  the two active tools. Restored it.

Two of the same test's own assertions were themselves stale relative to
this ticket's legitimate, deliberate intent (checking for
`generate_video(prompt, output_path` and `**Search provider routing**
comes from the published LLM set`, both accurate before this ticket but
contradicted by its own narrowing) — updated those assertions to match the
new, correct behavior rather than reverting the production narrowing.

`go build ./...` and `go test ./pkg/instructions/... ./cmd/server/...
./pkg/orchestrator/...` all pass after these fixes. This does not change
this ticket's own functional narrowing (media tools remain hidden); it
restores the reference-doc pointer and content the narrowing incidentally
carried away, and brings the two affected test files current with the
narrowing's own intent.

## Frontend correction (2026-08-29)

The backend/registry refactor had not removed all frontend provider-collection
paths: `LLMConfigurationModal` still contained unreachable-but-real Gemini
audio, MiniMax audio/music, ElevenLabs, and Deepgram credential sections, and
the frontend store could still migrate, persist, and sync the latter three
keys. A stale provider manifest or direct tab selection could therefore
re-expose a retired configuration surface.

Removed every retired media provider tab/section from the Model Provider page,
excluded MiniMax/ElevenLabs/Deepgram from the modal and library filters, and
stopped frontend key migration, persistence, and workspace-key sync for those
providers. Pi CLI remains in the coding-agent area; its own supported text
sub-provider routing is not a standalone MiniMax credential page.

`npx eslint src/components/LLMConfigurationModal.tsx src/stores/useLLMStore.ts`
and `npm run build` pass. The repository-wide frontend lint command still has
pre-existing failures in unrelated files and was not used as this change's
gate.

## Completion cleanup (2026-08-29)

Removed the remaining frontend and server-side configuration plumbing rather
than leaving it merely hidden:

- deleted the unused image-generation configuration modal/store, chat request
  fields, and `/api/image-gen/test` endpoint;
- removed legacy image-generation fields and executor override wiring from
  workflow/workshop query handling;
- removed obsolete MiniMax standalone, ElevenLabs, and Deepgram frontend
  state/default/model-list handling. MiniMax is retained only as a Pi text
  sub-provider credential route;
- stopped ElevenLabs and Deepgram environment discovery; and
- narrowed `list_llm_capabilities` documentation and registration to chat and
  web search. `estimate_llm_cost`, which was only meaningful for retired media
  features, is no longer registered.

The retained media implementation files serve no shared-agent path and remain
out of scope for the separate Video product. Family Server upload image reading
and Pi text sub-provider routing remain intentional exclusions.

Verification: focused server/package tests and the production frontend build
pass.
