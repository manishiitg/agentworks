[← browser / browser](index.md)

# PLAT-599: Stalled extension renderer probes block CDP controls and sandbox launches stale CLI

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | browser |
| Area | browser |
| Summary | Dispatch CDP requests concurrently and select/qualify the deployment-managed CLI inside the RTS sandbox. |

## What happened

RTS Code project SDE Private reported an activation timeout, then HTTP 409 on
both listing and creating tabs. In release `26fbe2a-20261006101048`, its relay
logged a `Runtime.evaluate` start at 10:16:50 UTC and completion at 10:18:45;
activation only ran after that pending call completed. This did not establish
that the page itself was permanently hung: the extension serialized every CDP
request behind one renderer-bound promise.

Read-only `/proc` inspection confirmed the SDE daemon ran the system-installed
agent-browser **0.37.0**, while the service's managed install and shared-browser
daemon used **0.38.2**. Docker-mode sanitization omitted the managed folder from
PATH. Deployment qualified only the service's PATH. The older native runtime
retained a connection after failed initialization, causing later HTTP 409s.
The subsequent user retry with the latest extension worked, but did not remove
this server configuration discrepancy. The original renderer delay's trigger
was not established; the bridge must remain responsive while it occurs.

The local Upwork `daily-bid` run then failed during job discovery on 2026-10-06.
At 16:19:10 IST, `Page.navigate` started and Chrome attached a child target;
navigation never completed, and later snapshots and tab lists timed out. Edge
still had **0.4.3 loaded**, even though the unpacked folder contained 0.4.5.
The local native CLI was already 0.38.2, so this was separate from the RTS PATH
mismatch. A paused child needs `Runtime.runIfWaitingForDebugger`; putting that
request behind pending navigation creates a circular wait. The child event
and blocked queue support this explanation for the old-build failure, without
establishing the exact state of the original child frame after the fact.

Reloaded the existing Edge install to 0.4.5 and confirmed its version in the
extensions UI and relay diagnostics. The live Upwork retry then completed
navigation in about 2.4 seconds and the following page read in under a second.
The relay recorded child resume requests completing alongside navigation.

## Fix

- Extension 0.4.5 dispatches CDP requests concurrently. The trusted server tool
  gate still serializes project actions and enforces controlling-chat ownership.
  Actions and grants are not retried, reassigned or reset. Pending replies
  from a disconnected logical client cannot enter a new client with reused IDs.
- `AGENT_BROWSER_CLI_DIR` selects the managed CLI in sanitized PATH. Landlock
  grants only that tool directory and its resolved installed package read-only,
  keeping private service files and credentials inaccessible.
- RTS explicitly configures that directory and qualifies the actual sandboxed
  `--version` before activation, requiring the host and sandbox versions to agree
  at 0.38.2 or newer. This runtime closes failed initialization connections.
- Safe diagnostics retain each request's method, extension version and duration
  so overlapping replies can be correlated without logging page data or tokens.

## Verification

- Real Chrome 154 extension E2E: held renderer probe still permits activation,
  background tab creation and close; failed native initialization releases CDP;
  next tab list/new works using the same pairing and physical grant. Late
  old-client replies are discarded when a new client reuses request IDs.
- The same E2E covers Code/Crew/workflow isolation, ownership, screenshots,
  successful/interrupted recording, debugger recovery and reconnects.
- A workflow regression navigates to a real cross-site iframe under Chrome
  site isolation, verifies Chrome attached it paused and its script resumed,
  then takes a snapshot and lists tabs from a different workflow step.
  This passed in Chrome 154. Restoring serialized dispatch locally reproduced
  the existing held-renderer control failure, but the simple cross-site fixture
  still passed; it does not reproduce the original Upwork navigation stall.
- Deployment preflight exercised with real agent-browser 0.38.2 in a
  Linux Landlock sandbox; the restricted version matches the managed install.
- Linux Landlock regression: managed package selected with restrictive PATH,
  service-home file and workspace token remain inaccessible.

## Left

Deploy the server change to RTS and reload its extension to 0.4.5. The local
Edge install has been reloaded. The production renderer delay itself is not
reproduced from page contents; no site crash or user DevTools interaction was
assumed. No RTS services were restarted or production projects changed while
investigating this ticket. The full Upwork bidding run was not started for
diagnosis.
