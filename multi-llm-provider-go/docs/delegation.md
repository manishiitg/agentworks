# Delegation Workflow

Delegation is asynchronous by design. Coding agents often take many minutes to
inspect a repository, use tools, modify files, and run tests. The host should not
hold one MCP tool call open for the entire execution.

## Start A Job

Ask the host naturally:

```text
Delegate the failing authentication tests to Cursor using composer-2.5.
Do not duplicate its work. Check the job after the recommended delay, then
review and test the result.
```

The host calls `delegate_coding_agent` with:

- `provider`: one enabled target CLI
- `task`: a bounded, self-contained instruction
- `working_dir`: the host's current trusted project
- `model`: an optional provider-specific selector
- `timeout_seconds`: an optional job deadline

The tool returns a job ID immediately. Users should not be asked to re-enter the
working directory because the host already knows its trusted project.

## Poll A Job

Call `get_coding_agent_job` after `poll_after_seconds`. Job states are:

- `queued`: persisted and waiting for a worker
- `running`: target CLI active in tmux
- `completed`: final provider response available
- `failed`: launch or execution failed
- `cancelled`: stopped through `cancel_coding_agent_job`
- `timed_out`: deadline reached

Ordinary polling should keep `include_terminal_output` disabled. Enable it only
when progress is unclear; terminal output adds repeated text to the host's
context.

Running jobs can return:

- `tmux_session`
- `tmux_capture_command`
- `tmux_attach_command`
- A bounded ANSI-cleaned terminal tail when requested

The attach command is intended for a human terminal, not for the host model to
execute automatically.

## Cancel A Job

Use `cancel_coding_agent_job` when the task is no longer useful or the target is
waiting indefinitely. Cancellation stops the owned worker and tmux session and
records a terminal state in the job database.

## Write Delegatable Tasks

Good tasks are independent and verifiable:

```text
Inspect the failing package tests, implement the smallest correct fix, run the
affected tests, and report the files changed and test results. Do not commit.
```

Avoid vague tasks such as "improve the project." State:

- The problem or desired outcome
- Relevant files or failing commands when known
- What the target may change
- Required verification
- Whether the target should only review or may edit files

## Choose A Target

- Use a powerful model for difficult implementation, design review, or an
  independent second opinion.
- Use a fast or lower-cost model for bounded cleanup, test generation, log
  analysis, or repetitive edits.
- Use Pi when the desired Gemini, OpenRouter, MiniMax, GLM, or Kimi model is not
  available in the host CLI.
- Use the same provider as the host when a separate asynchronous context is the
  main benefit.

## Review The Result

Delegation does not transfer responsibility for the working tree. The host
should always:

1. Read the final response.
2. Inspect the diff and untracked files.
3. Run focused tests or validation.
4. Resolve conflicts with work performed concurrently by the host.
5. Explain any remaining uncertainty to the user.

Do not run two editing jobs against the same files unless the tasks are designed
for that concurrency.
