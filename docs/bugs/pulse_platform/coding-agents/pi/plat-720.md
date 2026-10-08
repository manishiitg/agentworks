[← coding-agents / pi](index.md)

# PLAT-720: Model key checks fail after 30 s on slow services

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | coding-agents |
| Area | pi |
| Summary | On a slow model service (NVIDIA NIM free models) the key setup's Test key / Try it reported 'check failed' after 30 s while the server was still waiting (up to 50 s) |

## What happened

## Fix

## Left

## Seen

Excellence, 2026-10-08 ~10:50 CEST: a member's NVIDIA NIM key; Try it on `z-ai/glm-5.3-flash` showed "check failed". The server log has one `POST /api/byok/try-model` that took 30.3 s; NVIDIA's free models answer slowly.

## Cause

The browser's provider API client gives every request 30 s; the server waits up to 50 s for the service (`byok.go`).

## Fix

`byokTestKey` and `byokTryModel` wait 60 s.

## Also: a key's model could not be changed (422)

The member then found the cause: `Request failed with status code 422 model "nvidia/z-ai/glm-5.3-flash" is not offered for engine "pi-cli"`. The product chat checked the model against the platform catalog (`providerOptionOffersModel`), which does not hold models picked on a person's own key (or custom ids the Pi picker invites). Pi now accepts any well-formed `<service>/<model>` id; the account's own model list is still enforced when the turn runs. Test `TestPiAcceptsServiceModelsFromAPersonsKey`.
