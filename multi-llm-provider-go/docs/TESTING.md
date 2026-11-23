# Provider verification

## Deterministic coding-agent contracts

Run the credential-free certification checks:

```sh
go test . -run 'TestActiveCodingAgentProvidersSatisfyP0Contract|TestCodingAgentCertificationReferencesExistingTests' -count=1
go test ./pkg/adapters/agycli ./pkg/adapters/musecli -count=1
```

Adapter packages hold parser, continuation, private-home, permission and completion regressions. Tests that require an installed CLI or credentials use their explicit environment controls; do not count a skipped live test as acceptance.

## Manual coding-CLI harness

Build `cmd/llm-test`, then inspect `--help` for the available Claude Code and Codex CLI commands. Run a live command only when its actual provider work and effects are intended. Keep provider credentials in the environment, never in source or recorded output.

The direct API adapter packages remain part of the library for application features such as model metadata, image generation and voice. Their deterministic tests live beside the adapters. Use the application's real integration checks for its supported operations.
