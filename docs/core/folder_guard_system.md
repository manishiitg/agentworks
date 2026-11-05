# Folder Guard System

## Overview

The Folder Guard system is a **fine-grained access control mechanism** that restricts agent file operations to specific directories. It is a critical security boundary that isolates user data, prevents cross-workflow interference, and protects system files.

It provides security boundaries for:
1.  **Simple Mode**: Runtime validation of tool parameters (e.g., intercepting `diff_patch_workspace_file`).
2.  **Code Execution Mode**: AST-level validation + runtime path checking compiled into generated Go code.
3.  **Shell Execution**: Environment sanitization and OS-level filesystem namespace isolation.

**Key Benefits:**
-   Prevents agents from accessing unauthorized directories.
-   Supports separate read and write permission levels.
-   Automatically enhances tool descriptions in the LLM prompt with access restrictions.
-   Provides defense-in-depth via environment sanitization and OS-level isolation.
-   **Cross-platform**: Works on both Linux (Docker namespaces) and macOS (native sandbox).

---

## Architecture & Enforcement Layers

The folder guard system provides **multiple layers of security** (Defense in Depth):

1. **Prompt Injection (Tool Description Enhancement)**: The LLM sees clear restrictions in the descriptions of tools like `execute_shell_command` or `diff_patch_workspace_file`.
2. **Context Injection (`context.Context`)**: Allowed paths (`FolderGuardReadPathsKey`, `FolderGuardWritePathsKey`) are injected into the Go context before the agent executes.
3. **Runtime Validation Wrapper**: A middleware (`WrapWorkspaceToolsWithFolderGuard`) intercepts all file tool calls and validates the `filepath` against the allowed lists.
4. **AST Validation (Code Exec)**: Code execution mode parses the agent's generated code to block direct OS calls.
5. **Environment Sanitization**: No secrets (`DATABASE_URL`, API keys) are leaked to shell subprocesses.
6. **OS-Level Isolation**: Kernel-enforced filesystem restrictions (Linux mount namespaces via `unshare -m`, or macOS `sandbox-exec`).

---

## Operating Modes

The Folder Guard behaves differently depending on the active execution mode:

### 1. Chat Mode
In standard Chat Mode, the agent operates in a shared workspace but is heavily restricted.
- **Write Restrictions**: The agent is usually restricted to writing to the `Chats/` directory or a specific user's chat folder.
- **Read-Only Folders**: The `Workflow/` directory is strictly **read-only** in chat mode. Current workflow agents inspect it through the shell/read side of the workspace bridge; legacy basic file tools are not exposed in normal workflow-builder sessions.
- **Blocked Folders**: The `_users/` directory (which contains authentication data, OAuth tokens, and session history) is **strictly blocked** from all read and write access.

### 2. Multi-Agent Chat
Multi-agent chat sub-agents share the default chat folder guard — there is no per-plan folder scoping any more.
- **Write Restrictions**: Writes are restricted to the standard chat-mode allowed folders: `Chats/`, `Downloads/`, `config/`, `memories/` (plus `skills/custom/` and `subagents/custom/` when the builder tools are active).
- **Read Access**: Sub-agents can read `Chats/`, `Downloads/`, `skills/`, `subagents/`, `Workflow/`, `config/`, `memories/`.
- Scoping a sub-agent's output to a specific sub-folder under `Chats/` is done through the worker's instruction, not via a context flag.

### 3. Workflow Mode
Workflow mode dynamically configures the folder guard for **each individual step** in the graph, providing the highest level of isolation.
- **Execution Folder**: Writes are restricted to the specific step's execution folder (e.g., `runs/iteration-1/user123/execution/`).
- **Learnings**: The agent is granted read access to the `learnings/` folder to retrieve insights from previous steps.
- **Knowledgebase**: If enabled, the persistent `knowledgebase/` folder is added to the read/write paths, allowing data sharing across entirely different workflow runs.

---

## Security Constraints

#### Tool Classification & Rules
| Tool Type | Allowed Paths | Blocked Paths |
| :--- | :--- | :--- |
| **Read Tools** (`execute_shell_command`; legacy basic read tools when explicitly registered by library users) | `readPaths` + `writePaths` (combined) | `blockedPaths` (denied) |
| **Write Tools** (`diff_patch_workspace_file`; legacy basic write tools when explicitly registered by library users) | `writePaths` only | `blockedPaths` (denied) |
| **Shell Tools** (`execute_shell_command`) | Environment sanitized + Filesystem isolated | `blockedPaths` references |

#### Execution Rules
✅ **Allowed:**
-   Paths within configured `readPaths` (read-only access).
-   Paths within configured `writePaths` (read+write access).
-   `Downloads/` folder (always accessible for read+write as a scratchpad).
-   Relative paths resolved against the workspace root.

❌ **Forbidden:**
-   **Read access** to paths outside configured boundaries (returns "file not found").
-   **Write access** to paths outside `writePaths` (returns "permission denied").
-   Directory traversal patterns (`../`).
-   Direct `os` file operations in Code Execution mode.
-   Accessing secrets via `env` or `printenv` in shell.

---

### Per-profile strict mode

An agent profile can ask for a deny-by-default sandbox instead of the
allow-by-default one (`runtime.sandbox.mode: strict` in product.yaml, with
`network: disabled` to cut outbound network). The folder guard paths are the
same; only the isolator profile changes (`security.Isolator.StrictAllowlist`
and `AllowNetwork`), carried in `FolderGuardConfig.StrictAllowlist` /
`DenyNetwork`. See `docs/core/product_yaml_design_guide.md`.

## Threat Model

**Protected Against:**
- ✅ Unauthorized file reads (credential theft from `_users/`, data exfiltration).
- ✅ Unauthorized file writes (data corruption, code injection outside the isolated run folder).
- ✅ Environment variable leakage (API keys, database passwords).
- ✅ Directory traversal attacks (`../../../etc/passwd`).
- ✅ Agent confusion about allowed paths (clear boundaries set in tool descriptions).

**Not Protected Against:**
- ❌ Code execution vulnerabilities in the workspace tools themselves.
- ❌ Time-of-check-time-of-use (TOCTOU) races (paths are validated once at call time).
- ❌ Resource exhaustion (disk space, CPU, memory).
