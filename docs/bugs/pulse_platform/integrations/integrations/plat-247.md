[← platform / integrations](index.md)

# PLAT-247 — restore `read_image`/`image_gen`/`image_edit`: PLAT-244's media-tool retirement swept a load-bearing inspection tool out with the generation tools

| Field | Value |
|---|---|
| State | in progress |
| Priority | P1 |
| Product | integrations |
| Area | integrations |
| Summary | P1 — user-directed correction, reported live. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented` |
| Last synchronized | `2026-08-29` |

- **Priority:** P1 — user-directed correction, reported live.
- **Owner:** `pkg/workspace/advanced_tools.go`, `pkg/workspace/tools.go`,
  `cmd/server/virtual-tools/workspace_tool_registry.go`,
  `pkg/common/tool_categories.go`, `cmd/server/server.go`,
  `pkg/orchestrator/base_orchestrator_folder_guard.go`,
  `pkg/orchestrator/base_orchestrator_tools.go`, `cmd/server/instructions.go`,
  `interactive_workshop_manager.go`.
- **Related:** [PLAT-244](../../sandbox/security/plat-244.md)) (the retirement this partially
  reverses) and the two commits that implemented it —
  `01909fb4b` "feat: focus workspace provider tools on text and search" and
  `7ec055a74` "refactor: remove retired media tool escape hatches".

## What happened

PLAT-244 narrowed the provider-backed workspace tool surface to
`generate_text_llm` and `search_web_llm` only, retiring
`image_gen`/`image_edit`/`generate_video`/`text_to_speech`/`speech_to_text`/
`generate_music` — **and** `read_image` — in the same sweep, with no written
rationale distinguishing why `read_image` specifically needed to go.

Live impact: a coding-agent CLI running a workflow step in confida-login was
asked to review a UI screenshot for a CSS bug and reported it had no way to
read image bytes from a file path at all — the exact ordinary "look at this
screenshot and tell me what's wrong" task these tools exist for.

The generation tools are genuinely optional, provider-cost-bearing creative
features a workflow-automation platform can reasonably decide not to own.
`read_image` is not that — it is read-only inspection, the basic mechanism
by which an automated agent looks at anything visual at all, and it is
exactly the kind of default capability Cursor, Claude Code, and Codex ship
with. Lumping it into the same removal as `generate_music` conflated two
different things.

Restoring the guidance also surfaced something PLAT-244's removal had
already deleted the record of: `read_image`/`image_gen`/`image_edit` already
supported routing `provider` through a coding-agent CLI's own native vision
(`codex-cli`, `cursor-cli`, `claude-code` — passing the local workspace image
path straight to the CLI) as an alternative to a standalone vision-model API
call. This is the "native tool" capability the operator specifically asked
about; it already existed in this tool's design and was never a separate
build.

## Fix — scope: exactly `read_image`, `image_gen`, `image_edit`

Video/audio/music generation and transcription remain retired; that part of
PLAT-244 stands. Restored, verbatim where the deleted code was still
recoverable from git history:

- `pkg/workspace/advanced_tools.go` — `imageToolDef()`,
  `GetImageToolDefinitions()`, wired back into `GetAdvancedToolDefinitions()`.
- `pkg/workspace/tools.go` — the `read_image` executor
  (`client.ReadImage`), which was never deleted, only unregistered.
- `cmd/server/virtual-tools/workspace_tool_registry.go` —
  `CreateWorkspaceImageTools()` (image_gen + image_edit) back in the tool
  list, `MergeImageToolExecutors` back in the executor merge (this file
  gained an executor-visibility filter since PLAT-244 that did not exist
  before; restoring both the tool-list entry and the executor merge together
  was required for the filter to keep them), and the `workspace_image`
  category back in `initWorkspaceToolNamesCache`.
- `pkg/common/tool_categories.go` and `cmd/server/server.go` — `workspace_image`,
  `workspace_image_gen`, `workspace_image_edit` back in the builtin/MCP-bridge
  category allowlists, so these categories are recognized as selectable again.
- `pkg/orchestrator/base_orchestrator_folder_guard.go` — `read_image`
  reclassified as a read-only tool in both folder-guard classification maps
  (security correctness: an unclassified tool is not automatically
  permissive, but it must be explicitly read-only, not left unclassified).
- `pkg/orchestrator/base_orchestrator_tools.go` — `workspace_image` restored
  to the category-resolution switch.
- `cmd/server/instructions.go` — restored the Image Generation Defaults and
  Image Analysis Defaults sections (trimmed for the current active scope),
  including the native-CLI-provider routing explanation.
- `pkg/orchestrator/agents/workflow/step_based_workflow/interactive_workshop_manager.go` —
  restored `read_image` in the "Shell & discovery" one-liner and the
  `enabled_custom_tools` category description.

mcpagent (the sibling execution-engine repo) needed no changes: its
`ToolFilter.systemCategories` still listed `workspace_image_gen`/
`workspace_image_edit` throughout — PLAT-244's removal only ever touched the
agent_go side.

## Explicitly not done

- Did not restore `generate_video`/`text_to_speech`/`speech_to_text`/
  `generate_music` or any of their guidance — that narrowing stands.
- Did not revert `controller_agent_factory.go`'s legacy-manifest-entry drop
  logic (old `workspace_image*` workflow manifest entries still get dropped
  as no-ops). Functionally harmless either way: these tools are unconditionally
  bundled into `workspace_advanced`'s auto-inclusion, the same reason the
  pre-PLAT-244 code already treated individual `workspace_image:*` manifest
  entries as no-ops.
- Did not investigate whether the "native CLI tool passthrough" alternative
  (letting `codex-cli`/`cursor-cli`/`claude-code`'s own built-in tools
  operate directly instead of routing through this platform's bridge tools)
  is feasible — that would require per-CLI headless-mode research across
  four different third-party products. Restoring the existing, already-built
  provider-routing bridge (which already supports native-CLI routing as one
  of its providers) was the lower-risk, immediately-available fix.

## Verification

- `go build ./...` clean.
- `go test ./pkg/workspace/...`, `./cmd/server/`, `./pkg/orchestrator/`,
  `./pkg/orchestrator/agents/workflow/step_based_workflow/...` all pass.
- `TestCreateWorkspaceToolRegistryIncludesOnlyActiveTextAndSearchTools`
  renamed to `TestCreateWorkspaceToolRegistryIncludesActiveTextSearchAndImageTools`
  and rewritten: asserts `read_image`/`image_gen`/`image_edit` are present
  with executors and correct category, `generate_video`/`text_to_speech`/
  `speech_to_text`/`generate_music` remain absent, and `workspace_image`
  resolves tools again (inverted from PLAT-244's assertion that it resolved
  none).
- Restored `pkg/workspace/read_image_test.go`'s deleted
  `TestLLMBackedToolDefinitionsReferenceCapabilityDiscovery` case.
- `cmd/server/virtual-tools` package could not be test-run directly — it has
  a pre-existing, unrelated compile failure (`generate_text_llm_tool_p0_reviews_test.go`
  imports `github.com/manishiitg/mcpagent/agentreview`, an internal mcpagent
  package that cannot be imported from outside its module; confirmed this
  reproduces identically with this session's own changes fully reverted, so
  it predates this ticket). Verified this package's non-test code compiles
  via the full `go build ./...`, and `gofmt -l` is clean on every changed
  file in that package.

## Reverify

Start a fresh Builder/Workshop or step agent, confirm `read_image`,
`image_gen`, and `image_edit` appear in its tool list, and confirm a
`read_image` call against a real workspace screenshot succeeds end to end
(this was the exact live failure that prompted this ticket).

## Follow-up (2026-08-29) — the native-CLI-passthrough question this ticket
left open, now answered with live verification

"Explicitly not done" above flagged that whether native-CLI passthrough
(`provider=codex-cli`/`cursor-cli`/`claude-code`) actually *works* was
never investigated — restoring the bridge-routing code path was the
lower-risk fix at the time, but nobody had driven it against the real
CLIs. This follow-up did exactly that, live, in the sibling
`multi-llm-provider-go` repo (pulled into `agent_go` via its local
`replace` directive — no version bump needed for these fixes to be live
here).

**How native passthrough actually works** (was undocumented before this):
there is no dedicated function for it, unlike `search_web_llm`'s
`SearchWeb()`. `wrapReadImageWithLLM` / the `image_gen`/`image_edit` tool
wrappers just send a plain-text prompt ("here's a local file path /
attached reference image, do X") through the provider's normal
`GenerateContent()` / `GenerateImages()` call, and rely entirely on that
CLI's own agentic judgment to invoke its own native tool. Verification
had to be per-provider because there is no shared mechanism to reason
about.

**`read_image` native passthrough:**
- `codex-cli`, `claude-code`: verified live, work correctly, including
  under the same lockdown flags a real workflow-step session runs under
  (`WithDisableShellTool()`).
- `cursor-cli`: found a real bug. Under `WithDenyBuiltinTools(true)` (same
  lockdown a real workflow-step session uses), every `read_image` call
  hung for the full 180s context deadline instead of failing or
  succeeding. Root cause: the `beforeReadFile` hook cursor installs to
  force MCP-bridge routing denies *every* file read unconditionally (no
  matcher, unlike the tool-name-matched `preToolUse` deny) — and since the
  MCP bridge has no vision equivalent, the denied agent just flails
  (e.g. trying to `cat` binary PNG bytes through the bridge) until
  timeout instead of getting redirected anywhere useful. Fixed by having
  the deny script allow image-extension (`.png`/`.jpg`/`.jpeg`/`.webp`)
  reads through while leaving every other denial unchanged. Verified live:
  180s timeout → 20-30s pass, lockdown still fully in place.
  (`multi-llm-provider-go` commit `7a0e17d`.)

**`image_gen`/`image_edit` native passthrough (codex-cli is the only
supported provider besides vertex — `cursor-cli`/`claude-code` were never
wired up for generation/editing at all, only for `read_image`; confirmed
via `InitializeImageGenerationModel` in `multi-llm-provider-go`, which
only switches on `vertex`/`minimax-coding-plan`/`codex-cli`. vertex
itself not investigated this pass. agy-cli was fully removed from this
repo in this same session — see Follow-up (2026-08-29b) below):**
- `image_gen` worked, but ran under
  `--dangerously-bypass-approvals-and-sandbox` (zero lockdown at all,
  independent of whatever session lockdown was requested). Verified live
  that codex's built-in `image_gen` tool needs no approval prompt and
  completes fine under `--sandbox workspace-write --ask-for-approval
  never` instead — real security improvement, no behavior change.
  Deliberately did not also disable `shell_tool`: codex's image skill
  saves the generated file under `$CODEX_HOME/generated_images/` (outside
  any sandbox) and shells out to `cp` it into the workspace path;
  disabling shell would strand the file with no way to relocate it.
- `image_edit` was **completely broken** — every single call failed.
  `runSingleImageCommand` passed `--image <path>` as two separate argv
  entries; codex's `--image` flag is clap-variadic (`<FILE>...`) and
  greedily swallowed the prompt string that followed as another file
  argument, leaving codex with no prompt at all ("No prompt provided via
  stdin", exit 1). Reproduced manually against the real CLI before
  touching code, fixed by passing a single `--image=<path>` token,
  verified live (127s PASS, image content-checked as actually recolored
  per the edit prompt, not just "returned some bytes").
  (`multi-llm-provider-go` commit `1c55f59`.)

Both fixes shipped with new live tests
(`TestCursorCLIRealImagePathAnalysis` pre-existed and now passes;
`TestCodexCLIRealImagePathAnalysis`, `TestClaudeCodeRealImagePathAnalysis`,
`TestCodexCLIRealImageGeneration`, `TestCodexCLIRealImageEditing` are new)
and full-package regression sweeps (0 failures) in
`multi-llm-provider-go`. Not yet done: the same live-verification pass for
`image_gen`/`image_edit` on `vertex` (the only other supported
provider). Also not yet done: a P0 test in this repo
(`mcp-agent-builder-go`) exercising the actual `read_image`/`image_gen`/
`image_edit` tool surface end to end (the live verification above covers
the underlying `multi-llm-provider-go` adapters only, one layer below
this repo's own tool wrappers in `cmd/server/virtual-tools/`).

## Follow-up (2026-08-29b) — agy-cli fully removed from this repo

While investigating the "not yet checked" providers above, grepping for
`agy-cli` surfaced ~23 files in this repo still referencing it — despite
`multi-llm-provider-go` having removed the Agy/Antigravity CLI provider
entirely back on 2026-07-24 (commit `15636f4dd`, "remove Agy provider"):
no `ProviderAgyCLI`, no `agycli` adapter package, zero "agy" hits anywhere
in that module (confirmed via repo-wide grep). Every `agy-cli` reference
left in this repo — the `image_gen`/`image_edit`/`read_image` tool
descriptions advertising it as a real option (it would have failed at
runtime with "image generation not supported for provider: agy-cli"),
tmux session-prefix detection, statusline provider-label mapping, the
terminal-lease registry, engine-install-hint text, and several dedicated
test functions — was leftover from before that removal, most likely
reintroduced by this same ticket's original restoration (which restored
image-tool code "verbatim from git history" from a point that still had
`agy-cli` in it).

Removed entirely, mirroring the July 2026 upstream precedent: all
`agy-cli`/`AgyCLI` map entries, switch cases, tool-description text, and
dedicated test functions across `cmd/server/`, `cmd/testing/`,
`internal/terminals/`, `internal/terminalleases/`, `internal/enginedetect/`,
`pkg/common/`, and `pkg/orchestrator/agents/workflow/step_based_workflow/`.
Illustrative-only mentions (tmux session-name examples, doc comments) were
swapped to a still-live provider (usually `codex-cli`) rather than simply
deleted, to keep the surrounding tests/comments meaningful. Two
comments citing agy-cli's specific historical "does not support
concurrent sessions ... with different MCP configs" error were
generalized to describe the risk class rather than naming a CLI that no
longer exists, since the underlying isolation protection they document
(`config.IsolateCodingAgentWorkspace = true`) is applied unconditionally
regardless of provider and is still load-bearing.

Also simplified codex-cli's image-generation model list (`imageProviderModels`,
tool descriptions, `supportedImageProviderSummary`) from
`{codex-cli, gpt-5.4, gpt-5.4-mini, gpt-5.3-codex, gpt-5.3-codex-spark}` down
to just `{codex-cli}`: that list was a verbatim copy-paste of the general
codex-cli text-model list from `multiagent_llm_tools.go`, never curated for
image use. Verified from the live codex transcript captured investigating
the sandbox-lockdown question above that `--model` only selects which
model orchestrates the tool call (cost/latency); the native `image_gen`
tool itself is identical regardless, so offering those as distinct "image
models" was misleading. Left `defaultImageAnalysisModelForProvider`'s
`gpt-5.4-mini` default and `inferImageProviderFromModel`'s recognition of
all four model-ID strings untouched: the image *analysis* path
(`read_image`) doesn't validate against `imageProviderModels` at all, and
`gpt-5.4-mini` is still its real, functioning default.

Verified: `go build ./...` and the full test suites for every touched
package pass (`cmd/server`, `cmd/server/virtual-tools` — pre-existing
`agentreview` import failure aside, verified separately by temporarily
excluding the already-broken files — `cmd/testing`, `internal/terminals`,
`internal/terminalleases`, `pkg/common`,
`pkg/orchestrator/agents/workflow/step_based_workflow`), 0 failures.

## Follow-up (2026-08-29c) — the P0-in-this-repo gap, closed; two new live
findings surfaced

Closes the other "not yet done" item from Follow-up (2026-08-29): a go
test in this repo that exercises the actual `read_image`/`image_gen`/
`image_edit` tool surface (`CreateReadImageProviderTestExecutor`,
`CreateImageGenExecutor`, `CreateImageEditExecutor` in
`cmd/server/virtual-tools/`), not just the `multi-llm-provider-go` adapter
one layer below.

**Why not the existing `agentreview`-based P0 pattern** (the one
`search_web_llm`/`generate_text_llm` use, e.g.
`search_web_llm_tool_p0_live_test.go`): investigated it as the template
and found it is itself currently broken, repo-wide, not just in the one
file PLAT-247's original restoration flagged. `agentreview` lives at
`mcpagent/internal/agentreview` — genuinely `internal/`, with no public
counterpart — so `import "github.com/manishiitg/mcpagent/agentreview"`
cannot resolve from outside the `mcpagent` module at all. Every test file
using it (including `search_web_llm_tool_p0_live_test.go` itself) fails
to compile today. Mirroring that pattern would only have added more
broken files. Used plain env-var-gated live tests with direct assertions
instead (same shape as this session's `multi-llm-provider-go` tests),
which is both simpler and actually runs.

New files: `read_image_tool_p0_live_test.go`, `image_gen_tool_p0_live_test.go`,
`image_edit_tool_p0_live_test.go` in `cmd/server/virtual-tools/`. Each
skips by default; opt in with `READ_IMAGE_TOOL_P0_LIVE=1` /
`IMAGE_GEN_TOOL_P0_LIVE=1` / `IMAGE_EDIT_TOOL_P0_LIVE=1` plus the
workspace-URL/docs-dir/image-path env vars documented in each file's
doc comment.

**Test infrastructure gotchas hit and resolved, worth recording:**
- Ran against an isolated, throwaway workspace server (own port, own
  scratch `--docs-dir`) rather than the real one — a live desktop app +
  its own workspace server were already running against the real
  `workspace-docs` directory on this machine at the time, and this
  avoided any risk of visibly cluttering that live session.
- `read_image`'s path validation (`pkg/workspace/execute_shell_command.go`'s
  `workspaceDocsRoots()`) and `image_gen`/`image_edit`'s
  (`video_gen_tools.go`'s `workspaceDocumentRoots()`) both already respect
  a `WORKSPACE_DOCS_PATH` env var override -- no code change was needed to
  point them at the scratch docs dir; the tests just weren't setting it.
- All three tools require an explicit folder-guard grant via context, and
  `FolderGuardReadPathsKey` alone is inert: `resolveEffectiveFolderGuard`
  in `pkg/workspace/client.go` only consults it as a supplement once
  `FolderGuardAllowedWriteFolderKey` (or `FolderGuardWritePathsKey`) is
  *also* present to select that resolution branch at all -- even an empty
  `[]string{}` write grant is enough to trigger it. Without both keys set,
  every call fails closed with "no workspace read paths were granted"
  regardless of the client-level `FolderGuardConfig`.

**Live results:**
- `image_gen`/codex-cli: **PASS** (52s).
- `image_edit`/codex-cli: **PASS** (102s) -- the first successful
  end-to-end verification of this tool in this repo's history; it had
  zero test coverage of any kind before this (not even the manual
  `cmd/testing/image_gen_providers.go` operator CLI covers editing, only
  generation).
- `read_image`/codex-cli: **PASS**, but only after fixing the test's model
  ID -- the hardcoded `gpt-5.4-mini` default (also used by
  `cmd/testing/read_image_providers.go`'s own `defaultReadImageProviderModel`,
  which is very likely equally stale today) is now deprecated; codex
  responds with a "switch to GPT-5.6 Luna" prompt instead of answering.
  Fixed by using `codex-cli` (codex's own current default) instead of a
  pinned model ID.
- `read_image`/cursor-cli: **FAIL**, a real, live-observed finding, not a
  test artifact. Without an explicit working directory (which
  `wrapReadImageWithLLM` in `workspace_advanced_tools.go` never sets, for
  any provider -- confirmed by reading the code: its `GenerateContent`
  call passes zero options), cursor-agent's own agentic judgment shelled
  out with `find ... -iname '*.jpg' -o ...` searching for the image
  instead of opening the given absolute path directly, and that shell
  command hit a "Not in allowlist: find" gate. This is a distinct issue
  from the `beforeReadFile`-hook bug already fixed in `multi-llm-provider-go`
  commit `7a0e17d` -- that fix was scoped to the `WithDenyBuiltinTools`
  lockdown path; this reproduces on the *default*, non-locked-down path.
- `read_image`/claude-code: **inconclusive**. Hung past its own 2-minute
  per-call `context.WithTimeout` inside
  `ClaudeCodeInteractiveAdapter.waitForTmuxPrompt`, and the test binary's
  outer timeout eventually killed the whole process rather than the
  context cancellation stopping just that call. Not root-caused --
  unclear whether context cancellation genuinely doesn't propagate into
  that tmux wait loop, or whether this run's tmux session state was
  simply stuck for an unrelated reason.

**Not yet done:** root-causing the cursor-cli and claude-code findings
above; vertex (no Vertex/Gemini credentials configured in this
environment, so every vertex subtest skips by design, matching
`cmd/testing/image_gen_providers.go`'s own skip-reason logic).
