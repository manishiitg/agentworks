[← code / shell](index.md)

# PLAT-645: Backgrounded shell commands still block the chat's next commands

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | code |
| Area | shell |
| Summary | A command that leaves a job running in the background (`server &`) held execute_shell_command until that job ended; it now returns at once and names what is still running |

## What happened

RTS, 2026-10-07, SDE Private Code, side chat `task2`: the agent started a PR 189 review and, in the same command, polled GitHub every 20 s up to 25 times (about 10 minutes). After 120 s the CLI reported the command "moved to the background as task …; keeps running; you can keep working". The agent then ran three more commands (PR 190 lookups, then `echo alive`); none started until the loop finished, and the agent concluded the shell itself was stuck. The loop finished on its own; nothing on RTS was broken.

RTS `agent.log` for `code:project:3b40f2c4…` shows three such 10-minute `execute_shell_command` calls (05:48, 05:59, 06:10), each followed only by the next call's START once it had ended: no later call reached the bridge while one was running.

## Cause

Two separate things:

1. **Platform (fixed).** `workspace/handlers/shell.go` captured output with `cmd.Stdout = &bytes.Buffer`. Go then creates its own pipes and `cmd.Wait` waits until every process holding them has closed them. A job started with `&` (a dev server, a poll loop, a watcher) inherits the pipes, so the call did not return until that job ended, even though the shell had exited. To the chat this looked exactly like a stuck shell.
2. **CLI side (not ours).** The platform does not run one command at a time per chat: the MCP bridge (mcp-go stdio, 5 tool-call workers) and the HTTP handlers run calls concurrently, and there is no per-session shell lock. In the RTS case the later calls never reached the bridge while the "moved to the background" call was open, so the coding CLI held them. The way out for the agent is to start long work as a real background job, which now returns at once.

## Fix

- `shell.go` makes its own `os.Pipe`s for stdout/stderr (`shell_background.go`). `Wait` returns when the shell exits. If the output is still held `shellBackgroundGrace` (500 ms) later, the call returns right away with what was printed so far, plus a `[background]` note on stderr naming the still-running pids (the command's own process group; for a slot command, "they run as your account; find them with `ps`") and how to stop them, and saying that later output is not captured, so start the job with `> file 2>&1 &` to keep it. A reader keeps draining the pipe until the job exits, so the job never blocks on a full pipe or gets SIGPIPE.
- Confinement is unchanged: the job keeps the account, Landlock or mount namespace and process group it was started in. Commands with no background job behave as before.
- Test: `workspace/handlers/shell_background_test.go` `TestExecuteShellCommandBackgroundJobReturnsPromptly` (`sleep 30 & echo started` returns in about 0.5 s with the pid; the next command runs at once).

## Left

- The command's per-call scratch (`TMPDIR`) and isolation script are still removed when the call returns, so a background job that keeps writing into `$TMPDIR` loses it. This was already true before (it was only removed after the job ended).
- Background jobs are not added to the stale-process sweeper, and their later output is dropped, not saved to a log file the agent could read (no place the sandboxed command can read is service-written today).
- Agent guidance (execute_shell_command description in `mcpagent/agent/codeexec/shell.go`): poll in a background job or with a short loop, not a 10-minute foreground loop. Not changed yet.
