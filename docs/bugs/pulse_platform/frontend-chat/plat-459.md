[← Pulse platform issue index](../../pulse_platform_issue_register.md)

# PLAT-459 — Work chat selection test fails during module initialization

| Coordination | Value |
|---|---|
| State | open |
| Date | 2026-10-04 |
| Owner | frontend-chat |

## Evidence

Running `npm test -- src/products/work/workChatTabSelection.test.ts` after
`npm ci --ignore-scripts` fails before any tests execute:

```
ReferenceError: Cannot access '__vite_ssr_import_6__' before initialization
getApiBaseUrl — src/services/api.ts:346
src/services/llm-config-api.ts:231
src/stores/useLLMStore.ts:8
```

Reproduced at main `83c5d4c6d` in an owned worktree with the unmodified
WorkSurface.tsx. The same failure occurs with PLAT-458's label change. The
four adjacent tab suites pass (36 tests), as does `npx tsc -b`.

## Left

Investigate the API/workspace/store initialization dependency and make this
suite runnable. No fix is included with PLAT-458.
