# Real Delegation Demos

The README animations are based on real end-to-end runs performed on July 11,
2026. They are rendered from the sanitized tool events listed in
[`demo-transcript.json`](assets/demo-transcript.json), not invented provider
output. The Claude-to-Codex run is recorded separately in
[`codex-sol-demo-transcript.json`](assets/codex-sol-demo-transcript.json).

## Claude Code To Cursor

### Environment

- Host: Claude Code 2.1.207, Claude Sonnet 5
- Target: Cursor Agent 2026.07.09, Composer 2.5
- MCP server: locally built `llm-provider-mcp` 0.6.0 demo binary
- Workspace: disposable Go repository
- Initial failure: `Add(7, 5) = 2, want 12`

### Task

Claude Code was instructed not to edit the repository itself. It had to:

1. Confirm the failing test.
2. Delegate the bounded fix to `cursor-cli` through MCP.
3. Record the asynchronous job ID.
4. Poll to a terminal state.
5. Inspect the resulting Git diff.
6. Rerun the test without cache.

### Result

- Job ID: `job_a945abbdb5d45df757fe7cf9639565ca`
- Target status: completed
- Target elapsed time: 28 seconds
- Changed file: `calculator.go`
- Change: `return a - b` to `return a + b`
- Target verification: `go test ./...` passed
- Host verification: `go test -count=1 ./...` passed

The run also exercised project-local setup, managed skill loading, deferred MCP
tool discovery, durable job creation, tmux progress metadata, bounded terminal
capture, final extraction, usage reporting, session cleanup, and independent
host verification.

## Claude Code To Codex GPT-5.6 Sol

The second run used Claude Code 2.1.207 with Claude Sonnet 5 as the host and
Codex CLI 0.145.0-alpha.4 with `gpt-5.6-sol` as the target. The disposable Go
repository started with `Multiply(7, 6) = 13, want 42`.

- Job ID: `job_7c516599b9988b65c1debf2ce9146295`
- Target status: completed
- Target elapsed time: 36 seconds
- Changed file: `calculator.go`
- Change: `return a + b` to `return a * b`
- Target verification: `go test ./...` passed in 0.487 seconds
- Host verification: `go test -count=1 ./...` passed in 0.272 seconds

During verification, stable Codex CLI 0.144.1 rejected `gpt-5.6-sol` and asked
for a newer Codex version. The successful run used the isolated npm alpha
`0.145.0-alpha.4`; the user's stable Codex installation was not replaced.

## Reproduce

### Cursor

Create a disposable Go repository containing:

```go
// calculator.go
package calculator

func Add(a, b int) int { return a - b }
```

```go
// calculator_test.go
package calculator

import "testing"

func TestAdd(t *testing.T) {
    if got := Add(7, 5); got != 12 {
        t.Fatalf("Add(7, 5) = %d, want 12", got)
    }
}
```

Run `llm-provider-mcp setup`, select Claude Code as the host and Cursor as the
target, restart Claude in that project, and use:

```text
Use the llm-provider MCP tools to delegate this task to cursor-cli: Fix the
failing TestAdd, run go test ./..., and report what changed. Do not edit files
yourself. Poll the asynchronous job to completion, inspect the resulting diff,
and rerun go test -count=1 ./... yourself.
```

### Codex GPT-5.6 Sol

Verify that the Codex executable resolved by your login shell is a 0.145 build:

```bash
codex --version
```

Run setup, select Claude Code as the host and Codex CLI as a target, then ask:

```text
Use the llm-provider MCP tools to delegate this task to codex-cli with model
gpt-5.6-sol: Fix the failing TestMultiply by correcting the implementation,
run go test ./..., and report what changed. Do not edit files yourself. Poll
the asynchronous job to completion, inspect the resulting diff, and rerun go
test -count=1 ./... yourself.
```

Provider versions, model output, and elapsed time will vary.
