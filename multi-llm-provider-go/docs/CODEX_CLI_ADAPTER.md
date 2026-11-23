# OpenAI Codex CLI Adapter

## Overview
LLM provider adapter for [OpenAI Codex CLI](https://github.com/openai/codex) (`codex`). Uses the local `codex` CLI as an LLM provider, leveraging its agentic capabilities including shell execution, file editing, web search, and MCP tool support.

## Architecture

### Non-Interactive Execution
The adapter runs `codex exec --json --full-auto "prompt"` as a subprocess and parses JSONL from stdout.

Unlike Claude Code (which uses stream-json stdin for conversation history), Codex CLI takes the prompt as a positional argument. For multi-turn conversations, the adapter builds a text transcript prefixed with role labels.

### CLI Flags
```
codex exec --json \
  --full-auto \
  --model gpt-5.4 \
  --disable shell_tool \
  -c 'approval_policy="never"' \
  -c 'model_reasoning_effort="high"' \
  "prompt text here"
```

### Resume Sessions
```
codex exec resume --json --full-auto <SESSION_ID> "follow-up prompt"
```
Note: resume flags go on the `resume` subcommand, not on `exec`.

## JSONL Event Format

Each line of stdout is a JSON object. Event types:

### Thread Lifecycle
```json
{"type":"thread.started","thread_id":"019cfad1-2cd6-..."}
{"type":"turn.started"}
{"type":"turn.completed","usage":{"input_tokens":21076,"cached_input_tokens":19712,"output_tokens":164}}
{"type":"turn.failed","error":{"message":"..."}}
```

### Agent Messages
```json
{"type":"item.completed","item":{"id":"item_0","type":"agent_message","text":"Hello, world."}}
```
**Important:** Text is in the `text` field (not `content`).

### Command Execution (Shell)
```json
{"type":"item.started","item":{"id":"item_1","type":"command_execution","command":"/bin/zsh -lc ls","aggregated_output":"","exit_code":null,"status":"in_progress"}}
{"type":"item.completed","item":{"id":"item_1","type":"command_execution","command":"/bin/zsh -lc ls","aggregated_output":"file1\nfile2\n","exit_code":0,"status":"completed"}}
```

### File Changes (apply_patch)
No `item.started` — goes directly to `item.completed`:
```json
{"type":"item.completed","item":{"id":"item_1","type":"file_change","changes":[{"path":"/tmp/test.txt","kind":"add"}],"status":"completed"}}
```

### Web Search
```json
{"type":"item.started","item":{"id":"item_1","type":"web_search","query":"","action":{"type":"other"}}}
{"type":"item.completed","item":{"id":"item_1","type":"web_search","query":"search terms","action":{"type":"search","query":"...","queries":["..."]}}}
```

### Errors
```json
{"type":"error","message":"..."}
{"type":"item.completed","item":{"id":"item_0","type":"error","message":"Model metadata for `o3` not found..."}}
```

### Plan/Todo Items
```json
{"type":"item.started","item":{"id":"item_1","type":"todo_list","items":[{"text":"task","completed":false}]}}
{"type":"item.updated","item":{"id":"item_1","type":"todo_list","items":[{"text":"task","completed":true}]}}
```

## Options & Metadata Keys

### Model Selection
- `WithCodexModel(model)` — `--model` flag. Values: `gpt-5.4`, `gpt-5.3-codex`, `gpt-5.5`, etc.
- Default model sentinel: `codex-cli` (lets CLI use its configured default)

### Reasoning Effort
- `WithReasoningEffort(effort)` — `-c model_reasoning_effort="<level>"`
- **gpt-5.4**: `none`, `low`, `medium`, `high`, `xhigh`
- **gpt-5.3-codex**: `low`, `medium`, `high`, `xhigh`
- **gpt-5.5**: `none`, `low`, `medium`, `high`, `xhigh`
- `WithReasoningSummary(summary)` — `auto`, `concise`, `detailed`, `none`

### Approval Policy
- `WithApprovalPolicy(policy)` — `-c approval_policy="<value>"`
  - `"never"` — auto-approve everything (best for programmatic use)
  - `"on-request"` — model decides when to ask (used by `--full-auto`)
  - `"untrusted"` — most restrictive, only trusted commands auto-approved

### Tool Control
- `WithDisableShellTool()` — `--disable shell_tool`. Prevents shell command execution.
- `WithFullAuto()` — `--full-auto` (default true for programmatic use)
- `WithSandbox(mode)` — `--sandbox read-only|workspace-write|danger-full-access`
  - **Note:** Sandbox only restricts shell commands. `apply_patch` (file editing) and web search are NOT affected by sandbox mode.

### MCP-Only Configuration
To restrict Codex to only use MCP tools:
```go
codexcli.WithDisableShellTool()       // no shell commands
codexcli.WithApprovalPolicy("never")  // auto-approve MCP calls
```
`apply_patch` and `web_search` remain active — there is no feature flag to disable them.

### Session Management
- `WithResumeSessionID(id)` — resumes a previous session by thread ID
- `WithProjectDirID(dir)` — `--cd` flag to set working directory

### Other Options
- `WithConfigProfile(profile)` — `--profile` flag for config.toml profiles
- `WithOutputSchema(path)` — `--output-schema` for structured JSON output
- `WithAdditionalDirs(dirs)` — `--add-dir` for extra writable directories
- `WithConfigOverrides([]string{...})` — arbitrary `-c key=value` overrides

## Authentication
Priority order:
1. `config.APIKeys.CodexCLI` (explicit config)
2. `CODEX_API_KEY` env var
3. `OPENAI_API_KEY` env var
4. Saved CLI authentication (`codex login`)

## Model Metadata

| Model | Context Window | Input $/1M | Output $/1M | Cached $/1M | Reasoning Levels |
|-------|---------------|------------|-------------|-------------|-----------------|
| gpt-5.4 | 1,050,000 | $2.50 | $15.00 | $0.25 | none, low, medium, high, xhigh |
| gpt-5.3-codex | 400,000 | $1.75 | $14.00 | $0.175 | low, medium, high, xhigh |
| gpt-5.5 | 400,000 | $1.75 | $14.00 | $0.175 | low, medium, high, xhigh |

Note: gpt-5.4 prompts exceeding 272K input tokens incur 2x input and 1.5x output pricing.

## Feature Flags
Available via `--enable`/`--disable` or `-c features.<name>=true/false`:

Key stable flags:
- `shell_tool` — shell command execution (default: true)
- `shell_snapshot` — shell state snapshots (default: true)
- `fast_mode` — faster responses (default: true)
- `multi_agent` — sub-agent delegation (default: true)

List all: `codex features list`

## Error Handling
- Inactivity watchdog: kills process after 10 minutes of no output (skipped while tool calls in flight)
- Progress heartbeat: sends status message after 30s of silence
- Rate limit detection: monitors stderr for 429/503 errors
- Empty result retry: resumes session with finalization prompt if result is empty
