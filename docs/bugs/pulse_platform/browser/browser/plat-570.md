[← browser / browser](index.md)

# PLAT-570: Browser picker shows the connected account browser across products

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | browser |
| Area | browser |
| Summary | Keep the explicit browser choice while displaying account-wide Chrome/Edge availability and reusing it without another token paste. |

## What happened

Code showed a live extension connection while Crew presented a fresh install
and copy-code flow. The account token and browser were already usable, but the
picker only queried the current project's connection. The owner wants every
supported product to retain its browser choice and show what is connected.

## Fix

Status exposes `account_connected` separately from `selected` and `connected`,
scoped strictly to the authenticated account. The shared Code/Crew/workflow
picker and settings show Chrome/Edge availability with a green indicator.
Explicit selection calls an authenticated `connect` action that registers the
project and requests its own connection over the live account socket. It returns
status only. Opening the picker does not select Chrome or share existing tabs.

The regression drives the real React panel/hook and checks that the picker
remains, selection reuses the connection without clipboard access, and no
installation flow appears. Real server/WebSocket tests verify the connect-project
request, credential-free response and cross-account denial. Frontend build passes.

## Left

RTS deployment and visual verification on the running server. This UX change
does not claim to fix the separate tab-loss investigation in PLAT-569.
