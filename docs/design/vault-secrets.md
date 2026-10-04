# Vault secrets and product integrations

Current overview and verification: [Vault implementation status](vault-current-state.md).

## User flow

All products use `SecretSelectionSection`. Crew, Code and Goals place it under
Integrations > Secrets. Video Studio's Integrations dialog and SparkQuill's
Integrations panel use the same component. Project and Vault are separate sources.
Project owners manage project values; the existing project sharing rules still apply.
Only names and references are written to project configuration.

Vault > Secrets centrally creates, rotates, reveals and deletes shared credentials.
Vault > Access > existing group > Permissions > Secrets grants **use**, not reveal.
Product administrators manage values; that authority does not implicitly grant
runtime use. Open Vault shortcuts obey current product and administrator access.
The shared-source information box identifies these as **Platform secrets** and
points administrators to **Access > Groups > Permissions**. Project selection
describes only the shared secrets permitted by the user's groups. The copied-value
warning remains available under **Removing access**.

## Share a project secret to Vault

In the shared project Secrets list, Vault administrators with project write access
can click the **Share to Vault** icon beside a stored secret. Choose a Vault name
and one or more existing groups, then share. No group is preselected, including
Platform. Ordinary project users cannot publish shared values or grant access.
This works through the same component in Crew, Code, Goals, workflows, Relays,
Video Studio and SparkQuill wherever the project secrets list is used.

The server authorizes both Vault management and the source workspace, reads and
decrypts the source internally, and encrypts a new global copy with its Vault name
as authenticated data. The browser and builder send only source/name/group
references. The source encrypted record, selected names, and existing project
execution stay unchanged. Existing Vault names are never overwritten: choose a
new name or manage the existing secret in Vault. Copies rotate independently.
Recipients still explicitly select the Vault name in destination projects, and
runtime use remains subject to their current group membership and grants.

Builder chats use the existing shared `manage_global_secret` tool:

- `action="list_groups"` returns group IDs and descriptions, with no values.
- `action="share"`, `name`, `group_ids`, optional `vault_name`, and optional
  `source_workflow_path` perform the same host-side operation as the UI.
- Omit the source path to use the active project; use `list_secrets` to discover
  source names. Ask the user about recipient groups when unspecified.
- Read-only sessions omit management tools; authorization is checked again when
  the tool runs. The integrations skill documents this flow.
- The legacy `promote` action retains its move semantics for compatibility.

`GET /api/secrets/vault/share` lists available groups for administrators;
`POST` shares by reference. Group selections are checked before saving a value.
The Vault grant endpoint validates the whole group list before changing grants
and persists it in one store operation. There is no distributed transaction
between the host value store and Vault: if metadata or grant persistence fails
after the copy, the response explicitly reports that the value was saved and
access needs review in Vault. It never reports a completed share, removes the
source, or overwrites an existing global on retry. The UI keeps the form and
shows the error.

## Built-in platform group

Vault installation creates **Platform**, a built-in group for sharing MCP tools
and secrets across Code, Crew, and workflows. Startup runs the same idempotent
bootstrap for existing installations. `scripts/install-vault.sh` installs the
Vault binary and calls its bootstrap mode with the configured project and state
folders. Stop an existing Vault service before running the installer so the
single-writer database lease is available.

Platform starts with no grants. Administrators assign specific MCP tools, servers,
and secrets in Access; projects still select which shared resources they use.
All active platform users are automatic members. The host validates account
status before a service-authenticated runtime request binds a new platform
identity. Disabled accounts cannot use this path. Browser-supplied identity
headers are discarded, and group-scoped API keys retain their single group scope.

The built-in group has a stable workspace-specific ID. Installation preserves
existing permissions, membership and descriptions. Its name and automatic
membership cannot be changed or deleted through the UI, API or database mutation
tools; its description and resource grants can be edited normally.

## Group descriptions

Groups have an optional description displayed below the selected group name in
Access and People. Edit it with the group name using the pencil button; new groups
can include it at creation. Clearing it preserves the group name and all grants.
Descriptions are informational and never determine authorization.

