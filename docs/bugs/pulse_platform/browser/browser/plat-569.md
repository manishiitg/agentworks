[← browser / browser](index.md)

# PLAT-569: RTS extension loses shared tabs while the Chrome tabs remain open

| Field | Value |
|---|---|
| State | open |
| Priority | P1 |
| Product | browser |
| Area | browser |
| Summary | The connected extension drops its shared-tab/session mapping during agent use; the physical Chrome tab remains open. Cause and fix are not yet established. |

## What happened

Reported on RTS on 2026-10-06 for Code project `sde-private-3b40f2c4`.
The user only connects the extension and asks the agent to use it. They confirm
that neither page DevTools nor the Cancel debugging banner is involved.
A dev Course Designer tab briefly appears in the managed tab list, then later
calls return an empty list, `tab_gone`, or `CHROME_EXTENSION_NO_TABS`, although
the physical tab is still open. Recovery attempts create blank tabs.

## Verified server evidence

Read the actual RTS `logs/agent.log` over SSH, without editing or restarting
production. The running release was `9ec5d57-20261006062319`.
The persisted extension selection still contains this Code project.

| UTC on 2026-10-06 | Tool and result |
|---|---|
| 05:39:02 | `open` of `/voice-study` times out after about 29 seconds. |
| 05:39:04 | `snapshot` returns `CHROME_EXTENSION_NO_TABS`. |
| 05:39:12 | `tab new --label voice-study` fails at `Page.enable`: Detached while handling command. |
| 05:54:36 | `tab new --label cd-home` fails at `Page.enable`: Session is not shared with this workspace. |
| 06:37:36 (12:07:36 IST) | `open` returns `tab_gone` for a target whose last URL is the dev site's home page. |
| 06:38:14 | `open` of `/intro` times out after about 27 seconds. |
| 06:38:24 (12:08:24 IST) | `get url` returns `tab_gone` for an `about:blank` target. |
| 06:38:35 (12:08:35 IST) | Selecting `t4` returns `CHROME_EXTENSION_NO_TABS`. |

Both running agent and workspace processes resolve their PATH to the service
account's `agent-browser 0.38.2`. The system fallback is 0.37.0; that fallback
alone is not evidence of the tool version used by these services.
The extension popup was inspected read-only and showed Connected with **0 shared
tabs**. Chrome still contained the dev-site tabs. This distinguishes a live
platform connection from usable shared-tab authority.

## Investigation and validation limits

The extension's `chrome.debugger.onDetach` handler currently discards Chrome's
reason and calls `unshare`, which deletes all sessions for the tab and advertises
`Target.targetDestroyed`. Other removal paths are explicit unshare/close,
`tabs.onRemoved`, and a URL rejected by the `tabs.onUpdated` handler. Existing
RTS logs do not distinguish these paths. None is yet proved to be the cause.

The real managed tool → guarded workspace shell → agent-browser → relay →
unpacked extension E2E passed with Chrome for Testing 153.0.8010.12 and
agent-browser 0.38.2. Temporary probes also passed cross-origin iframe and
COOP/COEP navigation, and opening the reported dev site's `/intro` route.
No debugger-detach event was observed in those runs. The user's installed
Chrome is 154.0.8037.98; the fresh fixture profile does not reproduce the user's
existing browser state or the Linux server runtime.

Temporary command logging and navigation probes were removed after investigation.
No production fix, deployment, restart, or changes to the user's tab grants were
made. This ticket remains open; the passing isolated test does not resolve the
reported RTS failure.

## Left

Capture the reason and initiating path for the tab removal, with bounded metadata
rather than raw CDP commands, page contents or pairing credentials. Reproduce
against the actual extension/runtime combination and pin the cause with a real
browser regression. Preserve revocation on human cancellation and do not restore
unrelated tabs or blindly retry page actions. Then verify the fix on RTS and
update this ticket's state with the evidence.
