[← browser / browser](index.md)

# PLAT-583: Make transient Chrome setup error detection independent of exact message wording

| Field | Value |
|---|---|
| State | open |
| Priority | P3 |
| Product | browser |
| Area | browser |
| Summary | Setup retries depend on a Chrome error string; changed wording fails closed rather than retrying. |

## What happened

Review noted sendSetupCommand matches Chrome's exact
"Cannot access a chrome-extension:// URL of different extension" message. Chrome
versions can change its wording. The retry schedule can hold the command queue
for about ten seconds. Other errors currently fail immediately.

## Decision for this change

Keep the narrow classifier and serialized queue. Only subscription commands are
retried, with ownership/connection/main-URL checks on every attempt. Broadening
to all permission errors could hide a real denial; allowing page actions to pass
setup could break their ordering. No stable structured error code is exposed by
chrome.debugger.sendCommand in this path. This is not fixed by guessing more text.

## Left

Qualify a stable discriminator or version-tested structured adapter signal for
the transient frame-permission case. Preserve immediate failure for real
permission denials and never replay page mutations. Evaluate shorter setup
latency with the actual RTS repro before changing the bounded ten-second window.
