# Workflow step runtimes with linked outputs

Status: implemented in mcpagent and AgentWorks, 2026-09-30; not deployed.
The provider adapters continue to receive a private CLI working directory.
Workspace placement does not itself enforce access or enable native tools.

## Current runtime contract

- `mcpagent.WorkspaceRuntimeConfig.IsolatedSession` selects a private directory
  keyed by the platform session ID. The cwd survives between turns for native
  resume; `CloseSession` reclaims it. Without a session ID, `Agent.Close` reclaims
  a random runtime. Generated prompts, skills and provider config stay here.
- Trusted application code may supply `WorkspaceRuntimeConfig.OutputDir`, an
  existing absolute artifact directory. It is linked as `output/` inside the
  private runtime. AgentWorks supplies the exact current iteration/group/step
  directory, including nested and message-sequence overrides. Deliverables
  written through the link are authoritative immediately, without copying.
- AgentWorks checks the target against the dedicated step session's write guard
  before creating it. Its native security policy replaces parent-chat workspace
  grants with that step's admitted grants. The CLI home is also per runtime;
  existing DB/cache/KB/owning-subtree and host capabilities are preserved.
- Link setup is checked on construction and before each provider integration.
  Obstructing files/directories, wrong targets and state errors fail; linked
  runtimes never fall back to the real working directory. Resume keeps the
  same link target, and cleanup removes the link without following its target.
- Native tools use `output/<file>` or `cd output`; bridge paths and
  `STEP_OUTPUT_DIR` remain unchanged. This does not enable native editing/shell
  tools, change `mcp_only` defaults or broaden permissions. All six coding
  providers, including Agy, share the setup. API models and background reviews
  do not receive a link implicitly from an inherited output environment.
- Crew and workflow chats have separate Run/Builder runtimes with `project/`
  linked to their real project. Code chats keep their existing placement.

The security controls below still apply. Sections 1–9 preserve the historical
proposal and rollout notes; references there to per-call directories, no resume,
no symlinks, a shared chat cwd or old public option APIs are superseded by this
current contract.

## 0. Scope: four controls that must not be conflated

A working directory, a tool allowlist, an approval classifier, and an OS
sandbox solve different problems:

1. **Workspace placement** selects the directory in which the CLI starts. A
   per-step temporary directory prevents projection-file collisions, but `cwd`
   alone does not prevent a native tool from opening an absolute path or using
   `..` to leave that directory.
2. **Tool mode** controls which tool implementations the model can call:
   `mcp_only`, `hybrid`, or `native_only`.
3. **Approval mode** controls whether the provider automatically allows,
   classifies, prompts for, or denies a proposed tool action. For example,
   current Claude Code supports `--permission-mode auto`; this is safer than
   `--dangerously-skip-permissions`, but it is a policy classifier rather than
   a filesystem boundary.
4. **Security mode** controls the host resources visible to the entire coding
   CLI process: `compatibility`, `verified`, or `isolated`.

The reusable configuration vocabulary is:

| Setting | Values | Purpose |
|---|---|---|
| `agent_tools.mode` | `mcp_only`, `hybrid`, `native_only` | Select bridge tools, native tools, or both. |
| `approvals.mode` | `provider_auto`, `approve_all` | Use the provider's guarded automatic reviewer or explicitly skip approval review. |
| `security.mode` | `compatibility`, `verified`, `isolated` | Select normal host access, enforced approved paths, or a private home/account environment. |

`provider_auto` maps to Claude Code `--permission-mode auto`, Cursor
`--auto-review`, and Codex `--ask-for-approval untrusted` with
`approvals_reviewer="auto_review"`. `approve_all` maps to Claude Code
`--dangerously-skip-permissions`, Cursor `--force`, and Codex
`--ask-for-approval never`. Approval mode must not change security mode: in
particular, Codex `--dangerously-bypass-approvals-and-sandbox` is not a valid
implementation of `approve_all` because it also removes the sandbox.

`mcp_only` sends file and shell work through AgentWorks executors, where
FolderGuard and shell sandboxing apply. `hybrid` gives the provider native
Read/Edit/Write/Shell tools for ordinary project work while retaining MCP for
application capabilities. `native_only` is mainly diagnostic because it cannot
use application capabilities exposed only through MCP.

