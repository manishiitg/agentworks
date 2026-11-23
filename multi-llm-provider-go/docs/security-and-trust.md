# Security And Trust

`llm-provider-mcp` launches coding agents with tools in the user's local project.
That capability is useful because agents can inspect, edit, and test real code;
it also means installation and delegation should be treated as local code
execution.

## Trust Boundary

- The MCP server, worker, tmux server, and target CLIs run on the local machine.
- They inherit the operating-system account's accessible files and processes,
  subject to provider-specific sandboxes.
- Native CLI credentials remain owned by those CLIs.
- The MCP server stores job metadata and results in a local SQLite database.
- No remote orchestration service is required by this project.

The target model provider still receives prompts, code, and tool context
according to that native CLI's own behavior and terms.

## Project Scope

Interactive setup registers the MCP server for the current project rather than
globally. It prints the exact project and configuration paths before writing.

The host passes its current trusted project as `working_dir` for each job. Set
`LLM_PROVIDER_MCP_WORKSPACE_ROOTS` to reject jobs outside explicitly allowed
roots:

```bash
export LLM_PROVIDER_MCP_WORKSPACE_ROOTS="$HOME/work/project-a:$HOME/work/project-b"
```

This validation is defense in depth, not a process sandbox.

## Unattended Execution

Detached agents cannot rely on a user seeing approval dialogs. The adapters use
provider-specific unattended policies:

- Codex: no approval prompts with the workspace-write sandbox.
- Cursor: force approval with the Cursor sandbox enabled.
- Claude Code: `dontAsk`, project-scoped file rules, and a Bash sandbox that
  fails closed when unavailable.
- Pi: project trust and native coding tools.

Pi does not currently provide a hard workspace sandbox. Its Bash and file tools
retain the permissions of the local user. Do not use Pi for untrusted prompts or
untrusted repositories.

Every provider receives an explicit instruction to stay inside the delegated
working directory, but prompt instructions are not a security boundary.

## Safer Operating Practices

- Review the installer before piping it to a shell.
- Use a clean Git working tree before delegation.
- Delegate bounded tasks with explicit file and verification scope.
- Do not include secrets in prompts.
- Avoid running unrelated editing jobs against the same files concurrently.
- Inspect `git diff`, untracked files, and test output after completion.
- Use a disposable checkout or stronger OS-level sandbox for untrusted work.
- Keep target CLIs updated and review their own permission documentation.

## Installer Behavior

The release installer verifies the downloaded archive against the published
SHA-256 checksums before installation. It writes the binary to `~/.local/bin` by
default and launches the local setup wizard.

Setup does not collect API keys. When authentication is missing, it invokes the
target CLI's native login flow.

## Reporting Vulnerabilities

Do not open a public issue for a suspected vulnerability. Follow
[SECURITY.md](../SECURITY.md) for private reporting instructions.
