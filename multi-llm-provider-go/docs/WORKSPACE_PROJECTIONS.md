# Workspace Projections

Optional, per-session projection of orchestrator-supplied configuration into
each coding CLI's project-conventional on-disk format. Pairs with the existing
`-c`-flag / `--mcp-config` / env-var injection paths — the projections are
*additive workspace visibility*, not a replacement for the primary injection.

Owned by `WithWriteProjectInstructionFile(enabled bool)` on each adapter; ON
by default; byte-restore of any pre-existing operator content at session
teardown. Pass `false` to opt out for repos where you want to preserve
operator-authored content even across crash windows.

## 0. Chat vs. workflow split (read this first)

Coding-CLI sessions split into two modes that have different
relationships with the workspace:

| Mode | cwd | Resume? | Risk of operator-file mutation? |
| :--- | :-- | :------ | :----------------------------- |
| Multi-agent chat / builder chat | user's workspace | yes (native CLI resume) | yes — that's the desired UX (agent edits the user's files directly) |
| Workflow step | fresh `os.MkdirTemp("", "mlp-cli-session-*")` (Phase C of [WORKFLOW_STEP_ISOLATION.md](./WORKFLOW_STEP_ISOLATION.md)) | no (each step is a fresh conversation) | no — model has no path to operator files via built-ins |

Several of the "risks" called out in this doc (crash-window
operator-content destruction, single-file collision races,
`apply_patch` escape on codex) **DO NOT APPLY in workflow mode**
because the cwd is a fresh disposable tmp dir, not the user's
actual workspace. They still matter for chat mode where the
agent operates directly on user files.

Where a specific risk has a chat-mode caveat that's resolved by
workflow-step isolation, this doc flags it inline.

## 1. Why project at all

The orchestrator already injects every supported configuration through CLI
flags or env vars:

- system prompt → `--system-prompt-file` / `GEMINI_SYSTEM_MD` / `-c model_instructions_file` / in-prompt prefix
- MCP servers → `--mcp-config` / `-c mcp_servers.*` / `--include-directories`
- deny hooks → `WithDenyBuiltinTools` (cursor) /
  per-CLI hook config dropped by the adapter

These primary paths work and are the load-bearing mechanism. The workspace
projection is useful when:

- A downstream tool reads the on-disk convention directly (linters,
  IDE plugins, other agents inspecting the workspace).
- An operator auditing a running session wants to see the prompt /
  MCP config / hooks at the conventional path without spelunking
  through tmux capture or temp files.
- A CLI's hook/permission system *only* loads from on-disk config.

ON by default; the workspace-write risk surface (crash-window during
write/restore, discovery prompts on some CLIs) is bounded by byte-restore
on clean teardown. Pass `false` to opt out per-adapter when you'd rather
preserve operator-authored content unconditionally.

## 2. The Matrix

What lands in `<workingDir>` when `WithWriteProjectInstructionFile(true)` is
set, for each CLI. *Safe* drops are unconditional under the flag; *Unsafe*
drops require also setting `MLP_ENABLE_UNSAFE_WORKSPACE_PROJECTIONS=1` —
see [Section 4](#4-the-mlp_enable_unsafe_workspace_projections-gate).

| CLI         | Instruction file                            | MCP config                                      | Deny-builtin hook                                                     |
| :---------- | :------------------------------------------ | :---------------------------------------------- | :-------------------------------------------------------------------- |
| Claude Code | `CLAUDE.md`                                 | `.mcp.json` *(unsafe)*                          | n/a (use `--allowed-tools` / `--disallowed-tools`)                    |
| Codex       | `AGENTS.md`                                 | `.codex/config.toml` ([mcp_servers.*])          | n/a — use `WithDisableShellTool` / `WithDisableFeatures` CLI flags    |
| Gemini      | `GEMINI.md`                                 | `.gemini/settings.json` (merged with hooks)     | `.gemini/settings.json` `hooks.BeforeTool` + `.gemini/hooks/deny-builtin.sh` |
| Cursor      | `.cursor/rules/mlp-system.mdc`              | `.cursor/mcp.json`                              | `.cursor/hooks.json` + `.cursor/hooks/mlp-deny-builtin.sh`            |
| Pi          | n/a (`--append-system-prompt`)              | private session `mcp.json` via `PI_CODING_AGENT_DIR` | n/a — use Pi's `--no-builtin-tools` flag                         |

### Notes per CLI

- **Claude Code's `CLAUDE.md`** is a single root-file convention. The
  adapter captures any pre-existing operator `CLAUDE.md` bytes into the
  process-wide restore registry (`claudeProjectFileRestores`) and writes
  them back at session teardown. One chat owns a workdir at a time, so
  the fixed filename is intentional — no nonce.
- **Cursor's `.cursor/rules/mlp-system.mdc`** is a fixed-filename file
  under the tool-specific rules dir, removed at session teardown. The
  one-chat-per-workdir assumption applies.
- **Pi's native `mcp.json`** is written under a private session agent
  directory and read by `-e builtin:mcp` using `PI_CODING_AGENT_DIR`.
  Managed launches pass `--no-approve` to ignore project MCP overrides;
  explicitly supplied `--skill` paths still load. The file is
  removed at session teardown. A legacy platform-owned `.pi/mcp.json` is
  removed before launch so it cannot carry stale bridge credentials or trigger
  the adapter's migration warning. Bridge-only mode uses Pi's native
  `--no-builtin-tools` flag, so no denial hook file is projected.
- **`AGENTS.md` / `GEMINI.md` / `CLAUDE.md`** are single-file
  conventions. Byte-restore captures any pre-existing operator content
  and writes it back at session teardown. If the orchestrator process
  crashes between write and restore, the operator's file is destroyed —
  single-file conventions inherently carry this crash-window risk.

  **In workflow mode** ([Section 0](#0-chat-vs-workflow-split-read-this-first)),
  cwd is a fresh `os.MkdirTemp` dir. There's no pre-existing operator
  content to destroy in the tmp dir, so the crash-window risk is N/A —
  if the orchestrator crashes, the tmp dir is just an orphaned
  scratch space the OS will reap. The risk only applies in chat
  mode where the cwd is the user's actual workspace.

## 3. Lifecycle

For every projected file, the adapter:

1. **Read pre-existing content** at the target path. If the file
   exists, capture its bytes into a per-session restore registry. If
   it doesn't, record "no prior".
2. **Write the orchestrator content** to the path.
3. **Run the session.** The CLI sees the on-disk content alongside whatever
   the primary injection path already provided.
4. **At teardown**, restore: if there was prior content, write it back
   byte-for-byte. Otherwise, `os.Remove` the file we created. Empty
   directories we created (`.codex/hooks/`, `.gemini/hooks/`) are removed
   on a best-effort basis.

Teardown runs from three places — all three must honor the restore
registry, or the byte-restore promise breaks:

- The normal end-of-session path (`closeXxxPersistentSession`).
- The error path during launch (`cleanupFailedXxxInteractiveSession`).
- The process-wide global cleanup (`CleanupXxxCLIInteractiveSessions`),
  called from test teardown and graceful shutdown.

A bug in commit `3fff052` was that codex's global cleanup path
skipped the new field; it's wired in all three paths now.

## 4. The `MLP_ENABLE_UNSAFE_WORKSPACE_PROJECTIONS` gate

Two specific drops trigger startup prompts on real CLI binaries that the
tmux adapter cannot dismiss. Both are disabled by default and gated
behind this env var:

### Claude `.mcp.json`

Dropping `<workingDir>/.mcp.json` triggers Claude Code v2.1.150's
"New MCP server found in `.mcp.json` — approve?" interactive prompt at
startup. The tmux adapter cannot dismiss it; the session times out.

**Attempted fixes that don't work on v2.1.150:**

- Pre-populating `~/.claude.json` `projects.<dir>.enabledMcpjsonServers`
  with the server names. The field exists, gets written, but Claude
  still shows the prompt.
- Also populating `projects.<dir>.mcpServers` with the full server
  config (so there's no diff between disk and known state). Same
  outcome.
- Using `--strict-mcp-config` so Claude ignores everything except
  `--mcp-config` files. The discovery prompt still fires because Claude
  *sees* `.mcp.json` even when it would have ignored its contents.

The `--mcp-config <temp>` + `--strict-mcp-config` flags already load
the MCP servers without triggering the prompt, so disabling this
projection loses only the workspace-visibility belt-and-suspenders,
not the actual MCP loading.

### Codex `.codex/hooks.json` (REMOVED, not unsafe)

The `.codex/hooks.json` projection was **removed entirely** rather
than gated, because codex has first-class `--disable <feature>` CLI
flags that cover the deny-builtin lever without needing a hook
script at all.

The pre-baked deny list lives at `codexBridgeOnlyDisabledFeatures`
in `pkg/adapters/codexcli/options.go` and covers `shell_tool`,
`unified_exec`, `tool_search`, `multi_agent`, `apps`, `browser_use`,
`browser_use_external`, `computer_use`, `image_generation`,
`workspace_dependencies`, `hooks`, `plugins`, and
`unavailable_dummy_tools`. Passing those as flags via
`WithDisableShellTool` / `WithDisableFeatures` is strictly cleaner
than dropping a hook script — no SHA-keyed trust prompt to dismiss,
no per-session auto-dismiss flakiness, works on first invocation in
a fresh tempdir.

**Notably missing from `--disable`**: `apply_patch`. Codex does not
expose its file-edit tool as a feature flag, so `WithDisableShellTool`
+ `WithDisableFeatures` cannot block `apply_patch`. We close this
gap STRUCTURALLY rather than with hooks:

- The mcpagent codex integration pins `WithCodexSandbox("workspace-write")`
  on every codex call. With workspace-write, codex's sandbox confines
  BOTH `shell` AND `apply_patch` writes to the session's cwd, rejecting
  writes to absolute paths outside.
- In workflow mode ([Section 0](#0-chat-vs-workflow-split-read-this-first)),
  cwd is a fresh `os.MkdirTemp` dir, so `apply_patch` can only touch
  the tmp dir — the user's actual workflow files are unreachable via
  built-ins.
- In chat mode, cwd is the user's workspace dir, so `apply_patch`
  edits the user's files — that's the desired "agent edits my files"
  UX.

This is why we do NOT re-add the codex hooks projection: the
sandbox+cwd combo already provides the security property the hooks
would have, without the trust-prompt churn. See
[WORKFLOW_STEP_ISOLATION.md](./WORKFLOW_STEP_ISOLATION.md) §8 for
the dropped-Phase-D explanation.

Historical context (for future readers wondering why this section is
notably shorter than the others): an earlier version of this
projection did drop `.codex/hooks.json` + `.codex/hooks/deny-builtin.sh`,
gated behind `MLP_ENABLE_UNSAFE_WORKSPACE_PROJECTIONS=1` because it
triggered codex v0.131.0's hook trust review screen that the tmux
adapter couldn't reliably auto-dismiss. The auto-dismiss code in
`waitForCodexPrompt` survives (it's defensive — operators may have
their own `.codex/hooks.json` we shouldn't trip on), but the
projection itself is gone.

### Opting back in

Set the env var when you have a way to dismiss the prompts:

```bash
export MLP_ENABLE_UNSAFE_WORKSPACE_PROJECTIONS=1
```

Examples of "a way to dismiss":

- A custom post-launch `tmux send-keys` that sends Enter or `t` after
  detecting the prompt's distinctive output.
- A newer CLI version that suppresses the prompt with the existing
  flag.
- A non-interactive call path (`-p` / `exec` mode) where the prompt
  is skipped automatically.

## 5. What's NOT gated

These drops have been confirmed to NOT trigger discovery prompts on
their live CLIs and ship enabled-by-default under the flag:

- **Cursor's `.cursor/mcp.json`** — cursor handles this via
  `--approve-mcps` which auto-accepts the in-TUI consent dialog.
- **Cursor's `.cursor/hooks.json`** — cursor reads silently.
- **Claude's `CLAUDE.md`** — Claude Code's memory layer auto-loads the
  root-level project-instructions file at startup with no discovery
  prompt.

## 6. Test coverage

### Unit (every adapter)

- Lifecycle tests for each helper: fresh-workspace write+remove,
  pre-existing-operator-content write+byte-restore, empty-workingDir
  no-op.
- Format invariants: codex TOML emitter (key ordering, bare vs quoted keys and
  env subtable), plus cursor hook projections preserving their
  schema.

### Real-CLI E2E (gated per CLI)

| CLI         | Env var                                            | Status |
| :---------- | :------------------------------------------------- | :----- |
| Claude Code | `RUN_CLAUDE_CODE_TMUX_INTEGRATION=1`       | PASS   |
| Codex       | `RUN_CODEX_CLI_REAL_E2E=1`                          | PASS   |
| Cursor      | `RUN_CURSOR_CLI_REAL_E2E=1`                         | covered by older `cursorcli_deny_builtin_hooks_test.go` |

Each lifecycle E2E pre-seeds an operator-owned file, runs a real
adapter call with the flag on, force-cleans up if persistent-session
mode is in use, and asserts:

- Operator file content is byte-for-byte restored.
- mtime advanced past the pre-seed (proves the writer fired
  mid-session — without this assertion, byte-restore could pass via
  the null hypothesis "writer never ran").
- For codex specifically: `.codex/hooks.json` was NOT created
  (proves the removed hooks projection didn't sneak back in).

### What's NOT covered yet

- **Behavioral deny-hook verification for CLIs.**
  Equivalent behavioral tests for codex/gemini/cursor still
  don't exist; the missing piece for those CLIs is "the CLI honors
  the deny config we drop", as distinct from "the file we wrote
  lands in the right place."

## 7. Adding a new CLI

Follow the established shape:

1. Add a `MetadataKeyWriteProjectInstructionFile` constant to the
   adapter's `options.go`.
2. Add `WithWriteProjectInstructionFile(enabled bool)` option function.
3. Add a `writeXxxProjectArtifacts` helper that composes individual
   writers per artifact. Each individual writer captures any
   pre-existing operator content into a per-session restore registry
   and returns a cleanup func.
4. Compose all individual cleanups into a single composite cleanup
   stored on the session struct (`projectInstructionCleanup func()`).
5. Wire the composite cleanup into all teardown paths:
   `closeXxxPersistentSession`, `cleanupFailedXxxInteractiveSession`,
   `CleanupXxxCLIInteractiveSessions`. **Missing any one of these is
   a latent destroy-operator-content bug.**
6. Add a public re-export `WithXxxWriteProjectInstructionFile` in
   `providers.go`.
7. Add unit tests for each writer (fresh + restore + empty no-op) and
   one composite lifecycle test.
8. Add a real-CLI E2E gated by `RUN_XXX_CLI_REAL_E2E=1` that
   pre-seeds, runs, force-cleanups if persistent, and asserts
   byte-restore + mtime advance.
9. If the CLI shows a startup prompt on launch with the projection
   in place, gate the problematic drop behind
   `MLP_ENABLE_UNSAFE_WORKSPACE_PROJECTIONS` and document why in the
   adapter comment and in [Section 4](#4-the-mlp_enable_unsafe_workspace_projections-gate)
   above.
10. Update [Section 2](#2-the-matrix) and [Section 6](#6-test-coverage)
    of this doc.
