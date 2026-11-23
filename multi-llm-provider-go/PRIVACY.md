# Privacy

`llm-provider-mcp` is local orchestration software. It does not require a
project-operated account or hosted control plane.

## Local Data

The server stores job metadata, progress, tmux identifiers, errors, and final
results in a local SQLite database. The default path is:

```text
~/.local/state/llm-provider-mcp/jobs.db
```

Project-local MCP registration and managed skill files are written only after
setup shows the target paths.

## Provider Data

Delegated target CLIs communicate with their respective model providers under
the user's existing account and those providers' terms. Prompts, code, tool
results, and other context may be transmitted by the native CLI. This project
does not intercept or replace that provider relationship.

## Credentials

Setup checks native CLI authentication status and may launch native login
flows. It does not collect or copy provider credentials into its job database.

## Telemetry

The project does not add product analytics or a remote telemetry service. Native
coding CLIs may have their own telemetry behavior and settings.

## User Control

Users can cancel jobs, remove project registration with
`llm-provider-mcp uninstall`, and delete the local state database when no jobs
are running. Removing this project's files does not remove data retained by a
native CLI or model provider.
