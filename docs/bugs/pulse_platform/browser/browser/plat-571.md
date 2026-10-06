[← browser / browser](index.md)

# PLAT-571: Copy the existing browser connection code without resetting or selecting a browser

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | browser |
| Area | browser |
| Summary | Browser settings exposes Copy connection code separately from account Reset; copying preserves live connections and the stable token. |

## What happened

The connected Browser settings panel exposed Reset but no ordinary copy action.
The owner needs to retrieve the same account connection code without revoking
Code/Crew/workflow connections or making the panel wait for another pairing.

## Fix

Add authenticated `copy` alongside `pair` and `reset`. It returns the existing
account token and server-derived project scope with the same product/owner/write
checks. Unlike pairing, it does not select the project or request an extension
connection. Reset remains the only action that rotates an existing token.
The settings footer offers Copy connection code and a manual-copy fallback;
copying leaves the current Connected state intact.

The real server transport regression verifies the same token, unchanged Code
connection, unselected Crew and absence of a connect-project frame. The browser
panel regression verifies copy, clipboard contents and retained Connected UI.
Focused server tests, panel tests and production frontend build passed.

## Left

Deploy the frontend/backend change to RTS; no credential migration is required.
