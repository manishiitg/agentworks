## Summary

Describe the user-visible behavior and why the change is needed.

## Compatibility

Describe public Go API, host, provider, model, setup, and workspace-security impact.

## Verification

- [ ] `go test ./...`
- [ ] `golangci-lint run --timeout=5m ./...`
- [ ] Focused tests for the changed behavior
- [ ] Documentation updated when behavior or configuration changed
- [ ] No credentials, local MCP configuration, or private transcripts included

## Real Provider Testing

State whether a real CLI/provider test was run. Include sanitized versions and
results, or explain why deterministic coverage is sufficient.
