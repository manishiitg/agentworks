[← browser / browser](index.md)

# PLAT-569: RTS extension loses shared tabs while the Chrome tabs remain open

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | browser |
| Area | browser |
| Summary | The connected extension drops its shared-tab/session mapping during agent use; the physical Chrome tab remains open. Chrome target closure discarded still-live tab authority. Extension 0.4.3 restores the same tab/session after Chrome target closure and waits for transient frame permissions; the uninterrupted RTS regression passed. |

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

Before instrumentation, the extension's `chrome.debugger.onDetach` handler discarded Chrome's
reason and called `unshare`, which deletes all sessions for the tab and advertises
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

The initial temporary probes were removed. Extension 0.4.1 now adds bounded
metadata logging for debugger detach reasons, initiating unshare paths,
commands and child sessions. The relay negotiates and validates the optional
diagnostic channel, retaining compatibility with older servers. Credentials,
URLs, page contents and CDP parameters are excluded.

The full tool/sandbox/relay E2E passes with Chrome for Testing **154.0.8037.98**
and agent-browser 0.38.2. A second standalone test starts that Chrome without
Playwright or a remote-debugging port and retains one shared tab through four
snapshot/URL checkpoints and idle intervals on the actual dev `/intro` route.
Neither reproduces the loss. Relay transport and logging validation tests pass.

The user's existing unpacked extension folder was updated to 0.4.1, preserving
0.4.0 originals outside the folder. A temporary extension diagnostics page
verified the loaded version, a connected Code socket and zero tabs. Its ring
contained only connection_paired: no agent browser command had run since reload.
That page was closed and removed. Crew setup screenshots are evidence for
PLAT-570, not a reproduced debugger detach.

## Confirmed lifecycle boundary and recovery

The instrumented RTS retry on 2026-10-06 establishes the removal path:

- 07:26:10 UTC: an agent-created tab receives Chrome `target_closed` after
  Network.enable. The extension then logs `tab_unshared/debugger_detached`;
  following domain commands fail with `not_shared`.
- 07:31:06 UTC: the next tab receives the same detach while the socket remains
  paired. This is not an authentication/token failure.
- Read-only queries of the installed Chrome 154.0.8037.98 confirm both tab IDs
  still exist, complete, neither frozen nor discarded; one is grouped and one
  was ungrouped by the old removal handler.
- After the owner reconnects, 07:39:44–45 UTC: Page.navigate succeeds on the
  same tab, followed by child detach, `target_closed` and unshare. There is no
  initiating close-target or session-detach command for that tab.
- 07:41:27 UTC: a newly created tab suffers the same boundary after attach.

This confirms that debugger target closure is not physical-tab closure. The
underlying reason Chrome closes its target in this existing profile remains
unestablished; group creation and navigation are possible triggers, not proved
causes. Extension 0.4.2 additionally logs tab creation and group start/success/
failure to distinguish them without logging URLs or page contents.

For `target_closed` only, retain the explicitly granted tab ID, logical root
session and project group. Query that exact surviving HTTP(S)/about:blank tab
and reattach its debugger with bounded attempts. Reapply successful domain-enable
and auto-attach settings; discard obsolete child sessions. Following commands
wait for recovery. Never replay page actions. A closed/protected tab, explicit
cancellation, stopped/replaced connection or changed owner revokes access.
No new blank tab or grant inferred from URL, title or group is used.

The real tool → guarded workspace → agent-browser 0.38.2 → relay → Chrome
154.0.8037.98 E2E passed: detach the real debugger transport, inject the observed
Chrome lifecycle reason in a fixture-only worker, then read the same tab through
the same agent session. The snapshot and physical tab/group survive, focus stays
on the private user tab, and cancellation during recovery never restores access.
The existing full suite also passes agent-created tabs joining an existing group,
simultaneous Code/Crew isolated groups and workflow-step reuse. The fixture hook
is excluded from the shipped extension. This exercises the failure boundary;
it does not reproduce the original Chrome trigger in the owner's profile.

No production server restart/deployment was performed for this recovery change.

## RTS verification of 0.4.3

After 0.4.2 restored new-tab creation, the actual RTS retry exposed a second
startup race: Page.enable briefly returned `Cannot access a chrome-extension://
URL of different extension`. The physical tab's native target was HTTPS, and
later read-only native Page.getFrameTree/Runtime.evaluate succeeded on it.
The observed denial was transient; no particular installed extension is proved
to be the cause. Chromium checks frame URLs as well as the main page URL.

Domain subscriptions now wait with bounded backoff (about ten seconds) only for
that exact Chrome permission error, retaining the same grant/connection and
rechecking the safe tab URL and native permissions on each attempt. No navigation,
evaluation, input action or arbitrary error is retried. Setup/attach failure phases
are separately logged with fixed metadata; error payloads are excluded.

Loaded 0.4.3 in the owner's existing installed extension folder, with no token
reset or production server deployment. The first navigation retry passed but
its final list was invalidated by our extension reload for the group-name change
while the test was still running; that run is not claimed as an uninterrupted pass.

The subsequent browser-only SDE Private retry was uninterrupted: create a dev-home
tab, navigate that same tab to `/voice-study`, read title and compact interactive
snapshot, create a second dev-home tab, list both, select and read each. All passed.
Chrome showed the two physical tabs in the same project group. The pages showed
the site's login screen; no login, file/settings writes or deployment was attempted.
The full local managed-tool Chrome E2E and focused backend checks also pass.

## Left

Deploy the updated relay to RTS to capture new group/setup/recovery metadata in
server logs; extension-side recovery is already loaded and verified. The original
reason Chrome closes the target in this profile remains unestablished, but the
reported new-tab/navigation loss is fixed at the lifecycle boundary. Observe
longer usage for a recurrence. Screenshot staging is a separate open issue in
[PLAT-575](plat-575.md). Friendly group names require the server change in
[PLAT-573](plat-573.md); the loaded extension already removes the brand prefix.
