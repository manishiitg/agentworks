# MCP ownership across AgentWorks, Crew, Code and workflows

Current overview and verification: [Vault implementation status](vault-current-state.md).

MCP consumers use the same platform SSO identity even when accessing only Vault
from an external client. People > Users remains the shared platform directory.
Individual MCP OAuth consent binding is still unfinished in the local alpha.

## User experience

Crew, Code and workflow integrations show the same two sections. Ordinary chat MCP details also include the shared Vault section:


- **My MCPs**: connections private to the authenticated person. Credentials can
  be reused across that person's projects. Sharing a Crew, Code or workflow does
  not share the person's external account.
- **Vault**: shared connections and tools visible under that person's current
  group grants. Selecting a connection does not grant access. Administrators
  manage shared connections and groups in Vault.

New OAuth and regular MCP Connect requests default to `private`. Vault's OAuth
buttons send explicit `scope: vault`; the server checks the central admin role.
Chat MCP install/add/edit/remove tools operate on the caller's private store.
`MCP_CONFIG_LOCKED` protects the shared catalog/overlay; private account storage
is separate and follows the existing private My MCPs behavior.
Secrets and OAuth client secrets must be entered outside chat.

## Storage and compatibility

Private metadata and sealed credentials reuse the existing `personal-mcp` store
under the host's state root, partitioned by the hashed user ID. New project
attachments record `scope: user`, pointing at that store. The server-owned
attachment index contains no tokens. Removing a project attachment preserves
that private account for other projects; deleting the private account removes
its sign-in.

Existing per-project stores retain their original paths and encrypted files.
Their runtime access is restricted to their original owner. No migration copies
private credentials into shared project secrets.

Existing platform OAuth credentials retain the `_platform` namespace and any
previous explicit token path. They are now usable only through Vault's exact-URL
service broker. Existing catalog/overlay definitions are discovery metadata,
not an execution authorization source. There is no direct platform credential
fallback. An administrator must add the connection to Vault and assign groups,
or its user must reconnect privately. This deliberately stops old universal
access; plan group assignments before rolling out to an existing deployment.
Legacy header secrets previously copied into shared project secrets are not
silently deleted: administrators should rotate and remove those old shared
values as part of migration.

## Enforcement

Both chat and workflow-step agent constructors apply the same host MCP scope.
Builtin-only and private-only agents do not contact Vault; shared discovery is
limited to one request per construction.
Workflow preflight and Crew creation inspect the run owner's private/Vault
availability rather than treating a global catalog definition as connected.
Discovery configs contain only the caller's private connections or a Vault
proxy, never the gateway service secret or upstream Vault credentials.

Vault delegates are signed, bound to a user and one connector, and expire in at
most 24 hours. Live agent delegates also bind to their session; the host denies
use after session ownership changes. The host checks the central account on
requests and overwrites all service identity headers. The gateway service MCP
transport is stateless and accepts service authentication only; browser cookies
cannot authenticate it.

The gateway filters `tools/list` and rechecks group membership, active connector,
approved schema and argument conditions on `tools/call`.
Connector scope prevents calling another connector even when the user has
permissions for both. Removing a grant stops the next call on a warm connection.
All calls retain the existing audit trail.

The MCP library pools live connections globally by server name. Private runtime
names include the user identity. Vault runtime names include a hash of the
signed delegation, separating actors, sessions and token lifetimes. Tool
selections are remapped to those names so specific tool restrictions survive.
The host preserves sealed OAuth paths rather than allowing the SDK to overwrite
them with legacy catalog token paths. Catalog search and tool details consult
only the caller's private connections; a configured global template is never
reported as the caller's connected account. Private tool metadata is not
persisted in the browser, account switching clears MCP state, and late responses
from the previous account are ignored.

Project selections constrain which tools the agent loads. Vault group and
resource policies are the execution security boundary, including for native
coding-agent tools and calls through retained MCP clients. Do not use project
selection alone to impose a security restriction; put it in Vault policy.

## Validation

Regression coverage includes two users with different credentials for the same
provider; forged private runtime overrides; legacy platform credentials denied
without Vault; signed delegation tampering/expiry; host identity headers;
service-only inventory; connector-specific discovery/calls; group revocation
on a warm upstream; and UI selection/schema/revoked-selection behavior.

The separate OAuth `AUTH_SECRET` rotation gap from the previous security review
is still tracked; this ownership refactor does not claim to fix key rotation.


### Checks performed (2026-10-01)

- Full gateway `go test ./...`: passed, including the existing end-to-end suite.
- Focused host tests: passed for private HTTP connections and secret storage,
  private metadata, forged runtime overrides, session ownership, Vault delegation,
  Crew creation, Code selection, workflow scope, OAuth broker, and legacy stores.
- Focused common/agent wrapper/workflow tests: passed, including workflow preflight.
- 47 frontend tests across 11 files: passed, including Vault selection and schemas,
  private key setup, account response isolation, and Crew/Code/workflow integration.
- Full frontend production build, TypeScript, release asset and bundle budget
  checks: passed. JS gzip is 986.55 kB, above the 950 kB warning threshold and
  below the enforced 1030 kB budget.
- The full host server suite is **not green**. The run found unmodified sales and
  Playbook catalog expectations (including a 40-entry expectation against 64
  entries), a Pulse bridge fixture with an invalid generated `go.work`, and hit
  the overall 120-second timeout. Two ownership fixtures expecting old shared
  stores were updated and passed in the focused rerun. The full suite was not
  rerun after those fixture fixes; do not treat the focused tests as a full pass.
- Live browser interaction was blocked by the in-app browser's CDP focus and
  navigation timeouts. Rendered component tests passed; this does not constitute
  visual verification of the live Crew, Code and workflow panels.

The local backend and Vault service are rebuilt separately from the frontend.
The preview stays at `http://127.0.0.1:18162/`. Existing storage paths, encryption
keys and local test MCP processes are retained during restarts.


Local HTTP checks after restart: UI and product health returned 200; authenticated
private/catalog and group-filtered Vault inventories returned 200; unauthenticated
inventory access returned 401. The local user currently has zero connected private
accounts and zero group-permitted Vault servers, despite global catalog entries.
Vault management inventory remains available to the local administrator. No group
grants were added by these checks.

## Shared MCP UI (2026-10-01)

`frontend/src/components/integrations/McpConnectionsPanel.tsx` owns the MCP
browser for Vault, Crew, Code, workflows, and the ordinary-chat MCP dialog.
It renders search, sorting, connected and available views, catalog categories,
server action menus, selection controls, tool cards, JSON arguments, and common
loading/error/empty states. Credential and OAuth client forms also live in the
shared integrations directory.

Product adapters supply normalized connections, catalog entries, and authorized
callbacks. Private connections, group-filtered Vault inventory, and central Vault
administration retain their separate API/auth scopes. A project selection never
creates a group permission. Vault-only tool version review is an action extension
inside the shared tool card. Do not add another product-specific MCP browser or
copy the server/catalog/tool layouts into an adapter.

The old `PlaceMcpSection` and `VaultMcpSection` layouts are removed. The Vault
server adapter and ordinary-chat dialog also render this single browser. Live
Crew catalog and Vault connected-tool/schema views have been checked in the
in-app browser. No local memberships or grants were changed for verification.
