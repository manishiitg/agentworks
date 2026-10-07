[← vault / apps](index.md)

# PLAT-670: Vault add app: unknown provider for Github

| Field | Value |
|---|---|
| State | open |
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

- GitHub, Google and Slack in Vault need per-app OAuth clients (decision for the owner).
- The add-app box is free text; a picker from the catalog would avoid typos.