The `groups.description` column is available to the agent's existing query and
mutation tools. Existing SQLite workspaces add this column automatically with an
empty default. Descriptions persist in the encrypted configuration and its SQL
projection, with the same workspace checks as group names.

## Storage and authority

Values reuse the existing AES-GCM encrypted host secret store. Managed shared
values are stored under `_users/_system_global_secrets/secrets.json`, bound to the
secret name and encrypted using the server secrets key. Environment-defined
`GLOBAL_SECRET_*` values remain externally managed and cannot be rotated in the UI.
Project values retain their existing workflow-bound encrypted store.

Vault's project SQLite database persists metadata and grants:

- `vault_secrets`: immutable name/reference, workspace, managed flag.
- `group_secret_grants`: group and secret name; contains no values.

These tables are available to `query_workflow_db`. Grant writes use the dedicated
`manage_vault_secret_access` tool or validated HTTP API through the database owner.
Direct SQL writes are rejected. The host service credential stays backend-only.
The grant lifecycle uses the existing encrypted configuration transaction and
policy history, including grant/revoke/deletion. Membership removal immediately
changes subsequent permission resolutions.

## Runtime

The host asks Vault for the executing user's permitted names, using service
credentials and a server-owned identity. It intersects those names with the
project's explicit selections. No browser-provided actor is trusted. The same
resolver is used by chat, profile runtimes, workflow runs, builder phase runtimes,
report scripts, notification resolution, and delegations.

Omitted/null shared selections mean none. A failed permission lookup never falls
back to server globals. Required unresolved selections stop normal chat/profile,
workflow and report startup. Project secrets retain precedence on name collision.
A selected environment secret is injected as `SECRET_<NAME>` only after permission
checks. Native workspace environments exclude inherited `GLOBAL_SECRET_*`,
`CAPLAYER_SERVICE_*` and the gateway administration credential.

Rotating a shared value retains its reference and grants. Deletion revokes all
grants before deleting the encrypted value. Already-running processes cannot
have a previously supplied credential recalled; revoke that credential upstream
when necessary. Shell/native agents can read credentials supplied to their process.
The UI and normal metadata/list tools do not reveal these values.

## Revocation and copied credentials

Removing a group grant stops future secret resolution. It cannot recall a value
that a user or local process has already obtained, whether in a file, environment
variable, or another copy. Deleting the Vault entry also does not invalidate that
credential at its provider.

To invalidate existing copies, revoke or rotate the credential at its provider,
then save the replacement under the same name in Vault. Updating Vault alone does
not revoke the old credential. Authorized consumers keep their references and
grants; existing processes may need to restart to pick up the replacement.

The shared Secrets UI highlights this limitation in management and
project selection. The rotation icon opens the saved-value editor; its
instructions explain that Vault stores the replacement and does not perform
upstream credential rotation.

Secret value rows offer a copy icon only where value reveal is allowed. Copying
performs a fresh authorized reveal/decrypt request and writes directly to the
clipboard; it does not display the value in the page. Group permissions and
project selection of shared secrets remain metadata-only.

## Migration

Existing project secret stores and names remain intact. Existing globals are
registered idempotently when the administrator opens Vault > Secrets or assigns
secret access. Registration does not create any group grant. Assign appropriate
existing groups and retain explicit project selections. Existing names in manifests
remain references; no plaintext is copied into projects. A legacy implicit-all
selection intentionally grants nothing under the new rules. This is an access
change, so review affected scheduled runs before production rollout.

Values and permissions live in separate stores. If registration fails after an
encrypted save, the value remains stored without new access; refresh Vault before
assigning it. Back up both encrypted stores and their encryption keys through the
existing protected deployment backup process. Never commit secret values or keys.

## External agents / MCP

MCP connection tokens remain internal to each connection. This change does not
add a `get_secret` MCP tool or expose shared values to Claude. External tool calls
continue to use connection credentials on the backend. Whether general shared
secrets should also support an explicitly authorized value-fetch tool is a separate
product decision. Group **use** permission must never implicitly mean **reveal**.