### Design direction under consideration: preserve the provider harness

This section records architectural reasoning, not an implemented policy change.
The current workflow-step defaults and fail-closed behavior described below
remain in force until the security and provider-contract work is complete.

An end user does not care whether a result was produced by a provider-native
tool or an MCP tool. The user-visible contract is that input is accepted
promptly, progress streams continuously, the agent performs the task well, and
exactly one clear final response arrives. Tool transport is an implementation
detail and should be selected according to how reliably it satisfies that
contract.

Codex, Claude Code, Cursor, and similar products are not only model endpoints.
Their harnesses contribute tool selection and sequencing, file search and
editing, context management, compaction, failure recovery, and provider-aware
interpretation of tool results. Disabling most native tools and recreating the
same operations through an MCP bridge can reduce the capability of the complete
agent even when the underlying model is unchanged. It can also introduce extra
tool discovery, larger schemas and receipts in context, additional process
round trips, provider-to-bridge translation failures, and repeated discovery
that the native harness would otherwise retain or handle directly.

Conversely, MCP remains the correct boundary for AgentWorks-owned and
privileged capabilities: workflow control, durable platform state, human
decisions, notifications, secrets, external actions, and dynamic product APIs.
Those capabilities need stable authorization, auditability, and provider-neutral
contracts. A product may expose dynamic capabilities through `get_api_spec`, or
stable capabilities as direct MCP tools; neither choice requires ordinary file
and project work to use MCP as well.

The longer-term direction to evaluate is therefore:

1. Preserve the provider's native harness and native tools for ordinary
   workspace reasoning, search, editing, and local verification.
2. Add AgentWorks capabilities through MCP or a discovered product API surface.
3. Normalize provider output in mcpagent into one user-facing lifecycle:
   accepted input, streaming text and tool activity, one final assistant
   response, and one terminal turn-completion event.
4. Enforce security around the entire coding-agent process and independently at
   each privileged AgentWorks capability, rather than relying on disabling the
   provider harness as the primary boundary.
5. Retain `mcp_only` as a safe fallback for providers or environments where
   whole-process isolation has not been verified.

This makes `hybrid` the intended capability model once its process boundary is
certified, while avoiding an architectural-purity requirement that every
operation cross MCP. It also keeps skill exposure explicit: approved skills
must be projected into the session or served by a managed skill reader. Hybrid
mode is not justification for granting broad access to user configuration roots
such as `~/.codex` or `~/.claude`.

Before changing defaults, this direction needs live cross-provider evidence for
streaming continuity, final-response delivery, session reuse, task quality,
latency, cost, and sandbox escape prevention. Until then, this is a recorded
design hypothesis rather than a rollout decision.

### Product API transport for hybrid profiles

A product exposing custom capabilities through `get_api_spec` needs a way to
*invoke* them, not only discover them. Two transports exist, selected by the
profile's `runtime.api_transport.mode`:

- **`bridge_shell`** — the agent calls the endpoint through AgentWorks'
  guarded `execute_shell_command` executor. For `mcp_only` products, which have
  no native shell of their own.
- **`native_shell`** — the provider's own Bash calls the endpoint using a
  session-scoped environment this library admits and overlays onto the child
  process. No AgentWorks shell executor is exposed.

What this library owns is the second half of that: which environment keys may
cross into a coding-agent subprocess. That policy has exactly one
implementation, `llmtypes.IsScopedCodingAgentEnvironmentKey`, admitting
`SECRET_*`, `VAR_*`, and the closed MCP route set. `WithCodingAgentSecretEnvironment`,
`CodingAgentSecretEnvironmentFromOptions`, and the subprocess merge all consult
it, and mcpagent calls it rather than keeping its own list — a second list one
layer down is what produced `MCP_CUSTOM_set=no`, where the routes were created
and then silently filtered before the child ever saw them.

One inference to avoid, because it has already misled a review: `get_api_spec`
answering proves nothing about whether product tools are callable. It is an
mcpagent virtual tool, injected separately from bridge tools. Discovery working
is not evidence that invocation works.

