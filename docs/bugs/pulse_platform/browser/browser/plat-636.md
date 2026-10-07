[← browser / browser](index.md)

# PLAT-636: Extension input lost on hidden tabs

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | P1 |
| Product | browser |
| Area | browser |
| Summary | Clicks and typing on a hidden shared tab were silently lost; the extension now shows the tab before input |

## What happened

Upwork runs on 2026-10-06 (Browser Bridge 0.4.6) failed to open job details and contract controls: every click
returned `success: true` but the URL stayed the same and nothing opened. Opening URLs and reading pages worked.

The extension diagnostics showed the cause. Each click's first `Input.dispatchMouseEvent` (the move) took about
5 seconds (5005–5026 ms), and the press and release then returned in 0–2 ms. In real Chrome a tab hidden behind
another tab does not render, so its input is not processed: Chrome times out the first event, reports success, and
the page never sees the click. PLAT-516 kept all extension actions in the background, and `Page.bringToFront` is a
no-op unless a call passes `active=true`. The agent cannot tell its tab is hidden, because every lost click reports
success.

The extension e2e did not catch this. Playwright starts Chrome with background throttling disabled
(`--disable-renderer-backgrounding`, `--disable-backgrounding-occluded-windows`), so its background tabs still take
clicks.

## Fix

Browser Bridge 0.4.7: before any `Input.*` command, if the shared tab is not the visible tab of its window, the
extension makes it the active tab of that window, waits 150 ms for a frame, and records a `tab_shown_for_input`
diagnostic. It does not focus the window. Reads, snapshots, navigation, recording and new tabs stay in the background
(PLAT-516). `extension.zip` is rebuilt.

Owner, 2026-10-07: not `active=true` on every command (the agent reads constantly, so it would keep switching
tabs), and not left to the agent (it cannot see the problem).

## Verification

The real-Chrome extension e2e (`RUN_CHROME_EXTENSION_E2E=1`, Chromium 1243) passes with the assertions updated: fill
and click show their hidden shared tab and record the diagnostic. The other background guarantees still pass:
snapshot, recording, agent-created tabs, per-call `active=true` not persisting.

That Chrome does not throttle background tabs, so the hidden-tab failure itself is verified only on the owner's Chrome
by reloading the extension and re-running the Upwork step.

## Rollout

Ships with the next deploy. Each user reloads Browser Bridge (0.4.7) in Chrome.
