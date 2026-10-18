# Connect just this project

Use & share → MCP access opens project-scoped browser sign-in for Goals, Flows and both Crew storage origins. The server resolves the current workspace to its stable project ID. The setup URL grants no access: each recipient signs in with their own account, approves the connection and must already have project access.

## Capability and audience

The OAuth resource is `/api/external/v1/mcp/project/{work|code|workflow|relay}/{id}/{view|use}` on the configured PUBLIC_URL. It contains no credential or workspace folder. The existing OAuth store preserves that exact resource through authorization, PKCE code exchange and refresh. Consent also saves a hash of the original project kind, ID and creation date (plus the immutable creator for Goals/Flows) in the server-owned OAuth store. Code exchange and refresh preserve it, and the token endpoint and every project request compare it against fresh project metadata. A replacement project with a reused ID cannot inherit the old connection. Missing legacy creation metadata or old project grants without this binding fail closed and require restored metadata and a new approval. The token endpoint and every authenticated MCP request require the same audience; neither another project URL nor changing View to Use can widen a grant.

View offers permitted project reads and dashboards. Use also offers published functions and, for Goals/Flows, existing run controls. The general `chat` tool is excluded: its inbox replies require global messages access, which a project connection never receives. Existing published-function execution still uses the Run session boundary and `RunExternalDenylist`; this connection does not authorize Builder or a new assistant path. Both intersect current project permissions and existing function allowlists. Project settings, Builder, global administration, account inventory, Brain/Vault tools, AgentBridge and legacy code:run are excluded from this connection. Crew's Code origin uses the existing shared file/dashboard/function services; it does not enable the old private Code execution API. Native website features and the separate server-wide connection remain available through their existing permissions.

The server derives project kind, ID and access mode from the stored OAuth resource. These server-only fields cannot be submitted through PAT creation. Project grants do not receive the general account sign-in scope upgrade, even if their account later becomes an administrator. Existing OAuth connection expiry, refresh rotation and revoke controls are reused. The connections list includes each grant's resource, and the project panel lists only connections for that same project.

## Live permission enforcement

The project is resolved and authorized on every MCP request. Crew/Code use the existing project resolver, sharing registry and product gates. Goals/Flows use the existing manifest index only to locate the stable ID's current folder, then read the manifest again to check its current ACL. Cached permissions never authorize a request. Removing a share or disabling an account ends its access on the next request. Moving project storage does not rewrite the OAuth resource.

Existing ID filters restrict workflow/Crew discovery and workspace/dashboard listings. The project origin is also checked, so matching IDs in another product cannot be substituted. The project catalog uses an explicit member allowlist, followed by existing scopes and role checks; merged tool schemas expose only admitted actions. Calls by alternate selectors still pass the existing resolved-project gates.

## Verification and rollout

Isolated HTTP OAuth tests cover all four origins and View/Use, exact resource metadata/challenges, consent, PKCE, token exchange, refresh and revoke. Permission tests cover scoped discovery, wrong IDs/origins/paths, restricted catalog, View execution denial, sharing removal for a second temporary account, Code's stable ID differing from its folder, and dashboard listing with another accessible project. Fixtures use temporary storage and loopback workspace services, with no provider/model requests or production access changes.

Frontend tests cover server-resolved project configuration, View/Use URL selection, load failures, no credential creation on navigation, local project OAuth versus the existing global setup, connection instructions and project-specific consent.

No merge or production deployment is authorized by this document. Production acceptance is separate from isolated fixture verification.

Review hardening adds a minimal consent DTO (`id`, `title`, `kind`, `access`), an independent handler audience/bounds check, and structured grant/refusal audit lines containing only family ID, kind, project ID, access and reason. Tokens, credentials, paths and conversation content are excluded. Isolated lifecycle tests cover ID replacement, refused refresh, missing creation identity and handler checks without middleware.

Crew identity deliberately excludes the current owner and workspace path: ownership changes, ownership backfills and folder moves retain the creation binding. Goals and Flows include their immutable creator. Audit events also identify the signed-in user.
