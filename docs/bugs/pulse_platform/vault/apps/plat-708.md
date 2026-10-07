[← vault / apps](index.md)

# PLAT-708: Vault: Vercel sign-in fails, registration rejected and authorize opened without a client

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | vault |
| Area | apps |
| Summary | Vercel rejects dynamic client registration for any hosted callback; we then linked people to vercel.com/oauth/authorize with no client. Now the registration error is logged and shown, and no authorize URL is given without a client. |

## What happened

Confida, 2026-10-07 16:38 CEST: a person picked Vercel in Vault → Add app (PLAT-670).
The agent log showed `Dynamic client registration failed for Vercel: ... status 400`
then `No client_id for Vercel, returning needs_client_id response`, yet Vercel showed
"App configuration error: The app ID is invalid / The app redirect URL is invalid".

Two faults:

1. **Vercel's DCR only accepts approved redirect hosts.** Tested from a laptop with the
   exact body we send (`token_endpoint_auth_method: none`, `authorization_code` +
   `refresh_token`, `code`, `client_name: AgentWorks`) against
   `https://api.vercel.com/login/oauth/register`:
   - `http://localhost:8000/api/oauth/callback`, `http://127.0.0.1:...`,
     `https://claude.ai/api/mcp/auth_callback`, `cursor://...` → 201
   - `https://<confida host>/api/oauth/callback`, `https://agentworkshq.com/...`,
     `https://example.com/...` → 400
     `{"error":"invalid_redirect_uri","error_description":"The provided redirect URIs are not approved for use by this authorization server."}`

   So the request body is fine; Vercel allowlists known clients' callbacks and a hosted
   AgentWorks callback cannot self-register. Nothing in the request can change that.
2. **We opened an authorize URL anyway.** The `needs_client_id` reply carried the
   provider's `auth_url` (the bare authorization endpoint), and the Vault screen linked
   the first URL in the tool reply. Place MCP connect also opened any `auth_url` before
   checking `needs_client_id`.

## Fix

- mcpagent `c13ab2a`: `RegisterClient` returns `*oauth.RegistrationError` with the RFC 7591
  `error`/`error_description` and a truncated one-line body (a rejection holds no secret).
- agent_go `runOAuthFlow`: logs the error with the redirect URI; the `needs_client_id`
  reply no longer has `auth_url`/`token_url` and, after a failed registration, says
  "<App> sign-in couldn't be set up automatically: the provider rejected the app
  registration: <reason>. An admin can register an OAuth app for it, or store its API key
  as a Vault secret."
- Vault person tool (`connect` / `sign_in`): no sign-in JSON when there is no client;
  `connect` keeps the connection and returns that text, `sign_in` returns it as an error.
- Vault screen links only the reply's `auth_url` and otherwise shows the reply text;
  place MCP connect checks `needs_client_id` before opening anything.
- Test: `TestFailedRegistrationReturnsNoAuthorizeURL` (agent_go), and
  `TestRegisterClientRejectionKeepsTheProviderReason` (mcpagent).

## Follow-up: "Needs admin setup" in Add app (owner decision, 2026-10-07)

Apps whose sign-in cannot start on its own stay in Vault → Add app but carry a
"Needs admin setup" badge (tooltip: "Sign-in can't be set up automatically here; an admin
can register an OAuth app for it, or use an API key as a Vault secret") and sort last. They
can still be picked; the error above explains.

- The `apps` operation returns `needs_admin_setup: true` for an OAuth app with no
  configured client (none in the config, no deployment sign-in app) when it has no
  `registration_endpoint`, needs a hand-registered confidential client, or already
  rejected this server's callback.
- Rejections are remembered in `<tokens root>/_platform/registration_failures.json`, keyed
  by registration endpoint + callback, written where the RegistrationError is logged in
  `runOAuthFlow` (provider rejections only, not network errors); a later successful
  registration removes the entry, and a configured client overrides it.
- Workflow/Crew connect messages name the catalog app ("Vercel"), not the internal server name.
- Test: `TestFailedRegistrationReturnsNoAuthorizeURL` now also checks the apps list marks
  and sorts Vercel, and that a configured client clears the mark.

## Left

- Vercel itself cannot be connected from a hosted server unless Vercel approves our
  callback or an admin enters a Vercel OAuth client by hand. Asking Vercel to approve the
  AgentWorks callback is the remaining option.
- A server only learns of a rejection after someone tries; until then Vercel shows as a
  normal sign-in app there.
- Not deployed.