Which transport a profile should choose, why a hybrid profile does not also
take the bridge, the failure modes when a transport is unconfigured, and the
requirement that the system prompt name the route are AgentWorks concerns and
live with the profile policy — see
`docs/design/native_coding_agent_environment_policy.md` in that repository.
Restating them here is how the two would drift.

The shell bridge remains subject to the AgentWorks folder guard and shell
sandboxing. It must not be used to print secret values. Prefer the Secret UI
for entering a value; if a secret-management endpoint is called through the
bridge, its payload must be redacted from debug and error logs.

In `compatibility` mode, native tools run in the provider's normal host
environment. MCP calls remain guarded, but AgentWorks FolderGuard does not
constrain native tools. `hybrid + compatibility` is therefore a legitimate,
explicit risk-accepting option for a trusted local developer who values native
agent capability over hard folder isolation. The UI and configuration must
state that the coding provider may access files outside the project.

In `verified` mode, the sandbox wraps the entire coding CLI process and grants
only approved workspace plus required runtime/auth paths. Native tools and
their child processes inherit that boundary. `isolated` adds a private home and
separate account environment. A system prompt, provider action classifier, or
working directory cannot substitute for either boundary.

Strict whole-process enforcement is currently available only for Codex on
supported macOS hosts. Claude Code, Cursor, and Pi require independent
sandbox/credential-path certification. A request for `verified` or `isolated`
with an uncertified provider must fail closed or explicitly fall back to
`mcp_only`; it must never silently become unsandboxed hybrid mode.

Example local, risk-accepting interactive configuration:

```yaml
agent_tools:
  mode: hybrid
approvals:
  mode: provider_auto
security:
  mode: compatibility
```

Example future preferred configuration after provider certification:

```yaml
agent_tools:
  mode: hybrid
approvals:
  mode: provider_auto
security:
  mode: verified
```

Workflow steps remain `mcp_only` by default. Their per-session temporary
directory prevents configuration collisions; their guarded MCP executors
provide the actual workspace access policy. Native tools should be enabled for
a workflow step only when the whole CLI process has a stage-specific verified
or isolated boundary.

## 1. Why

### 1.1 The collision problem we have today

All coding-CLI sessions — chat AND workflow steps — currently run with
`CodingAgentWorkingDir` set to the user's workspace dir. The
WithWriteProjectInstructionFile pattern (shipped earlier this week)
projects per-session files into that dir:

- Claude Code: `CLAUDE.md`, `.mcp.json` (gated)
- Codex:       `AGENTS.md`, `.codex/config.toml`
- Cursor:     `.cursor/rules/mlp-system.mdc`, `.cursor/mcp.json`, `.cursor/hooks.json`
- Gemini:     `GEMINI.md`, `.gemini/settings.json`, `.gemini/hooks/...`
- Antigravity: `.agents/rules/mlp-system.md`, `.agents/mcp_config.json`, `.agents/hooks.json`

All instruction-file paths are now fixed-name single-file conventions
(`CLAUDE.md`, `AGENTS.md`, `GEMINI.md`, `mlp-system.mdc/md`) under the
"one chat owns the workdir at a time" assumption. The same single-file
race exists for the MCP / hooks files alongside.

When two workflow steps run concurrently (or back-to-back in
persistent-session mode) against the same workspace:

1. **Single-file race**: step A writes `AGENTS.md`, step B writes
   `AGENTS.md` mid-flight. Last-write-wins; the earlier step's
   content vanishes.

2. **Byte-restore corruption**: step A captures pre-existing
   operator content into its restore registry. Step B then writes
   over the file. Step A's cleanup runs first and "restores" — but
   the bytes it captured may already be step B's content, not the
   operator's original. Restore order matters and gets it wrong.

3. **Cross-CLI confusion**: step A uses Claude (drops `.claude/`),
   step B uses Codex (drops `AGENTS.md`). Steps don't collide
   directly, but the workspace ends up with stale config from both
   agents that the next step sees and tries to interpret.

4. **Crash window**: if the orchestrator process dies between write
   and cleanup, operator-owned single-file content (`AGENTS.md`,
   `GEMINI.md`) is destroyed. This is the documented caveat behind
   `MLP_ENABLE_UNSAFE_WORKSPACE_PROJECTIONS` for claude `.mcp.json`
   and (previously, now removed) codex `.codex/hooks.json`.

