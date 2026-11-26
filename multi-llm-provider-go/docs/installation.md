# Installation

`llm-provider-mcp` installs one local binary, registers a project-local stdio
MCP server, and adds a managed delegation skill for the selected host CLIs.

## Requirements

- macOS or Linux
- tmux 3.x or newer
- Codex or Claude Code as a host
- At least one authenticated target CLI

## Interactive Installation

Run the installer from the project where delegation should be available:

```bash
curl -fsSL https://raw.githubusercontent.com/manishiitg/llm-provider-mcp/main/scripts/install-mcp.sh | sh
```

The installer downloads the checksum-verified release archive for the current
OS and architecture, installs `llm-provider-mcp` in `~/.local/bin`, and starts
the setup wizard.

The wizard:

1. Detects installed Codex and Claude Code hosts.
2. Lets you select any number of hosts.
3. Detects Cursor, Pi, Codex, and Claude Code targets.
4. Lets you select any number of targets.
5. Verifies target authentication and offers native login flows when needed.
6. Shows every project-local file it will create or update.
7. Checks the MCP protocol before completing registration.

Use Up and Down to move, Space to toggle an item, and Enter to confirm. A text
fallback is used when the terminal does not support the interactive checklist.

## Inspect Before Running

Developers who do not want to pipe a remote script directly to a shell can
download and inspect it first:

```bash
curl -fsSLO https://raw.githubusercontent.com/manishiitg/llm-provider-mcp/main/scripts/install-mcp.sh
less install-mcp.sh
sh install-mcp.sh
```

Release archives and `checksums.txt` are also available from the GitHub release
page for fully manual verification.

## Install With Go

The Go module path remains unchanged for compatibility:

```bash
go install github.com/manishiitg/multi-llm-provider-go/cmd/llm-provider-mcp@latest
llm-provider-mcp setup
```

Go 1.25.12 or newer is required only for source installation. Installing a
release archive does not require Go.

## Project Scope

Setup is intentionally local to the current project:

- Codex MCP registration is written to the trusted project configuration.
- Claude Code uses local project scope for the current user.
- Codex receives `.agents/skills/delegate-coding-agent/SKILL.md`.
- Claude Code receives `.claude/skills/delegate-coding-agent/SKILL.md`.

Re-running setup updates installer-managed skill files but refuses to overwrite
an unrelated skill with the same name.

Start a new host session after setup so the client reloads its MCP servers.

## Noninteractive Installation

Automation can preselect a host and target allowlist:

```bash
curl -fsSL https://raw.githubusercontent.com/manishiitg/llm-provider-mcp/main/scripts/install-mcp.sh \
  | sh -s -- --client codex --providers cursor-cli,pi-cli --non-interactive
```

Run `sh scripts/install-mcp.sh --help` from a checkout for all bootstrap flags.
Run `llm-provider-mcp setup --help` for setup automation options.

## Manual MCP Registration

For Codex, register the binary in the trusted project's `.codex/config.toml`:

```toml
[mcp_servers.llm-provider]
command = "/absolute/path/to/llm-provider-mcp"
required = true
startup_timeout_sec = 10
tool_timeout_sec = 30
```

Claude Code and Cursor use the standard JSON stdio shape:

```json
{
  "mcpServers": {
    "llm-provider": {
      "command": "/absolute/path/to/llm-provider-mcp",
      "args": []
    }
  }
}
```

Pi can use the same server entry through its MCP adapter. Manual registration
does not install the delegation skill or verify target authentication.

## Validate The Installation

```bash
llm-provider-mcp doctor
llm-provider-mcp models cursor-cli
llm-provider-mcp models pi-cli
```

`doctor` checks the binary, tmux, MCP protocol, project registration, target
executables, and authentication.

## Uninstall

Run this from the configured project:

```bash
llm-provider-mcp uninstall
```

Uninstall removes only MCP registrations and skill files marked as managed by
this installer. It does not remove native CLI credentials or unrelated project
configuration.

## Environment Variables

- `LLM_PROVIDER_MCP_STATE`: override the SQLite state file.
- `LLM_PROVIDER_MCP_ALLOWED_PROVIDERS`: comma-separated target allowlist.
- `LLM_PROVIDER_MCP_WORKSPACE_ROOTS`: OS path-list of accepted workspace roots.
- `LLM_PROVIDER_MCP_VERSION`: release tag, `main`, or `latest` for bootstrap.

The default job database is `~/.local/state/llm-provider-mcp/jobs.db`.
