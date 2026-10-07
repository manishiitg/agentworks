[← relays / triggers](index.md)

# PLAT-638: Allow Python Relay platform runs without workflow plan artifacts

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | relays |
| Area | triggers |
| Summary | Shared trigger target validation loaded plan.json for Python Relays, blocking Builder setup; real lifecycle checks also exposed a manifest lock copied into release snapshots. |

## What happened

Excellence, Workflow/testing, 2026-10-07: the Builder's test_relay failed because
there was no function trigger. Creating the required INPUT function then failed:
`Webhook operation failed (400): load API trigger routes: failed to extract plan
content from API response`. Python source ran directly in the shell, but that did
not verify a platform invocation. This was a gap in PLAT-611's shared integration.

The shared trigger admission/listing helpers still read planning/plan.json, which
Python Relays do not have. Extending the real workspace test to create the trigger
also found publication trying to copy workflow.json.kb-lock, the empty internal
manifest synchronization file created by normal manifest saves. The workspace
file API rejects empty content, so publication stopped on that file.

## Fix

- Python Relay step/route inventory is empty without a plan read. Empty target
  admission succeeds; authored workflow step/route selections are rejected.
- Manifest validation rejects Python step targets and payload routing, including
  disabled triggers and raw manifest saves. Branching belongs in relay.py.
- Exclude the exact internal workflow.json.kb-lock from release snapshots.
- Keep shared identity/access, variable validation, function schema, scheduler
  dispatch and legacy graph/workflow route validation.
- Builder prompt/skill explicitly distinguish direct shell execution from a
  platform test and configure an object INPUT function before test_relay.

## Verification

- Exact error confirmed in Excellence server logs; production data unchanged.
- Real workspace lifecycle test now creates/lists an enabled object INPUT function
  through the real handlers with no plan.json, rejects disabled workflow routing,
  publishes immutable versions and executes the published Python starter.
- Shared workflow webhook management and function checks are included in the
  regression run; both server and product packages passed.

## Left

Deploy to Excellence, then retry the user's trigger setup and platform test.
No direct edits to the server's Relay configuration.