### 1.2 Why chat doesn't share this problem

Chat sessions:

- One user, one workspace, one session at a time per workspace
- Long-lived persistent sessions (resume tied to dir)
- User EXPECTS the agent to edit files in their actual workspace
  (the whole point of running a coding assistant)

Workflow steps:

- N steps, often concurrent, against the same workflow dir
- Stateless per step (no resume needed across steps)
- Steps should NOT directly edit the workflow dir's actual files;
  the MCP bridge is the orchestration contract for file changes

## 2. The Decision Tree

| Use case | Working dir | Resume? | Action |
|---|---|---|---|
| 1. Multi-agent chat | User's workspace | Yes (native CLI resume) | **No change** |
| 2. Builder chat | User's workspace | Yes | **No change** |
| 3. Workflow steps | Per-step tmp dir | **No** | **New behavior: tmp-dir isolation** |

The orchestrator already knows which mode it's running in. The new
mcpagent option flags the call as "isolated workspace required."

## 3. Public API Shape

### 3.1 New mcpagent agent option

```go
// WithIsolatedSessionWorkspace asks the coding-CLI session to run in a
// fresh per-call tmp dir instead of CodingAgentWorkingDir. When set:
//
//   - The agent creates a new os.MkdirTemp("", "mlp-cli-session-*") dir
//     before launching the CLI session.
//   - The CLI's cwd / --dir / --cd / WithXxxWorkingDir is overridden to
//     that tmp path.
//   - The tmp dir is rm -rf'd when the session completes (success or
//     failure).
//   - The MCP bridge config (which already carries the actual workflow
//     dir paths in its env / args) is unchanged — the bridge subprocess
//     runs outside the CLI's sandbox so it can still touch the user's
//     workflow dir for file ops the model invokes via bridge tools.
//
// Intended for workflow-step calls where resume is never needed and
// pollution + concurrent overlap is a real risk. Chat calls should not
// set this; they need persistent state on the user's actual workspace.
func WithIsolatedSessionWorkspace(enabled bool) AgentOption {
    return func(a *Agent) {
        a.IsolatedSessionWorkspace = enabled
    }
}
```

### 3.2 Agent struct field

```go
type Agent struct {
    // ...
    CodingAgentWorkingDir       string
    IsolatedSessionWorkspace    bool  // NEW
    // ...
}
```

### 3.3 Workflow orchestrator wires it

In `mcp-agent-builder-go/agent_go/pkg/orchestrator/agents/workflow/step_based_workflow/...`
or equivalent, where a step's agent is built, append:

```go
agentOpts := []mcpagent.AgentOption{
    // ... existing options ...
    mcpagent.WithIsolatedSessionWorkspace(true),
}
```

Chat code paths in `mcp-agent-builder-go/agent_go/pkg/agentwrapper/llm_agent.go`
do NOT set this option.

## 4. Per-CLI Implementation

The isolation logic lives in mcpagent's option-appending layer (e.g.
`appendCodingAgentWorkingDirOptionForProvider` or a new sibling),
NOT in each adapter. The pattern:

1. If `IsolatedSessionWorkspace` is true:
   - Create `tmpDir := os.MkdirTemp("", "mlp-cli-session-*")`
   - Override the working-dir option for the active provider with
     `tmpDir`
   - Register a deferred cleanup: `defer os.RemoveAll(tmpDir)` at the
     call-site that owns the session lifecycle

2. Provider-specific options for sandbox confinement (extra defense
   in depth):

| CLI | Sandbox flag | Effect |
|---|---|---|
| Claude Code | `--permission-mode dontAsk` (already set) | Already permit-all within workspace |
| Codex | `--sandbox workspace-write` | Confines apply_patch + shell to cwd (tmp dir). Outside writes blocked. |
| Cursor | `--force` (already set) | Already permit-all |
| Gemini | `--approval-mode yolo` (already set) | Already permit-all |

