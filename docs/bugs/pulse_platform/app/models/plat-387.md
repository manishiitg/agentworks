[← app / models](index.md)

# PLAT-387 — Cursor curated models can exceed an account's live availability

| Field | Value |
|---|---|
| State | open |
| Priority | - |
| Product | app |
| Area | models |
| Summary | open, reproduced on RTS. |

| Coordination | Value |
|---|---|
| State | open; reproduced with RTS service key |
| Date | 2026-10-03 |
| Owner | frontend-chat; account-scoped discovery in coding-agent-bridge |

## Evidence

Cursor documents GLM 5.3/Flash and the curated catalog includes them, but RTS's
live `--list-models` exposes GLM 5.2 High/Max and Grok 4.6 variants, not either
5.3 model. Both 5.3 selectors exited 1 with `Cannot use this model` in isolated
CLI inference probes using the service's existing key. Grok inference succeeded.

`WorkModelsPanel` unions live choices into the curated manifest; a successful
list does not remove curated choices that this account cannot use. Discovery is
currently requested by provider, so filtering cannot assume the server key's
inventory is also a personal account's inventory.

## Left and acceptance

- Resolve discovery/cache identity against the selected connection and credential
  version, then use successful account inventories to constrain or clearly mark
  unavailable choices. Define a fallback for failed discovery without swapping
  the saved model or exposing another account's private inventory.
- Test two accounts with different catalogs, unsupported curated models, failed
  discovery and an unavailable saved selection. Qualify any GLM availability claim
  with the selected account's live list and successful inference.
- [PLAT-386](plat-386.md) tracks the completed setup/selection boundary.

## Register notes

[PLAT-387](plat-387.md), open, reproduced on RTS.
A curated/live catalog union still offers GLM 5.3/Flash to a key whose CLI rejects
them; discovery and filtering need selected-account identity.
