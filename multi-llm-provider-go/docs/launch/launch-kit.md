# Launch Kit

Use factual copy and the real demo. Do not describe benchmark superiority or
claim support beyond the certified host and target matrix.

## Core Positioning

`llm-provider-mcp` lets one local coding CLI delegate an asynchronous coding job
to another. Claude Code or Codex receives a job ID immediately, keeps working,
and later collects the result from Cursor, Pi, Codex, or Claude Code.

## X Launch Post

```text
I built llm-provider-mcp: a local MCP server that lets Claude Code and Codex
delegate background coding jobs to Cursor, Pi, Codex, or Claude Code.

The host gets a job ID immediately, keeps working, then reviews the result.
Jobs run locally in inspectable tmux sessions using existing CLI logins.

The README includes real Claude-to-Cursor and Claude-to-Codex GPT-5.6 Sol runs,
including host-side diff and test verification.

[attach docs/assets/llm-provider-mcp-demo.mp4]
https://github.com/manishiitg/llm-provider-mcp
```

For a Codex-focused follow-up, attach
`docs/assets/llm-provider-mcp-codex-sol-demo.mp4` and disclose that the Sol run
used Codex CLI `0.145.0-alpha.4` while the model was still on the alpha channel.

## X Technical Follow-Up

```text
Why asynchronous jobs instead of one long MCP call?

Coding agents can take minutes to inspect, edit, and test a repository. The MCP
tool persists a job in SQLite, starts a detached worker and tmux session, and
returns immediately. The host polls progress only when useful.

The target remains directly inspectable with tmux attach.
```

```text
Current matrix:

Hosts: Claude Code, Codex
Targets: Cursor, Pi, Codex, Claude Code
Pi routes to Gemini, OpenRouter, MiniMax, GLM, and Kimi.

macOS/Linux and tmux are required. Completion is polling-based today.
```

## Show HN

Title:

```text
Show HN: llm-provider-mcp – delegate tasks between Claude Code, Codex, Cursor, and Pi
```

First comment:

```text
I built this after repeatedly wanting to use a model or coding CLI that was not
available in my current Claude Code or Codex session.

llm-provider-mcp is a local stdio MCP server. A host delegates a bounded task,
gets a durable job ID immediately, and keeps working. A detached worker runs the
target CLI in tmux. The host can poll structured progress, request a bounded
terminal tail, or give the human the tmux attach command. When the job finishes,
the host receives the result and verifies the actual diff and tests.

It currently supports Claude Code and Codex as automatically configured hosts,
with Cursor, Pi, Codex, and Claude Code as targets. Pi is useful for models such
as Gemini, OpenRouter, MiniMax, GLM, and Kimi. It uses existing native CLI
logins; this project does not collect provider keys.

The main tradeoffs are that it requires macOS/Linux and tmux, completion is
polling-based, and unattended agents still need careful workspace boundaries.
Pi currently lacks a hard workspace sandbox, which is documented prominently.

The README contains a real Claude-to-Cursor run and a one-command installer. I
would especially value feedback on the async tool contract, trust model, and
which host/target pairing is most useful in practice.
```

Follow the official Show HN rules: submit something users can try, remain
available for discussion, do not solicit votes or booster comments, and use a
personal account rather than a project-branded account.

## Reddit Version

```text
Title: Open-source MCP for delegating work between Claude Code, Codex, Cursor, and Pi

I built a local MCP server for a workflow I kept missing: start in Claude Code
or Codex, delegate one bounded coding task to another installed CLI, continue
working, and review the result when it finishes.

Jobs are asynchronous and run in detached tmux sessions. The MCP response
includes a job ID, progress polling, optional terminal capture, cancellation,
and a human tmux attach command. It uses existing CLI authentication.

The README includes a real Claude Code -> Cursor Composer run that fixes a test
and is independently verified by the host. Current limitations are documented.

Repository: https://github.com/manishiitg/llm-provider-mcp
```

Adapt the opening to the community and read its current self-promotion rules
before posting.

## Technical Article Outline

Title: `Why coding-agent delegation should be asynchronous`

1. The missing cross-CLI workflow
2. Why a synchronous MCP tool is the wrong lifecycle
3. Durable jobs and worker ownership
4. Why tmux remains useful for inspection and recovery
5. Provider-specific unattended permission policies
6. Workspace trust versus actual process sandboxing
7. Final extraction and independent host verification
8. Polling today and push notifications later
9. What the real Claude-to-Cursor E2E exposed

## Likely Questions

**Why not just open another terminal?**

The value is delegation from the active host context: structured task handoff,
durable status, model selection, result collection, and host-side verification.

**Why tmux?**

The native CLIs are interactive terminal programs. tmux provides detached
execution, persistent state, live human inspection, and a provider-independent
way to capture bounded progress.

**Does it share credentials?**

No. Each target uses its own existing native CLI authentication.

**Is it sandboxed?**

Provider behavior differs. Codex, Cursor, and Claude use provider-specific
unattended sandbox/permission policies. Pi does not currently expose a hard
workspace sandbox, so the limitation is explicit.

**Why polling?**

Polling is consistently supported by current hosts. MCP task notifications are
being evaluated after host behavior is certified.

**Does the host trust the target's answer?**

It should not. The installed skill tells the host to inspect the actual diff and
run relevant tests before reporting completion.
