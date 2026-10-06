[← browser / browser](index.md)

# PLAT-575: RTS extension screenshot cannot save its staging artifact across workspace service boundaries

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P2 |
| Product | browser |
| Area | browser |
| Summary | Extension screenshots now stage in their sandbox-recognized connection scope; Linux private-/tmp regression and real Chrome capture/transfer pass. |

## What happened

During the real RTS Code retry on 2026-10-06 at 07:39:38 UTC, screenshot failed
with ENOENT while saving below `/tmp/agentworks-browser-artifacts`. The requested
project output was `code/mentaldome/ab-test/browser-test.png`. This is separate
from the debugger/tab loss tracked by [PLAT-569](plat-569.md).

## Cause and fix

The extension relay named daemon sessions `ext-<hash>`, which the sandbox did
not recognize as managed browser sessions. Linux kept the shared socket folder
but hid the separate artifact staging root in its private `/tmp`. The persistent
daemon retained its original mount namespace after later requests created the
staging directory on the host. Creating that directory in either service alone
could not make it visible to the daemon.

Use a new per-connection `session-<hash>--browser` scope and stage extension
screenshots inside its already-granted socket folder's `artifacts/` directory.
The workspace validates the staging source against the current request's
backend-owned browser session, checks regular-file/image data and publishes
atomically into the currently authorized workspace output. No shared `/tmp`,
other connection's folder or extra project write grant is added. Existing
non-extension broker paths retain their compatibility behavior. Tokens, physical
Chrome tabs and project routing scopes do not change.

## Validation

- Real Linux 7.0.14 container, real Landlock launcher and private mount namespace:
  start a persistent file writer before any staging folder exists; reproduce
  the old path's ENOENT; create scoped staging later; save and finalize through
  the real HTTP handler. Another browser scope and an unauthorized output are
  refused. The writer stands in for agent-browser's file write, not Chrome pixels.
- Complete real Chrome 154.0.8037.98 managed-tool → guarded workspace shell →
  agent-browser → relay → unpacked extension E2E: capture a real PNG and fetch
  the published workspace image. Code/Crew/workflow isolation, reconnect,
  debugger recovery and cancellation checks also pass.
- Existing workspace artifact validation and transfer tests pass.

## Left

Deploy the agent/backend and workspace build together to RTS, then verify one
actual RTS screenshot. A fresh extension connection uses the new daemon scope;
server restart/reconnect normally creates it automatically. No extension package
update or token reset is required. This change does not enable extension video
recording, uploads or downloads.
