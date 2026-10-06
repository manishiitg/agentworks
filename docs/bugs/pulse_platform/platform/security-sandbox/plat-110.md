[← platform / security-sandbox](index.md)

# PLAT-110 — Codex strict CLI-security launch can retain undeclared credentials and expose declared secret values in process arguments

| Field | Value |
|---|---|
| State | deployed |
| Priority | P1 |
| Product | platform |
| Area | security-sandbox |
| Summary | The Codex `verified`/`isolated` launch path correctly starts from `env -i`, but |

| Field | Value |
|---|---|
| Status | `open_deferred` — not reachable in AgentWorks' current default compatibility mode; must be fixed before verified/isolated mode is enabled |
| Priority | P1 before strict-mode rollout; P2 while compatibility remains the only deployed mode |
| Owner | `multi-llm-provider-go` Codex sandbox launch boundary |
| Reported | 2026-08-15 during review of `llm-provider-mcp` PR #34 |
| Evidence | `internal/clisandbox/sandbox.go` at PR head `6fa01ea269d46ede1822b285d2653c8393f0f505` |
| Related | `llm-provider-mcp#34`; AgentWorks CLI-security configuration |

## Problem

The Codex `verified`/`isolated` launch path correctly starts from `env -i`, but
then reconstructs the child environment in two unsafe ways:

1. It copies every name in `CLISecurityPolicy.EnvironmentVariables` from the
   backend's ambient environment before applying the current turn's declared
   credential scope. If a credential-like name is allowlisted but omitted from
   the current scope, the ambient value survives. An omitted credential should
   mean unavailable, not inherited.
2. It appends each declared `KEY=secret` entry to the command arguments used to
   construct the `sandbox-exec`/`/bin/sh -c` launch. Those plaintext values can
   therefore be visible in process arguments and the tmux launch command.

Calling Codex through AgentWorks does not bypass this code: AgentWorks resolves
the CLI-security policy and passes it through `mcpagent` to the Codex adapter.
The defects are dormant today only because the installed AgentWorks
configuration falls back to `compatibility`, whose protected temporary-wrapper
path returns before this strict branch.

## Current reachability

- **Current AgentWorks deployment:** effectively not reachable. No persisted
  CLI-security override exists under AgentWorks' standard config root, so
  `DefaultConfig()` selects `compatibility`.
- **Undeclared allowlisted credential:** additionally dormant with the current
  Codex profile because its capability list does not add credential environment
  names. It becomes deterministic if a future profile or caller allowlists a
  scoped credential which the current turn omits.
- **Secret in process arguments:** deterministic in strict mode whenever a
  Codex turn carries at least one declared scoped environment entry.

This ticket permits PR #34 to merge for its immediately useful compatibility
and Pi/Codex isolation repairs, but it is a release gate for exposing
`verified` or `isolated` mode to users.

## Required repair

1. Build one authoritative child environment from non-credential allowlisted
   variables plus the current declared scope. Before ambient allowlist copying,
   exclude every name/prefix classified by `ScopedCredentialNames()` and
   `ScopedCredentialPrefixes()` unless the current scope explicitly grants it.
2. Never place plaintext scoped values in `argv` or a tmux command. Reuse the
   protected `0600`, self-deleting launch-wrapper mechanism already used by the
   compatibility path, while preserving `env -i` and `sandbox-exec`.
3. Preserve the semantic difference between no declared scope and an explicitly
   empty scope; the latter must scrub every scoped credential.
4. Ensure logs, errors, process inspection, and tmux inspection cannot reveal a
   supplied secret value.

## P0 regression coverage

1. Launch strict Codex with ambient `MCP_API_TOKEN=other-tenant`, allowlist the
   name, and declare an empty scope. The child must not receive the token.
2. Repeat with an explicitly granted current token. The child receives exactly
   that value, never the ambient one.
3. Include a distinctive secret in the declared scope and inspect the complete
   generated command, process arguments, tmux command, and captured logs. The
   distinctive value must be absent from all four while remaining available
   inside the child.
4. Exercise both `verified` and `isolated` modes through the real
   AgentWorks → mcpagent → Codex adapter path.

## Acceptance

1. A strict Codex child can access only credentials explicitly declared for its
   current turn.
2. Plaintext scoped credential values never appear in process arguments, tmux
   commands, logs, or error strings.
3. Compatibility behavior remains unchanged.
4. Strict CLI-security cannot be enabled in AgentWorks until the real boundary
   tests pass.
