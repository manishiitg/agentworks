[← coding-agents / tools](index.md)

# PLAT-225 — the MCP bridge no longer discards a tool's actual output when it reports failure

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | coding-agents |
| Area | tools |
| Summary | P1 — Sales Outreach `PUL-AAC278EF` (per the |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `implemented; runtime reverify` |
| Last synchronized | `2026-08-29` |

- **Priority:** P1 — Sales Outreach `PUL-AAC278EF` (per the
  [2026-08-29 triage audit](../../../../audits/pulse-platform-triage-2026-08-29.md)).
- **Owner:** `github.com/manishiitg/mcpagent`, `cmd/mcpbridge/main.go` — a
  sibling repository (`replace github.com/manishiitg/mcpagent =>
  ../../mcpagent` in `agent_go/go.mod`), same cross-repo pattern as
  [PLAT-193](plat-193.md).
- **Related:** PLAT-193 (`toolerr` misclassifying `execute_shell_command`
  *success* as failure). This ticket is the opposite direction: a *genuine*
  failure correctly detected, but its actual output thrown away in the
  process.

## Problem

`execute_shell_command` on a chained script (e.g. `ls | grep ... && curl
...`) whose trailing command returned nonzero surfaced only a bare
`ERROR: tool execution failed: exit_code=1` / `permission denial in stderr`
/ `http_status=404` — with no captured stdout at all, even though earlier
commands in the chain had run successfully and produced real, useful
output. The agent (and, reproduced first-hand by this Pulse reviewer's own
calls in the same session) had to retry blind to see what had actually
happened, wasting tool calls, tokens, and turns, and raising the risk of a
repair attempt aimed at the wrong cause.

## Root cause

`agent_go/pkg/workspace/tools.go`'s `execute_shell_command` executor
already forwards the full result (including `Stdout`/`Stderr`/`ExitCode`)
regardless of exit code — it never itself returns a Go error for a nonzero
exit. The actual discard happened one layer further out, in `mcpbridge`'s
stdio tool-call handler (`cmd/mcpbridge/main.go`), which receives the
custom-tool HTTP response as `{success, result, error}`:

```go
if !result.Success {
    errorMsg := result.Error
    ...
    return mcp.NewToolResultText(fmt.Sprintf("ERROR: %s", ...)), nil
}
```

`result.Result` — the same tool output the caller would have received on
success — was never included when `Success == false`. It is not an
alternate success-only payload; for `execute_shell_command` it is the
captured stdout/stderr/exit_code JSON regardless of outcome. Discarding it
here is what produced the bare, contentless error text.

## Fix

Append `result.Result` (bounded and offloaded to `tool_output_folder` the
same way a successful result is, via the existing `prepareBridgeToolResult`)
after the error text whenever it is non-empty. No behavior change when it is
empty. The error text stays regular content (not MCP `IsError`), matching
this file's existing rationale: some CLI providers (Claude Code among them)
hide content behind a generic message when `IsError=true`.

## Verification

```text
go test ./cmd/mcpbridge/... ./toolerr/... ./executor/...   (mcpagent)
go build ./...; go test ./cmd/server/...                    (agent_go, via the local replace directive)
```

New `TestMCPBridgePreservesShellOutputOnToolFailureE2E` exercises the
complete production transport — a real `mcpbridge` subprocess over stdio,
forwarding to a real HTTP server, no helper called directly — with a
fixture shaped exactly like the finding's own reproduction (a chain whose
earlier commands produced real stdout before a trailing nonzero exit), and
asserts the returned text carries both the error signal and the actual
captured output. Full existing suites pass unchanged in both repositories.

## Reverify

No live agent turn has hit this corrected path through the deployed server
yet. Reverify by reproducing a chained `execute_shell_command` script with a
failing trailing command and confirming the captured stdout from the
earlier commands is now visible alongside the error.
