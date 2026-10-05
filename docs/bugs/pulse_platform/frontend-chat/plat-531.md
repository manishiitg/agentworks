[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-531 — Switching from a workflow to Crew (or Code) shows "Opening workspace…" every time, slow on RTS

| Coordination | Value |
|---|---|
| State | first part fixed on main (the cache); not deployed; owner to measure on RTS after a deploy |
| Date | 2026-10-05 |
| Owner | frontend-chat |

## Source

Owner: on the RTS server, switching workflow to Crew via Ctrl+K "shows opening crew loading... its not like instant".

## Cause (read from code; not measured on RTS)

`useWorkSessions` (`WorkSurface.tsx`) started every mount with `sessions = []` and `loading = true`, and the surface remounts on each product switch, so every switch waited for the full list: `loadProductProjects`
(`platform/chat/productProjects.ts`) lists the folder, lists own shared Crews, then reads each Crew's `product.json` and then its `workflow.json` (two sequential reads per Crew, in parallel across Crews), and
`loadWorkSessions` may add initialization writes and a secrets call per uninitialised Crew; shared Crews are fetched too. The placeholder "Opening workspace…" shows until all of it returns. On RTS every round trip is slower
and there are more Crews, so it is visibly slow; locally it hides behind low latency.

## Done

- Applied the cache of owner PR #245 (2026-09-28, conflicted with main: `create` has since gained `runsOn`) to current main: the last list per product, workspace and user is kept at module level, shown at once on the next
  switch, and refreshed from the server in the background; a local edit made during that fetch wins over the older fetch result. The surface is keyed by user too. First visit in a page session is unchanged.
- Type-check clean; `src/products/work` tests 220/220. No new test (UI timing; checked by owner on RTS).

## Left

- First open after a page load is still the full chain. If it is still slow on RTS: one server call that returns the project list with manifests (replaces 1 + 2N round trips), or reading `workflow.json` in parallel with `product.json`.
- PR #245 is superseded by this and can be closed (owner's call).
- Measure on RTS after the next deploy (owner's go needed per server): time from Ctrl+K to the Crew view, first and second switch.
