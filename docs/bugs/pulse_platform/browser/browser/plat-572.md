[← browser / browser](index.md)

# PLAT-572: RTS extension screenshot cannot save its staging artifact across workspace service boundaries

| Field | Value |
|---|---|
| State | open |
| Priority | P2 |
| Product | browser |
| Area | browser |
| Summary | A connected RTS extension captures the page but agent-browser cannot write the broker's temporary screenshot path. |

## What happened

During the real RTS Code retry on 2026-10-06 at 07:39:38 UTC, screenshot failed
with ENOENT while saving below `/tmp/agentworks-browser-artifacts`. The requested
project output was `code/mentaldome/ab-test/browser-test.png`. This is separate
from the debugger/tab loss tracked by [PLAT-569](plat-569.md).

## Investigation

`newBrowserArtifactStagingPath` creates the temporary parent directory in the
agent process, while the command runs in the workspace service/shell sandbox.
The local macOS full browser E2E successfully captures and transfers screenshots;
that does not establish shared temporary-path visibility on Linux RTS.

## Left

Verify the workspace sandbox's actual temporary-path mapping and create staging
through the execution service or an existing shared artifact boundary. Validate
capture and transfer through the real Linux sandbox. Preserve project output
write grants and do not broaden arbitrary /tmp access. No fix is claimed here.
