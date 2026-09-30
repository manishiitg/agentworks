# Google accounts through the server's Google app (gog)

Status: built 2026-09-30, on main, not deployed. Owner decisions the same day: Google apps are
the one integration to focus on; Google's Workspace MCP servers and GitHub MCP are not offered.

## Why not MCP

Google's Workspace MCP servers (Gmail, Drive, Calendar, Docs, Sheets, Slides) are a Google
**Developer Preview**: Google refuses the sign-in unless the Google Cloud project behind the OAuth
client is enrolled and has each MCP API enabled. That is out of our hands. The `gog` integration
uses Google's regular APIs, works with a normal OAuth client, and already runs server-side:
`google_workspace_cli` injects the person's token on the server (it never reaches the agent's
shell) and enforces read-only with `--readonly` and `--gmail-no-send`. GitHub is a personal
access token (a `GITHUB_TOKEN` secret) used with git and the API.

## What people see

- **Admin, once:** Integrations → Gmail → "Google sign-in app": upload the Google OAuth client JSON
  (or paste the id and secret). It is the deployment's Google app, stored sealed at
  `<tokens>/_platform/apps/google.json` (the same store the MCP sign-in used; RTS and excellence
  already have it). The card shows the redirect URI to register and the APIs to enable.
- **A Code's owner:** Integrations → Gmail → **Connect Google account**: choose what the agent may
  use (Gmail: not used / read only / read, draft and send; Drive, Calendar, Docs, Sheets, Slides:
  not used / read only / read and edit), sign in with their own Google account, personal or work.
  No file to upload and no Google Cloud project of their own. The connection is private to that
  Code and its owner (`GmailConnection.ScopeWorkspace`, `UsableFrom`).
- **The agent:** `google_workspace_cli` (gog), for that Code's owner only. A read-only session gets
  read tools only.

## How it works

- `services.EnsurePlatformOAuthClient` registers the stored app as the reserved named gog client
  `platform` (gog reads its clients by name) and keeps it in step: unchanged if the id and secret
  match, a rotated secret keeps the same client id so logins survive, a different client id replaces
  it (connections made under the old client must reconnect).
- `POST /api/human-feedback/gmail/google-app/connect` (`GoogleAppRoutes`) checks who is asking first
  (only a Code's owner, or an admin for a shared account), ensures the client, then creates the
  connection with `ClientName: "platform"`. The UI then calls the existing
  `/connections/{id}/auth/start`.
- The sign-in returns through `/api/oauth/callback`, the redirect URI the Google app already
  registers, so nothing new has to be added in Google Cloud. `handleOAuthCallback` hands a state
  that belongs to a pending Gmail sign-in (`services.HasPendingGmailOAuthState`) to the Gmail
  completion; every other state stays an MCP sign-in.
- `GET /api/human-feedback/gmail/google-app` says whether the server has a Google app and the
  redirect URI; the component renders only when it does.

## Google-side requirements (not visible to us)

- The Cloud project behind the client needs the **Gmail, Drive, Calendar, Docs, Sheets and Slides
  APIs** enabled.
- Consent screen **Internal** lets anyone in the Workspace organisation sign in with no review.
  Personal `@gmail.com` accounts need an **External** screen: until Google verifies the app only up
  to 100 test users you add by hand can sign in, with a warning screen. Gmail permissions are
  "restricted", so public use needs Google's verification.

## Sign-in apps and MCP

The MCP catalog no longer offers Google or GitHub (`mcpCatalogHiddenKeys`), and the only sign-in
app card is Google (`mcpAppFocusKeys`). The MCP-side use of an admin app (a connection reading its
app at refresh time) stays for connections that already exist; removing the feature entirely needs a
migration that copies each connection's app into its own client first.

## Not done

Crew and workflow shared Gmail accounts keep their existing admin-only flow (upload a client per
mailbox); pointing them at the same Google app is a small follow-up. A live Google sign-in has not
been run (it needs a real client); the redirect and the callback hand-off are covered by tests.
