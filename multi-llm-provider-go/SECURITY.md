# Security Policy

`llm-provider-mcp` launches local coding agents with filesystem and shell tools.
Security reports involving workspace boundaries, command execution, installer
integrity, credential exposure, or MCP tool authorization are treated as high
priority.

## Supported Versions

Security fixes are provided for the latest GitHub release. Users should upgrade
before reporting a problem already fixed on `main`.

## Report Privately

Use GitHub's **Report a vulnerability** form in the repository Security tab.
Do not open a public issue or include exploit details in a discussion.

Include when possible:

- A concise impact statement
- Affected version, OS, host, and target provider
- Reproduction steps or a minimal test repository
- Whether credentials or files outside `working_dir` are exposed
- Relevant sanitized logs
- A proposed mitigation, if known

Do not include real API keys, access tokens, private source code, or unrelated
personal information.

## Response

The maintainer will acknowledge a valid report, investigate reproduction and
impact, coordinate a fix and release, and credit the reporter when requested.
Timelines depend on severity and the behavior of upstream coding CLIs.

## Scope Notes

Provider CLIs and model services have independent security policies. Reports
that reproduce only in an upstream CLI may be redirected to that provider, but
issues caused by this project's launch policy, workspace validation, MCP
surface, or installer remain in scope.

See [Security and trust](docs/security-and-trust.md) for the documented local
execution model and current sandbox limitations.