For codex specifically: setting `--sandbox workspace-write` AND
`cwd = /tmp/foo` means even if the model invokes `apply_patch`
against `/Users/x/workflow/main.go`, codex's sandbox rejects the
write because the path is outside cwd. **This is the security
property we want**: the model has NO way to mutate the user's
workflow dir except via the MCP bridge.

## 5. What Goes Away (or Becomes Simpler)

When `IsolatedSessionWorkspace` is true:

- **Byte-restore complexity**: every file in the tmp dir is ours.
  `rm -rf tmp` is the entire cleanup. No restore registry, no
  pre-existing-content capture, no LIFO ordering.

- **`MLP_ENABLE_UNSAFE_WORKSPACE_PROJECTIONS` gate becomes
  unnecessary for workflow mode**: the .mcp.json discovery prompt
  (claude) and hook trust prompt (codex) still fire, but they fire
  in a FRESH tmp dir — no operator content to destroy, so the
  "unsafe" framing doesn't apply. The auto-dismiss code in
  `waitForCodexPrompt` can stay for cases where the prompt slips
  through.

- **Codex hooks projection can return**: the reason we removed it
  (trust prompts + apply_patch can't be CLI-disabled) is moot when
  the workspace is fresh and the SHA is constant. Codex's
  per-SHA trust cache will hit after the first ever invocation in
  the user's `~/.codex/config.toml`. We could even pre-write the
  `[hooks.state]` block (the task #31 follow-up I deferred) and
  have zero prompts ever.

- **All the file-collision risk goes away** by construction. Two
  steps with different tmp dirs literally cannot collide.

What STAYS the same:

- The chat code path (multi-agent + builder) is untouched.
- Per-CLI byte-restore code is still needed for chat — operators
  WILL have pre-existing files in their workspace and the
  projection must restore them.

## 6. Test Plan

### 6.1 New unit tests

- `TestAgentWithIsolatedSessionWorkspaceCreatesAndCleansUpTmpDir`:
  the option creates a tmp dir, passes it through as the cwd
  option, and rm -rf's it after the call.
- `TestAgentWithIsolatedSessionWorkspaceDoesNotTouchCodingAgentWorkingDir`:
  pre-seed `CodingAgentWorkingDir` with files, confirm none are
  touched (read, written, or deleted) when isolation is on.

### 6.2 Updated existing tests

- Project-artifacts E2Es (claude/codex/gemini) currently
  pre-seed operator content in `t.TempDir()` and verify
  byte-restore. With isolation on, byte-restore wouldn't trigger
  (no operator content in the fresh tmp dir). Add a parallel test
  per CLI that exercises the isolation path: `RealProjectArtifactsLifecycleIsolated`.

### 6.3 New workflow-level E2E

- `TestWorkflowStepIsolationPreservesUserWorkflowDir`: run a
  workflow step against a workspace dir containing operator files
  (`README.md`, `.cursor/mcp.json` with operator content, etc.).
  Assert NONE of those files have been modified or touched after
  the step completes. The model can only see them via MCP bridge.

### 6.4 Concurrent-step regression test

- `TestConcurrentWorkflowStepsDoNotCollide`: fire two workflow
  steps against the same `CodingAgentWorkingDir` in parallel.
  Each step writes a distinct sentinel to `AGENTS.md` (within its
  tmp dir, so they don't collide). Assert both finish without
  error, both tmp dirs are gone afterwards, and the user's actual
  workspace's `AGENTS.md` (if any) is untouched.

## 7. Open Questions

1. **Cache location**: `os.MkdirTemp("", ...)` puts it in
   `/var/folders/...` (macOS) or `/tmp` (Linux). Workflow steps are
   short-lived, so OS cleanup-on-reboot is fine. But if a step ever
   needs to persist artifacts past the call (debug archive,
   conversation log), we'd need a longer-lived location. Decision:
   `os.MkdirTemp` is fine for v1. Reconsider if any step needs
   persistence.

2. **Cleanup on adapter crash**: if the mcpagent process crashes
   between tmp-dir creation and cleanup, the tmp dir leaks. Tmp
   dirs are OS-cleaned eventually but until then they accumulate.
   Mitigation: a startup scan of `os.TempDir()` for stale
   `mlp-cli-session-*` dirs older than 1h, with rm -rf. Add to
   adapter startup.

3. **MCP bridge env passing**: the bridge needs to know the user's
   actual workflow dir for its file-read/write tools. Currently
   `WithMCPConfig` carries this. Need to confirm: when we override
   the CLI's cwd to tmp dir, the bridge config (which is built
   BEFORE the override) still carries the original workflow dir.
   Should be fine because the bridge is a subprocess spawned by
   the CLI; the CLI doesn't filter env to the bridge.

4. **Symlink trickery**: should the tmp dir contain a symlink to
   the workflow dir for convenience? E.g.
   `tmp/workspace -> /Users/x/workflow`. Operators inspecting the
   running session can poke around. Trade-off: symlinks can
   confuse the model (it sees both `tmp/` and `tmp/workspace/`).
   Decision: NO symlinks. Tmp dir is opaque; MCP bridge is the
   only path to user files.

5. **Persistent-tmux-session workflow steps**: do we want
   persistent sessions for workflow steps? If yes, the tmp dir
   needs to live for the session lifetime, not the call lifetime.
   Currently workflow steps default to NON-persistent
   (`CodexPersistentInteractiveSession=false`,
   `CursorPersistentInteractiveSession=false`, etc.). Decision:
   leave the default; if a workflow step opts into persistent
   mode, isolation lives for the session and is cleaned up at
   session teardown (matches the existing
   `projectInstructionCleanup` pattern).

## 8. Rollout Order

1. **Design alignment** (this doc, awaiting sign-off) — ✅ done
2. **Prototype on cursor** — has clean deny coverage + shared-dir
   lease test for regression catching — ✅ done (Phase A in
   mcpagent commit 6032608, Phase B in
   multi-llm-provider-go commit 955f40e)
3. **Workflow orchestrator wire-through**: flip the flag for all
   workflow-step code paths in mcp-agent-builder-go — ✅ done
   (commit d438a5b1)
4. **Update WORKSPACE_PROJECTIONS.md**: document the chat-vs-workflow
   split explicitly. Note that several "risks" called out there go
   away in workflow mode.
5. **Pin sandbox=workspace-write for codex** in mcpagent's codex
   integration path so apply_patch (which codex has no `--disable`
   flag for) is confined to cwd. Combined with the tmp-dir cwd from
   Phase 3, this closes the apply_patch gap structurally — no hooks
   needed. — ✅ done
6. **Template Phase 2's cursor isolation E2E to other 5 CLIs** as
   regression coverage.

### ~~Dropped~~ — Re-add codex hooks projection for workflow mode

The earlier proposal was: now that the tmp dir is fresh per step,
codex's per-SHA hook trust cache would hit consistently, so we
could safely re-add the `.codex/hooks.json` + deny script
projection (which was removed because of trust-prompt flakiness).
The goal was closing the `apply_patch` deny gap — codex's
`--disable <feature>` flag list does not include `apply_patch`, so
hooks were the only deny lever.

**Dropped because the tmp-dir + sandbox combination closes the gap
structurally without re-introducing hooks**:

- Phase 3 makes cwd a fresh `/tmp/mlp-cli-session-*` per step.
- Codex's `--sandbox workspace-write` (now explicitly pinned in
  Phase 5) confines BOTH `shell` AND `apply_patch` writes to cwd.
- So `apply_patch` can only write to the tmp dir, never the user's
  workflow dir.
- Hooks are no longer necessary for the security property they
  would have provided.

Re-adding hooks would add per-SHA trust-state file management,
write coordination with `~/.codex/config.toml`, and the (admittedly
shrunken) risk surface of misconfigured deny scripts — for zero
incremental safety. Better to leave the hooks projection removed
and rely on the structural confinement.

## 9. Non-Goals

- This is NOT a permission-system redesign. Existing
  WithDenyBuiltinTools / WithBridgeOnlyTools options continue to
  apply when set; isolation is orthogonal.
- This does NOT change chat behavior. Chat users still get the
  "agent edits my files directly" experience.
- This does NOT replace the MCP bridge. The bridge is the
  orchestration contract for file changes; this just narrows where
  the CLI can write to enforce that contract.
