---
name: "secure-share-links"
description: "Share existing workflow files and folders with authenticated links: path rules and the difference between access-controlled sharing and public publishing."
external: true
external_tools: ["get_file_link"]
external_note: "External mapping: get_file_link IS in your catalog: pass exactly one of workflow_id (files:read) or crew_id (crews:read), plus a canonical project-relative path; current project visibility, connection bounds and file guards apply. Its links grant no access; the dashboard tool (action=link) requires dashboards:read and returns authenticated URLs without starting a Run chat. manage_internet_share is not available. Use files download for local copies."
---
# Secure file and folder share links

Use this reference when a user wants a link to an existing workflow artifact
without publishing it. These are authenticated AgentWorks preview links, not
public hosting.

## Create a report link

For the active workflow's dashboard, call `dashboard` with `action=link` and no other arguments:

```json
{"action":"link"}
```

The server verifies access and confirms that `db/reports/index.html` exists,
then returns a dedicated `/report` URL. Present its `url` value verbatim. This
viewer uses the same report runtime as Builder, including the report's styling,
tabs, live data API, file actions, and refresh behavior. Do not use
`get_file_link` for the dashboard: its restricted generic HTML preview is not
the report runtime, and the tool rejects the dashboard entry path. The report
URL opens as a full-page dashboard after the normal AgentWorks SSO flow.

This internal report link is already protected by AgentWorks authentication and
workflow access. Do not add a second password gate, StatiCrypt page, or separate
publish login around it. That additional visibility/login setup belongs only to
a separately deployed static public report created through the publish flow.

## Create a file or folder link

Call `get_file_link` with a canonical path relative to the active workflow:

```json
{"path":"runs/latest/report.pdf"}
```

The server checks that the caller can still access the workflow, rejects
traversal and protected paths, verifies that the target exists, detects whether
it is a file or folder, and returns the correct `/file` or `/folder` preview URL.
Do not manually Base64-encode paths or construct these URLs in a prompt or
script.

Present the returned `url` value verbatim as the clickable link. Do not rewrite
it into `/file/<base64>` or `/folder/<base64>` and do not manually encode a
path. `preview_url` is retained as a compatibility alias for `url`.

Always inspect the returned `shareable`, `scope`, and `warning` fields. When
`shareable` is `false`, explain the warning to the user and describe the URL as
a same-machine preview, not a shareable link. In particular, a `PUBLIC_URL`
using `localhost`, `127.0.0.1`, or `::1` cannot be opened from another user's
computer or another device. A reachable deployment URL must be configured
before offering the link for sharing.

The returned URL contains no password, token, or access grant. A
recipient must sign in to AgentWorks and already have access to the workflow.
Removing that access also removes their ability to open the link. Never claim
that creating the link shared the workflow with a recipient.

When the links are local-only and the user explicitly asks to share one over
the internet, `manage_internet_share` (action `start`) can put this server on
the internet through a temporary Cloudflare quick tunnel, after which these
tools return reachable URLs. That exposes the WHOLE server, not one report —
admin-only, default 1h expiry, links die with the tunnel, and tunnel traffic
transits Cloudflare. Never start one without the user's explicit request, and
use the publish flow instead when the user wants durable public distribution.

Use the workflow access tool separately only when the user explicitly asks to
change who can access the workflow. Use the publish flow instead when the user
wants anonymous, externally hosted, or password-gated public distribution.

## Scripted steps

Load `references/mcp-bridge.md` before authoring bridge code. `get_file_link` is
a custom tool, so call it through `$MCP_CUSTOM/get_file_link`:

```bash
artifact_path='runs/latest/report.pdf'
payload="$(jq -cn --arg path "$artifact_path" '{path:$path}')"
curl --fail-with-body -sS --json "$payload" -H "$MCP_AUTH" "$MCP_CUSTOM/get_file_link"
```

Check the bridge response's `success` field before using its `result`. Parse the
tool result as JSON, inspect `shareable` and `warning`, and use `url` (or legacy
`preview_url`). Persist or send that URL only when the workflow contract
requires it and `shareable` is true; a local-only URL may be shown only as a
same-machine preview with its warning. A scripted step must pass the artifact's current
workflow-relative path; it cannot create links for another workflow or for an
arbitrary web URL.

For the workflow dashboard, use the same bridge pattern with an empty payload
and the report tool:

```bash
payload='{"action":"link"}'
curl --fail-with-body -sS --json "$payload" -H "$MCP_AUTH" "$MCP_CUSTOM/dashboard"
```
