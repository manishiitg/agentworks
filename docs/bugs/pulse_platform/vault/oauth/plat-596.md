[← vault / oauth](index.md)

# PLAT-596: Apify rejects Excellence callback missing from AgentWorks client metadata

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | vault |
| Area | oauth |
| Summary | Corrected AgentWorks client metadata is public; Apify now accepts Excellence's callback and reaches account sign-in. |

## What happened

Reported 2026-10-06 by Utkarsh while connecting Apify in Vault on Excellence.
Live agent logs show Vault connection `c-be22ea261732ed6824bfe2cac4522dd1`
started OAuth at 15:15:22 and 15:19:43 IST, then timed out without a callback.
The agent's `PUBLIC_URL` is `https://agents.excellencetechnologies.in` and its
Apify config uses `https://agentworkshq.com/.well-known/mcp-client.json` as
`client_id`. That public JSON lists RTS, Confida, Trader and local callbacks,
but omits `https://agents.excellencetechnologies.in/api/oauth/callback`.
Apify therefore refuses the redirect before account sign-in. Vault and the
agent service were running; this is an OAuth client metadata configuration issue.

Evidence was read directly from Excellence and both public metadata endpoints.
Apify advertises client ID metadata document support at
`https://mcp.apify.com/.well-known/oauth-authorization-server`.

## Fix

Add the exact Excellence HTTPS callback to the `redirect_uris` array in
`multi-builder-public-website/.well-known/mcp-client.json`. This preserves
existing callbacks and introduces no wildcard. The website preparation script
already copies this file to `dist/.well-known/mcp-client.json`.
No Excellence agent or Vault binary change or restart is required.

Verification: parse the corrected JSON, check that its callbacks are unique,
and check the exact one-entry addition against the fetched public document.
Website commit `afcda4d` (originally referencing PLAT-593 before concurrent ticket allocation was reconciled to PLAT-596) is on `multi-builder-public-website` main and its
automatic deployment is live. A fresh browser authorization request with the
Excellence redirect reached Apify's "Log in to Apify" page rather than the
redirect rejection. Human Apify sign-in/token exchange has not been performed.

## Left

- The affected person should retry Apify sign-in with a fresh Vault authorization
  link. Their account consent/token exchange requires their own login.
- No application redeployment is needed for this metadata correction.
