[← browser / browser](index.md)

# PLAT-585: Extension browser sessions consume headless browser capacity after screenshot scope change

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | browser |
| Area | browser |
| Summary | The trusted extension transport bypasses headless capacity independently of its sandbox session name. |

## What happened

Commit 4cbc8a1ee (PLAT-575) changed relay session names from ext- to the
sandbox-recognized session-…--browser form. The workspace tracker exempted
only the old prefix. Relay commands consumed global slots, evicted headless
sessions and could fail the per-chat limit when the chat already had a browser.

## Fix

The backend stamps BrowserTransport=extension in its trusted folder guard;
the guarded shell uses it only for agent-browser commands with a CDP endpoint.
Headless commands retain per-chat/global capacity checks. Legacy ext- relay
clients remain compatible; arbitrary session- names are never exempted.
No public tool argument can set the transport.

A focused regression fills per-chat/global capacity and checks relay navigation,
tab/snapshot/record commands do not add or evict slots. The real Chrome E2E also
runs with headless capacity saturated and verifies the existing slot survives.

## Left

Deploy the backend/workspace change to RTS. No extension reinstall is required for this fix.
