[← vault / apps](index.md)

# PLAT-670: Vault add app: unknown provider for Github

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | vault |
| Area | apps |
| Summary | Adding an app in Vault failed with a bare 400 'unknown provider': names were matched exactly, and apps outside Vault's catalog (GitHub, Google, Slack…) gave no hint |

## What happened

## Fix

## Left

## Report

#agent_works, 2026-10-07 14:11 (Saurabh, Confida): My Vaults → add app "Github" → "vault operation failed (400)". The Vault service's catalog matched the typed name exactly, and its built-in catalog (62 apps) has no GitHub at all: like Google and Slack, GitHub needs its own registered OAuth client, which Vault's shared OAuth service cannot create.

## Fix

- The catalog matches the typed name by its normalized key ("Github" finds "GitHub", "google drive" finds "GoogleDrive").
- An app Vault cannot connect is refused with its name, the list of apps it can, and the alternative (store the app's API key as a Vault secret).

## Left

- Decided (owner 2026-10-07): Google apps, GitHub and Slack stay out of Vault; they use the platform's own integrations. Vault's refusal points there.
- Done: the add-app box is now a searchable picker of Vault's catalog (see below).

## Follow-up (2026-10-07 16:03, Rakesh still saw "Vault operation failed (400)")

The platform passed Vault refusals through only up to 300 characters; the new message with the app list is longer, so people still saw the bare status. The limit is now 2000. A picker of Vault apps instead of free text is being built separately.

## App picker (2026-10-07, asked by Rakesh on Confida)

My vaults → Add app is a searchable list of the apps Vault can connect, not free
text, with a "Sign-in" badge on apps that need one; the optional name field
stays. Under it: Google apps, GitHub and Slack connect through their own
Integrations; any other app's API key can be saved as a vault secret. The list
comes from a new `manage_my_vaults` / `POST /api/my-vaults/op` operation `apps`,
which reads the Vault service catalog (`GET /api/admin/catalog`, reached with the
host's service credential as the person) and returns only `{apps:[{name,
oauth}]}`, never upstream URLs. Not deployed.
