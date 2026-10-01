# Centrally managed Sentry access through CapLayer

**Status:** design proposal. The current CapLayer gateway is a single-user local alpha. It does not yet provide company SSO, durable group policies, or a credential vault for team deployment.

## Goal and trust boundary

A central team configures the Sentry connection and grants employees access through company identity groups. Employees connect Claude or another MCP client to CapLayer, authenticate to CapLayer, and never receive the upstream Sentry token. CapLayer records the employee, group, connection, operation, decision, and outcome for each call. Sentry sees the centrally managed credential rather than the individual employee; native Sentry attribution to each employee would require individual or delegated Sentry credentials.

The central team must verify which Sentry credential type supports the required MCP operations. Sentry [documents direct remote token forwarding](https://github.com/getsentry/sentry-mcp/blob/main/README.md#remote-with-an-explicit-sentry-token), while its local setup [specifies a user auth token](https://github.com/getsentry/sentry-mcp/blob/main/README.md#stdio-vs-remote). Do not assume an internal integration token supports every operation without testing it. Store the chosen credential in a server-side vault, rotate it, and give it only the required Sentry API scopes.

## Way 1: one Sentry MCP connection per organization or project

Sentry supports scoped MCP URLs:

| Scope | Example upstream URL |
| --- | --- |
| Organization | `https://mcp.sentry.dev/mcp/acme` |
| Project | `https://mcp.sentry.dev/mcp/acme/payments` |

For a Payments team, the central admin registers the Payments connection, binds it to the Payments identity group, and selects allowed tools. CapLayer routes that group's calls only to the registered upstream URL and injects the central credential. Sentry says a scoped session removes constrained organization/project parameters from applicable schemas, injects their values server-side, and validates resource access. Sentry's `update_issue` implementation fetches the issue and checks its actual project against the session constraint before writing. See [Sentry's scoped-session guide](https://github.com/getsentry/sentry-mcp/blob/main/docs/specs/subpath-constraints.md) and [`update_issue` source](https://github.com/getsentry/sentry-mcp/blob/main/packages/mcp-core/src/tools/catalog/update-issue.ts).

This is the preferred Sentry design when the central team wants an entire Sentry project or organization exposed through a known upstream boundary. It is specific to Sentry's MCP implementation, not a general MCP URL convention. The path constrains this MCP session; it does not make the underlying Sentry credential project-specific. Employees must have no route to the credential or an unrestricted CapLayer connection.

For direct-token mode, Sentry documents `?skills=inspect,triage` to limit enabled skill groups. CapLayer's current connector URL validator rejects query parameters. A future implementation should store skill selection as structured configuration and construct only approved upstream URLs; it should not accept arbitrary credential-bearing query strings. Skill selection does not replace CapLayer's group grants and call checks.

## Way 2: CapLayer policy on tool arguments

CapLayer discovers each exposed tool through `tools/list` and retains its `inputSchema`. The schema tells us declared argument names, types, allowed values where present, and individually required fields. It does not necessarily express cross-field rules or prove which organization owns an opaque resource ID. For example, Sentry's `update_issue` accepts `organizationSlug`, `issueId`, or `issueUrl`, but its runtime requires `issueUrl` **or** `organizationSlug` plus `issueId`, and at least one of `status` or `assignedTo`. It has no `projectSlug` argument. See the [MCP tool specification](https://modelcontextprotocol.io/specification/2025-06-18/server/tools) and [Sentry's `update_issue` source](https://github.com/getsentry/sentry-mcp/blob/main/packages/mcp-core/src/tools/catalog/update-issue.ts).

A centrally reviewed Sentry policy can attach conditions to a group and tool:

1. Match `organizationSlug` to an exact allowed organization. Supply the fixed value where the upstream schema allows omission; reject a different value.
2. Parse `issueUrl` as a URL and check its claimed organization. Before any call that uses an issue URL or ID, verify the issue's actual organization and project through a trusted lookup or a verified upstream scoped-session check. A URL or prefix in `issueId` is not proof of ownership.
3. Restrict allowed operations and values. For example, a group may resolve issues but not ignore or reassign them. Validate the complete call before forwarding it.
4. Treat `execute_sentry_tool(name, arguments)` as a dispatcher. Authorize the selected `name` and its nested arguments against the same policy; otherwise deny this wrapper. `search_sentry_tools` can reveal more operations than the nine top-level tools, so discovery results are not authorization.
5. Reject new or changed schemas until the applicable policy has been reviewed. Log denied calls as well as allowed calls.

Regex is useful for input format checks, such as a slug pattern. Exact matching, URL parsing, resource ownership lookup, and upstream scoped sessions are needed for authorization. Argument policy gives finer rules within an organization or project, but requires maintained, connector-specific knowledge for every allowed operation and its indirect resource references. If a tool cannot be scoped confidently, do not grant it under a claimed organization/project restriction.

## How the two ways fit together

Use a Sentry-scoped connection as the project or organization boundary, then apply CapLayer tool and argument rules for the employee's role. For example, the Payments readers and triagers can share the same `/mcp/acme/payments` upstream connection while only triagers receive `update_issue`. A narrower argument policy can allow only `status: resolved` for a particular group. Both checks run on every call; hiding a tool in `tools/list` is not sufficient authorization.

## Minimum verification before team use

- Confirm the centrally managed credential can call every intended Sentry MCP operation, including `update_issue` where required. Record the granted API scopes and test credential rotation.
- Confirm each group receives only its approved connection and tools in `tools/list`; directly calling a hidden tool must be denied.
- Attempt reads and writes against a second organization and project using explicit slugs, issue URLs, issue IDs, search queries, and `execute_sentry_tool` nested arguments. All cross-scope calls must fail before returning data or changing state.
- Verify schema changes and newly discovered catalog operations are denied until reviewed.
- Verify audit records identify the CapLayer employee and never expose the Sentry token.
