[← sandbox / tools](index.md)

# PLAT-498 — The released desktop app v1.25.133 crashes at startup; the release script no longer runs

| Field | Value |
|---|---|
| State | fixed on main |
| Priority | - |
| Product | sandbox |
| Area | tools |
| Summary | open: the package lacks `lib/` (fixed on main, not released); the release script was stale after the repo rename (fixed). |

| Coordination | Value |
|---|---|
| State | OPEN: the fix is on main (PLAT-493 packages `desktop/lib/`), no fixed release is published yet; v1.25.133 is still the Latest release and is broken |
| Date | 2026-10-05 |
| Owner | desktop |

## Source

Found while checking the release for PLAT-493 (Vault in the DMG): the agent building it saw `desktop/lib/` missing from a packaged build.

## Verified

- Downloaded `AgentWorks-1.25.133-arm64-mac.zip` and read its `app.asar`: it holds only `main.js`, `preload.js`, `package.json`, `auth-prompt.html`, `settings.html` (plus
  node_modules). No `lib/`.
- That release's `main.js` does `require('./lib')` (line 16), and `desktop/package.json` `build.files` listed only `main.js`, `preload.js`, `settings.html`, `auth-prompt.html`.
  So the app fails at launch with "Cannot find module './lib'". v1.25.132 and earlier do not require `lib/` and are not affected.
- `scripts/desktop-release.sh` still named the old repository `manishiitg/coding-agent-loop` (renamed to `manishiitg/agentworks` on 2026-09-28) and refused to run:
  "origin points to git@github.com:manishiitg/agentworks.git, expected the canonical manishiitg/coding-agent-loop repository".

## Done

- `desktop/package.json` `files` now includes `lib/*.js` (PLAT-493, `ce3c7658c`).
- The release script uses `manishiitg/agentworks` and still accepts the old remote URLs.

## Left

- Publish a fixed release (needs the owner's go; it publishes every commit since v1.25.133 incl. Vault in the app). Then confirm the new `app.asar` has `lib/`.
- `desktop/main.js` and `package.json` still point at the old repository URL (works through GitHub's redirect).
- Consider a CI check that fails when `main.js` requires a local path that `build.files` does not package.

## Release attempt (2026-10-05)

- v1.25.134 was started: version commit `2f9f86156` and tag pushed, then the `Desktop DMG` release job FAILED at "Test Frontend Performance Contracts": one test,
  `chatScrollRequest.test.ts` "keeps the reading position in the chat already on screen when scrolled up". Cause: `89e1040fa` (10:57) keys the reading position by
  `tab:session` (`transcriptReadingKey`); the test still set the state under the bare tab id. `Frontend CI` on main had been red for the same reason. Test updated to the new key;
  the whole suite is green (488 files, 2821 tests).
- No release was published for v1.25.134 (the failed job published nothing).

## Register notes

[PLAT-498](plat-498.md), open: the package lacks `lib/` (fixed on main, not released); the release script was stale after the repo rename (fixed).
