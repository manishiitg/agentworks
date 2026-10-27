# SparkQuill MCP

Connect an external assistant to the family's SparkQuill learning workspace through
`https://your-sparkquill-server/api/external/v1/mcp`. In the parent interface, open
**Settings → Manage AI apps (MCP)** (also available under **Connectors → AI apps (MCP)**) for client-specific commands and connected-app revocation.
Sign in and approve the connection in the browser; clients never receive the parent PIN
or connected provider credentials.

```sh
claude mcp add --transport http sparkquill 'https://your-sparkquill-server/api/external/v1/mcp'
codex mcp add sparkquill --url 'https://your-sparkquill-server/api/external/v1/mcp'
codex mcp login sparkquill
```

## Permissions and product boundary

A dedicated SparkQuill deployment (`AGENT_PRODUCTS=sparkquill`) advertises only
SparkQuill permissions and tools. Existing AgentWorks tokens cannot access a platform
catalog through that server. Shared deployments select this catalog for connections
with explicit SparkQuill permissions; ordinary platform connections retain their catalog.
SparkQuill and platform permissions cannot be mixed in one token.

- `sparkquill:read`: learning context, materials, activities, saved progress and content files.
- `sparkquill:write`: file creation/replacement/upload, activity finalization and child handoff.
- `sparkquill:run`: delegate activity creation or revision to Quill with the parent's saved
  model/account and normal tools. This uses the parent's AI budget and connected accounts.

OAuth requests all three by default on a dedicated deployment and shows their effects
before approval. Viewer accounts receive read access only. Every call rechecks current
account/product access. Revocation invalidates the connection. SparkQuill PATs are
independently revocable and do not replace existing AgentWorks PATs.

Content paths are relative to the authenticated family's fixed `Chats/SparkQuill`
workspace. Reads cover `activities/`, `materials/`, `inbox/`, `reports/` and `memory/`;
direct writes cover the first three. Private state, credentials, PINs, product manifests,
chat history and symbolic links are excluded. Direct writes cannot change attempt records,
reports, child-profile notes or extraction metadata. Optional token folder guards narrow
access further; Quill delegation and finalization/handoff require unrestricted family grants.

## Catalog

MCP exposes `get_api_spec` and `call_tool`. Discover the following operations and their
schemas through `get_api_spec`, then call them through `call_tool`:

| Tools | Purpose |
| --- | --- |
| `get_learning_context`, `get_learning_progress` | Saved child profile, progress report and attempt file inventory |
| `list_materials`, `read_material` | Uploaded materials and extracted-text metadata |
| `list_activities` | Finalized activities, metadata and preview links |
| `list_files`, `read_file`, `get_file_link` | Browse/read content and open authenticated previews |
| `create_file`, `update_file`, `upload_file` | Create or replace text and binary assets with revision protection |
| `create_learning_activity`, `update_learning_activity`, `get_activity_status` | Delegate work to Quill and poll its durable job receipt |
| `finalize_activity`, `assign_activity` | Render/register finished content and explicitly hand it to the child |

Preview URLs open the normal parent interface and require family sign-in and parent access.
They are not public share links. Existing large media can be linked even when it exceeds
the direct file read/upload limit.

## Direct authoring

1. Read the learning context and relevant materials.
2. Write activity files beneath `activities/<slug>/` using `create_file`. Read an existing
   file before `update_file` and pass its `revision` as `expected_revision`.
3. Upload images, audio or other binary assets using `upload_file` with base64 content.
   Use `expected_revision: "missing"` for a new asset or the current revision to replace it.
4. Call `finalize_activity` with the directory, title and bare item filenames. It uses the
   existing SparkQuill renderer and activity/product manifest writer. A `.sq.html` source
   becomes a child-ready `.html` item. Inspect the returned render report.
5. Preview with `get_file_link`. Call `assign_activity` separately when the parent requests
   handoff; creation/finalization does not automatically assign the activity.

Text and binary reads/uploads are limited to **2 MiB per file** (decoded size for base64).
File writes require a stable `request_id`; repeat an uncertain request with identical
arguments to recover its receipt. Reusing an ID for different content fails. A revision
conflict requires reading and reconciling the current file before a new request.

## Quill delegation

`create_learning_activity` accepts a brief, a format (`game`, `quiz`, `book`, `video`,
`study-material`) and `request_id`. `update_learning_activity` accepts an existing activity
directory and a revision brief. Both return `job_id` immediately. Poll `get_activity_status`
for `running`, `completed`, `error` or `interrupted`, the exact final response and relevant
activity preview links.

Each job uses an isolated parent conversation and the saved family model, provider account
and reasoning settings. It does not interrupt the parent's live chat. One MCP job may run
per family at a time, with a 45-minute timeout. Job receipts live in the private durable
server state directory and can only be polled by their originating connection. Identical
submission retries recover the same job instead of spending the AI budget twice. A job
left running across a server restart is marked interrupted; inspect its files before
submitting another request. Uncertain work is never automatically rerun.

## Server rollout and verification

Deploy both the agent and workspace service from this revision: binary uploads extend the
shared guarded writer. The services must share `WORKSPACE_API_TOKEN`; the workspace service
remains internal. Configure `PUBLIC_URL` for OAuth and persist `AGENTWORKS_STATE_ROOT`
outside the public workspace across deployments.

Targeted server tests exercise the real HTTP MCP catalog, OAuth code exchange and revocation,
family confinement, guarded text/binary writes, finalization and assignment. Job tests use
synthetic turn results and do not call a live model. Frontend tests cover consent/setup and
preview path validation.
