[← relays / frontend-chat](index.md)

# PLAT-680: Centre Relay branch labels on connector lines

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | relays |
| Area | frontend-chat |
| Summary | Centre Relay branch labels on their routed connector lines so the path is clear. |

## What happened

The Dominion Relay graph displayed labels such as "Next ticket", "Not high, next
ticket" and "priority = high" beside their connector lines. Dagre's default
`labelpos: r` shifts each label's x coordinate by half its width plus the label
offset. This made the label look disconnected from its branch, especially on
loop and skip routes.

## Fix

`RelaySourceGraph` sets `labelpos: c` when registering its edges with Dagre.
`SourceEdge` continues to use the routed points and returned label coordinates;
React Flow's existing opaque label background keeps the text readable over the
line. No additional graph renderer or routing implementation is introduced.

Verification runs on GitHub through `scripts/verify-remote.sh`, including the
frontend type check, existing Relay graph/view tests and the ticket check. No
frontend build or test is run on the owner's laptop.

## Left

Deploy the frontend change to Dominion and inspect the labelled loop/branch
connectors in the live graph. The Relay Python program and its execution are
independent of this presentation change.
