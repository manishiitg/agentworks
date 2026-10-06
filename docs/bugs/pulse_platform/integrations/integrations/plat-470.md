# PLAT-470: One Google account UI for company and local OAuth clients

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | integrations |
| Area | integrations |
| Summary | fixed on main, not deployed: the same icon-based account/access form supports the company app, saved local clients and JSON uploads, and distinguishes AgentWorks settings from Google grants. |

State: fixed on main; not deployed. Local frontend receives the change on the required fast-forward update.
Priority: P2.

## Problem

The account UI used modern service icons and Change access only when the server had a platform Google app. Without it, locally uploaded named OAuth JSON clients fell back to older account cards and checkbox forms. A mailbox with Google-granted `gmail.readonly` but `allow_read_access` disabled could show both Send only and Gmail: Read, leaving users and Builder unclear about what to enable.

## Change

- WorkflowEmailPanel and the shared Gmail pane always use GoogleAccountList and GoogleAccountConnect across workflow, Crew, Code and Relay targets.
- The connect form offers the administrator-configured Company Google app, existing named clients, or a new named OAuth JSON upload. The company option needs no per-account upload. Named clients use the existing connection/auth APIs. Uploads never request replacement of an existing client; the reserved platform name is not available for upload.
- Existing accounts reconnect by their current ID and OAuth client, even when no platform app exists. Changes to optional services send the complete list with services_set, including clearing the list.
- Account cards show saved AgentWorks settings and checked Google permissions separately, explain already-granted Gmail reading that is disabled in AgentWorks, and flag selected access absent from Google.
- Existing owner/admin, workspace and read-only restrictions remain. No account permission is automatically enabled or changed. Failed app discovery is visible and does not hide legacy reconnection.
- Builder instructions and in-product help cover both sources and distinguish Google grants from the application read opt-in. Sign-in links remain copyable for another browser profile.

## Verification

Focused frontend tests cover both sources together, named-client selection, JSON registration without replacement, legacy reconnect with no company app, workspace isolation, permission drift, broader and restricted Google scopes, private/shared management restrictions, and service replacement. Frontend type checking and the full production build pass.

## Remaining

Deploy/restart hosted frontends when desired. No cloud app, Pub/Sub, user account or OAuth credential was changed by this task.

## Register notes

[PLAT-470](plat-470.md), fixed on main, not deployed:
the same icon-based account/access form supports the company app, saved local
clients and JSON uploads, and distinguishes AgentWorks settings from Google grants.
