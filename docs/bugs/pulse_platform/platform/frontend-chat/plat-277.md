[← platform / frontend-chat](index.md)

# PLAT-277 — "Failed to fetch dynamically imported module" after every deploy: open tabs asked for the previous build's hashed chunks

| Field | Value |
|---|---|
| State | deployed |
| Priority | P2 |
| Product | platform |
| Area | frontend-chat |
| Summary | P2 — every deploy turned every open tab into a "Something went wrong" screen on its next navigation; read by the operator as a random crash. |

| Coordination | Value |
|---|---|
| Assigned agent | Claude Code |
| Ticket state | `fixed` — deployed to RTS 2026-09-03 and verified live; see Verification |
| Last synchronized | `2026-09-03` |

- **Priority:** P2 — every deploy turned every open tab into a "Something went wrong" screen on its next navigation; read by the operator as a random crash.
- **Owner:** frontend `utils/staleChunkReload.ts`, `main.tsx`, `components/ErrorBoundary.tsx`; `deploy/aws-ec2/deploy-rootless.sh`.
- **Origin:** live, RTS 2026-09-03 after three deploys in one afternoon; the failing chunk existed in the two previous `releases/*/frontend/assets` directories but not in `current`.

## Root cause

Each Vite build renames the hashed chunks under `/assets`; a tab opened before the swap still lazy-imports the old names, which the new release does not serve.

## Fix (agent_go `9d48c370c`)

- The app reloads itself once on that error (`vite:preloadError` and the error boundary), guarded per tab session so a genuinely broken build cannot loop; the boundary reads "A new version is available" instead of "Something went wrong" for this case.
- `deploy-rootless.sh` copies the previous release's `frontend/assets` into the new one (missing files only, mtimes preserved, pruned after 14 days) so old tabs keep working until they reload.

## Verification

Unit: `staleChunkReload.test.ts`. Live: release `9d48c370c-20260903135124` carried all 103 chunks of the previous release alongside its own 75. The app-side reload only protects tabs that loaded a build containing it, so the operator's tab from before this release needed one manual reload.

