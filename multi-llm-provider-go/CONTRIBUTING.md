# Contributing

Contributions should preserve the shared Go provider API, direct API/cloud
transports, tmux-backed coding-agent behavior, and the optional asynchronous MCP
delegation workflow.

## Before Opening An Issue

- Run `llm-provider-mcp doctor` for installation or authentication problems.
- Search existing issues for the provider, host, model, and error text.
- Remove credentials, private source, and sensitive terminal output.
- Use the security process for vulnerabilities instead of a public issue.

## Development Setup

Requirements:

- Go 1.25.12 or newer
- tmux 3.x or newer
- `golangci-lint`
- Native coding CLIs required by the tests you intend to run

```bash
git clone https://github.com/manishiitg/llm-provider-mcp.git
cd llm-provider-mcp
go mod download
make build
make build-mcp
go test -p 1 ./...
```

## Required Checks

Before opening a pull request:

```bash
gofmt -w <changed-go-files>
go test -p 1 ./...
golangci-lint run --timeout=5m ./...
git diff --check
```

Changes to exported provider APIs must also compile against MCP Agent and MCP
Agent Builder. CI performs those downstream checks automatically.

## Provider Changes

Direct API and cloud adapter changes should update request conversion, response
parsing, metadata, error classification, and the relevant unit or opt-in real
tests. Keep provider-specific behavior inside its adapter when it cannot be
expressed safely through a shared contract.

See `docs/api_provider_test_contract.md` for the expected API-provider test
areas.

## Coding-Agent Changes

`CodingAgentProviderContracts()` is the capability source of truth. A provider
change may also require updates to:

- Model discovery
- Setup detection and authentication
- Job runner dispatch
- MCP tool schemas or responses
- tmux/session lifecycle registries
- Project skill guidance
- Static contract tests and opt-in real E2E tests

Do not advertise a capability before the adapter, setup, tests, and cleanup path
all support it.

## Tests

Keep ordinary tests deterministic and offline. Real CLI or provider tests must
remain opt-in behind an explicit environment variable and explain required
credentials in the skip message.

Use the smallest real model suitable for contract verification. Real E2E tests
must clean up owned tmux sessions and temporary project artifacts.

Run the full local suite with `-p 1`, matching CI, because interactive adapter
packages share the machine's tmux service.

## Pull Requests

- Keep changes scoped to one behavior or cleanup.
- Explain user-visible behavior and compatibility impact.
- Include tests proportional to the change risk.
- Document new configuration and limitations.
- Do not include generated credentials, local MCP registration, or private
  terminal transcripts.

Maintainers may ask for a downstream compatibility migration before accepting a
breaking public-API change.
