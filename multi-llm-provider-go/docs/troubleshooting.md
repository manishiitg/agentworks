# Troubleshooting

Start with:

```bash
llm-provider-mcp doctor
```

The doctor checks installation, tmux, MCP protocol, host registration, target
executables, and authentication.

## Command Not Found

The default binary location is `~/.local/bin/llm-provider-mcp`. Add it to PATH:

```bash
export PATH="$HOME/.local/bin:$PATH"
```

Persist that line in the appropriate shell configuration after verifying the
command works.

## Host Does Not See The MCP Tools

1. Run `llm-provider-mcp doctor` from the configured project.
2. Re-run `llm-provider-mcp setup` if registration is missing.
3. Fully restart Codex or Claude Code in the same project.
4. Confirm the project is trusted by the host CLI.

Setup is project-local. A registration made in one repository is not expected
to appear in another.

## Target Is Installed But Not Ready

Use the target CLI's native auth status and login flow. Re-run setup afterward;
it can verify all selected targets and offer an optional small read-only
connectivity prompt.

Credentials are not stored by `llm-provider-mcp`.

## Model Is Rejected

List the current catalog:

```bash
llm-provider-mcp models PROVIDER
llm-provider-mcp models cursor-cli --live
```

Account-visible catalogs can change independently of this repository. For Pi,
verify that the selected sub-provider is authenticated in Pi.

For GPT-5.6 Sol or Luna, verify Codex 0.145 or newer. Stable Codex CLI
0.144.1 rejects these selectors during the initial rollout:

```bash
command -v codex
codex --version
```

Coding-agent targets launch through the user's login shell by default. If
multiple Codex installations exist, run the same commands in a fresh login
shell and remove or upgrade the older executable that appears first. For
controlled environments that intentionally supply `PATH`, set
`CODING_AGENT_SHELL_MODE=direct` on the MCP server process.

## Job Stays Queued

- Confirm the installed binary is executable.
- Check that the state database directory is writable.
- Run `llm-provider-mcp doctor` for tmux and provider failures.
- Poll the job once more after the recommended delay for worker launch errors.

## Job Appears Stuck

Request terminal output with `get_coding_agent_job`, or run the returned tmux
attach command in a human terminal. The pane may show authentication failure,
an unexpected provider prompt, or long-running tests.

Cancel the job when it cannot make useful progress.

## Working Directory Rejected

The host must pass an absolute trusted project path. When
`LLM_PROVIDER_MCP_WORKSPACE_ROOTS` is set, the path must be inside one of those
roots.

Users should not need to type the working directory manually; start the host in
the intended trusted project.

## Setup Refuses To Replace A Skill

The installer updates only files carrying its managed marker. If an unrelated
skill already uses `delegate-coding-agent`, move or rename that skill before
running setup. The installer will not overwrite it automatically.

## Collecting A Bug Report

Include:

- OS and architecture
- `llm-provider-mcp --version`
- `tmux -V`
- Host and target CLI versions
- Provider ID and model selector
- Job status and sanitized error
- Whether the issue reproduces after `doctor`

Remove credentials, source code, absolute private paths, and terminal content
that contains sensitive data.
