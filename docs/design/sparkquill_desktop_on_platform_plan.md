# SparkQuill desktop on the platform — plan and research record

**Status (2026-09-06): direction decided — the SparkQuill desktop becomes an AgentWorks client.**
`desktop-sparkquill` will spawn the same two binaries the AgentWorks desktop spawns (`workspace-server` +
the `agent_go` server) with SparkQuill as a product (`internal/sparkquillproduct/product.yaml`, on by default),
and `cmd/family-server` + the bubble chat in `LearningApp.tsx` are retired. Stated goal: *"very less code
duplication between SparkQuill and AgentWorks — share the backend and frontend code as much as possible."*
This reverses the 2026-09-03 decision to keep a separate family-server. **Part A** below is the research (A1–A9)
and the phased implementation plan (A10: P0 server enablers → P1 shell sharing → P2 delete the bubble chat and move
the learning app under `frontend/src/products/sparkquill` → P3 blocking parity → P4 gaps → P5 migration → P6
deletions → P7 live verification; ~25–30 dev-days to the first shippable milestone, ~8–11 trailing).

**What follows is the research record from 2026-09-06.** Sections 1–6 (facts, reuse seams, wire contracts,
mismatches) remain accurate and are the reference for how `PlatformChat` talks to the server. The "Plan",
"Verification" and "Risks" sections describe the *rejected* alternative — teaching `family-server` the
AgentWorks session protocol (~12–15 days) — and are kept for the record, not for execution.

---

# Part A — Desktop = AgentWorks client: findings for the new direction (2026-09-06)

## A1. Desktop shell — what to share

`desktop/main.js` (AgentWorks) is the template; `desktop-sparkquill/main.js` re-implements ~250–300 lines of it
(login-shell PATH import `desktop/main.js:54-92` vs `desktop-sparkquill/main.js:44-68`; bounded 25 MB log writer
`:165-202` vs `:74-87`; `detect(port)`; health poll `:1358-1389` vs `:138-158`; external-URL interception
`:1797-1838` vs `:189-197`; render-process-gone reload; cache-bust query; GitHub release listing + `nohup install*.sh`
`:1401-1525,1675-1706` vs `updater.js:30-97`; tray; `DEV_URL` bypass; SIGTERM child kill). The two installers
(`install.sh`, `install-sparkquill.sh`) differ only in app name, tag prefix, dmg name and env prefix.

Genuinely SparkQuill-specific in the shell: light theme + cream `#fbf7ef`, dock icon when unpackaged, tray labels,
menu (`Open SparkQuill Folder` → `~/.sunlit-learning`), **close hides to tray** (AgentWorks quits), voice
warm/unload on window show/hide (`main.js:127-136,212-213`), gentler notification-only updater, `sparkquill-v*`
tags with `gh release create` (`.github/workflows/sparkquill-desktop.yml:112-119` explains why not electron-builder's
publisher).

**Recommendation:** extract `desktop/lib/{loginEnv,boundedLog,spawnServer,health,updater,externalNav}.js` and a
`desktop/products/<id>.js` descriptor (`appId, productName, userDataDir, preferredAgentPort, preferredWorkspacePort,
windowTitle, backgroundColor, theme, trayLabels, iconPath, tagPrefix, dmgName, installScriptUrl, closeBehavior,
extraServerEnv`), selected by `AGENTWORKS_PRODUCT` / a baked `product.json`. One app directory emits both apps via
`electron-builder --config build/sparkquill.yml` (two config files: `appId`, `productName`, icon, `extraResources`);
keep the two release workflows (independent cadence is intentional) but unify on the SparkQuill publish approach;
one parameterized installer. Note `desktop/dev-setup.sh:32-38` does not produce `resources/lib` (only CI does via
`scripts/build-darwin-voice-binary.sh`), so a clean local `npm run build` can fail today.

## A2. Spawn recipe (from `desktop/main.js`)

- `spawnWorkspace()` `:1108-1182`: `workspace-server server --port <detect(45679)> --docs-dir <docsDir>`, env
  `DOCS_DIR`, `DATA_DIR=<userData>/data`, `NATIVE_WORKSPACE=true`, `WORKSPACE_API_TOKEN` (random 32 B hex, `:23`);
  ready on stdout `DynamicPort: N` (`workspace/server.go:202`).
- `spawnAgent()` `:1184-1356`: cwd = Resources dir (packaged) / `agent_go` (dev) — **this is what makes `./static/`
  resolve**; copies+rewrites `configs/mcp_servers_clean.json` → `<userData>/configs/mcp_servers.json`; args
  `server --port <detect(45678)> --log-file … --log-level debug --mcp-config …`; env `WORKSPACE_API_URL`,
  `WORKSPACE_DOCS_PATH`, `DOCS_DIR`, `LOG_FILE`, `NATIVE_WORKSPACE=true`, `WORKSPACE_API_TOKEN`, inherited
  `AUTH_SECRET`; ready on `DynamicPort:` (`server.go:2582`).
- Health `…/api/health` + `…/health`, 90 s; window loads `http://127.0.0.1:<agentPort>/?runloop_version=<v>` — the
  **server-served frontend** (`server.go:2533` `spaStaticFileHandler("./static/")`, cwd-relative, no env override).
- Packaging `desktop/package.json:28-53` `extraResources`: `agent-server`, `workspace-server`, `lib`, `configs`,
  `../agent_go/static → static`.

## A3. The two gates — and the trap

1. **JWT gate (always on)**: `AuthMiddleware` on all `/api/*` (`server.go:1947`). Desktop satisfies it silently:
   `MULTI_USER_MODE` unset → `POST /api/auth/login` mints a default-user JWT signed with the shell-generated
   `AUTH_SECRET` (`user_auth_routes.go:222-238`). First launch asks nothing unless `config/provider-api-keys.json`
   is encrypted with an unknown secret (`auth-prompt.html#unlock`, `main.js:464-516`).
2. **Claude-Code token gate**: `handleQuery` 400s a `claude-code` profile with no stored `ClaudeCodeOAuthToken`
   **only when `isSingleProductServerDeployment()`** (exactly one entry in `AGENT_PRODUCTS`, `server.go:139-172,
   3583-3594`) or `runtime.require_provider_token`. **`AGENT_PRODUCTS=sparkquill` would 400 every SparkQuill turn**
   (its default provider is `claude-code`, `product.yaml:100-105,216-222`). The desktop must leave `AGENT_PRODUCTS`
   unset (registering the other products costs a few ms and no runtime surface) or add an explicit desktop
   exemption. Also: `registeredProductIDs()` (`user_directory.go:826-835`) omits `sparkquill` — fix.
   No other setup-token/preflight gate exists.

## A4. Two AgentWorks servers on one Mac — collisions and the fixes

Working recipe already exists: `scripts/run-local-instance.sh:277-297`.

| Resource | Collides? | Fix in the SparkQuill shell |
|---|---|---|
| Electron `userData` (`appData/runloop-desktop`, `RUNLOOP_USER_DATA_DIR`) | yes if shell shared | per-product `userData` |
| Ports `detect(45678/45679)` | soft (origin flips → localStorage/JWT reset) | own preferred pair (e.g. 45778/45779) |
| workspace-docs root (`_system/costs.sqlite`, `config/provider-api-keys.json`, `config/whatsapp-sessions`, `.agentworks/product-dependencies.json`) | **yes** | separate `RUNLOOP_DOCS_DIR`/`WORKSPACE_DOCS_PATH` |
| CLI-security store `UserConfigDir()/AgentWorks/cli-security` (hardcoded, `log.Fatalf`, `pkg/clisecurity/store.go:40-45`) | **yes, global** | add an env override, or accept sharing |
| Voice models `~/.agentworks/voice-models/…` (`.partial` + rename, no lock, `pkg/voicestt/model.go:253,282`) | first-run download race | lock or single warm; steady-state sharing is desirable |
| tmux: `cmd/server` reaper/close match `mlp-*` only (`coding_agent_tmux_reaper.go:209-214`, `session_lifecycle.go:178-185`); startup sweep is ownership-tagged | no cross-kill | **do not set `*_SESSION_PREFIX=sq-*`** (sessions would leak); `TMUX_TMPDIR` for hard isolation |
| Browser startup `--kill-all`/`pkill chromium` (`server.go:1874-1919`) | **destructive** | `AGENTWORKS_SKIP_GLOBAL_BROWSER_CLEANUP=true`, `AGENTWORKS_BROWSER_SESSION_PREFIX=sparkquill`, `AGENT_BROWSER_CONFIG` |
| Coding-CLI login (`~/.claude`) | shared by design | leave |

## A5. What the server always starts (no "lite" mode)

Unconditional or independently gated in `runServer` (`server.go:1450`): tmux orphan sweep (`MCP_DISABLE_TMUX_STARTUP_SWEEP`),
EventStore, terminal store, **chat store (needs workspace-server, `log.Fatalf`)**, **cost-ledger SQLite**,
NotificationManager + Org Dashboard connector, Slack/Gmail init (self-disable), **CLI security store (`log.Fatalf`)**,
platform tools, WhatsApp (`WHATSAPP_ENABLED=false` to skip), inactive-session cleanup, **Scheduler +
ProductScheduleService (`SCHEDULER_ENABLED=false` would also kill SparkQuill's `product:sparkquill:pulse` check-in)**,
browser kill-all (skip flag), pprof, tool-cache warm, ~50 route groups.

## A6. Frontend serving — how the shell reaches the SparkQuill surface

- `spaStaticFileHandler("./static/")` is the single mount (`server.go:2533`), cwd-relative.
- Dynamic `/runtime-config.js` (`server.go:2513-2521`) emits only `apiBaseUrl`/`workspaceApiBaseUrl`, but the
  frontend already reads `appName`/`faviconUrl` (`frontend/src/runtime-branding.ts:29-46`) and
  `defaultProductSurface`/`enabledProductSurfaces` (`frontend/src/products/productSurfaceConfig.ts:10-38`) — today
  set only by static files (`deploy/aws-ec2/server/runtime-config.js`, `agent_go/run_server_with_logging.sh:1011-1021`).
- `sparkquill` is **not** a surface: `PRODUCT_SURFACES = ['agentworks','video-studio','finance','dominion']`
  (`productSurfaceConfig.ts:1`, store union `useProductSurfaceStore.ts:4,27-34`, switch `App.tsx:910-914`); selection
  is store-persisted, not URL-routed.
- **Recommended:** emit `defaultProductSurface/enabledProductSurfaces/appName/faviconUrl` from `/runtime-config.js`
  (sources: `AGENT_PRODUCTS`, `AGENTWORKS_APP_NAME`/`AGENTWORKS_FAVICON_URL`, or the manifest —
  `sparkquillproduct/product.yaml:250-259` already declares `branding.icon/favicon` and `ui.surface: sparkquill`);
  add `sparkquill` to `PRODUCT_SURFACES` + store + `App.tsx`; shell loads plain `/`. One Vite build, one `static/`.
  Add a `STATIC_DIR` env for `server.go:2533` to drop the cwd coupling (and the log-symlink hack `main.js:891-924`).
  Alternative with zero server change: SparkQuill's `Resources/static` = learning-app dist (keeps two builds).

## A7. Frontend consolidation — verdict: `learning-app` becomes `frontend/src/products/sparkquill/`

**How surfaces work today (no router):** `App.tsx:908-916` is a ternary on the persisted zustand value
`useProductSurfaceStore` (`agentworks-product-surface`, version 3, default `agentworks`); lazy imports `App.tsx:45-47`;
when a product surface is active the whole AgentWorks tree is simply not rendered. Allowlist/default come from
`window.__APP_RUNTIME_CONFIG__.{enabledProductSurfaces,defaultProductSurface}` (`productSurfaceConfig.ts:10-39`),
enforced at `App.tsx:97-110`; branding from `appName`/`faviconUrl` (`runtime-branding.ts`, applied once at boot).
`product.yaml`'s `branding:`/`ui:` are **not on the wire** — only validated in Go (`sparkquillproduct/product_config.go:49`).
Video Studio-as-default exists only via the static `deploy/aws-ec2/server/runtime-config.js`; the desktop has no
product handling at all.

**`learning-app` today:** 9,095 lines / 37 files (`LearningApp.tsx` 4,629; `learning-app.css` 1,298). It is not
really a separate app: it imports its chat from `../../src`, resolves deps from `../node_modules`, is tested by the
parent's vitest and linted by the parent's eslint, and **is type-checked by nothing** (`"build": "vite build"`, no
`tsc`). Costs of the split: four `vite.config.ts` hacks (`fs.allow`, `@` alias, **file-level** `lucide-react` alias,
`dedupe` for zustand/react/react-query), two `node_modules` trees with real drift (lucide **0.525 vs 1.25**, vite 7 vs 8,
typescript 5.8 vs 7.0, zustand 5.0.9 vs 5.0.14, `"*"` ranges in `learning-app/package.json:20-30`), and a doubled
`npm ci` in CI (`sparkquill-desktop.yml:69-84`, with a comment recording the failure that forced it).

**Duplicate pairs to collapse** (learning-app ↔ `frontend/src`): voice `MicButton`/`useMicDictation` vs
`src/voice/MicButton.tsx` (a diverging port, both on `shared/voice`); `shared/chat/SyntaxHighlightedCode` vs
`ui/SyntaxHighlightedCode` (byte-level); `shared/chat/ChatRenderer` vs `ui/MarkdownRenderer` (two markdown stacks);
file viewer helpers vs `FileContentViewer` + `ui/*Renderer` (+ dead `shared/files/ProjectFileBrowser`); `notifySound`
vs `utils/sound`; `BuildUpdateNotice` vs `staleChunkReload`/`UpdateProgressToast`; secrets UI vs
`components/secrets/*`; `stores/useWorkspaceStore` **name collision** with `src/stores/useWorkspaceStore` (different
shape); `useParentChatStore`/`useChildChatStore` (bubble-only, going away); `standaloneApi` vs `agentApi`; and the
find-or-create-tab → `restoreSession` → `ChatArea` boilerplate copy-pasted 4× (`PlatformChat`, `ChildPlatformChat`,
`VideoStudioSurface:1129`, `DominionSurface:406`) — refactorable into `useProductChatTab(profileId)` once all live
under `products/`.

**Move plan (after the bubble chat is deleted — never migrate code about to be removed):**
1. `git mv frontend/learning-app/src → frontend/src/products/sparkquill/`; rename `useWorkspaceStore` →
   `useSparkQuillWorkspaceStore`, `stores/types` → `sparkQuillTypes`; rewrite `../../../src/X` → `../../X` (4 files).
2. Add `'sparkquill'` to `PRODUCT_SURFACES` (`productSurfaceConfig.ts:1`), the `ProductSurface` union (`useProductSurfaceStore.ts:4`,
   bump version → 4, extend `migrate` `:24-35`), `ProductSurfaceSwitcher` (`:22-32`, add `SparkQuillMark`), and a fourth
   `App.tsx` branch `lazy(() => import('./products/sparkquill/SparkQuillSurface'))`. `LearningApp` already returns a
   full-screen `<main className="learning-app">` — export it as `SparkQuillSurface`.
3. CSS: tokens (`learning-app.css:4-72`) and the already-working product-owned shadcn overrides `.fl-platform-chat`
   (`:1228-1294`) move to `products/sparkquill/sparkquill.css` (lazy chunk); drop the two global lines (`:1-2`);
   nest the ~80 unscoped generic selectors (`.primary-button`, `.chat-bubble`, `.composer-*`, `.engine-*`, …) under
   `.learning-app`; the Phase-7 bubble ranges never move.
4. **Preflight** — the real work: `learning-app/tailwind.config.js:12` has `preflight: false`; inside `frontend/src` it
   is on. Every `.fl-*` control was authored against browser defaults → explicit resets for ~10 element types or an
   `@layer` above `base`; mandatory visual pass on all five screens.
5. **Theme**: `ThemeContext.tsx:4` `FORCED_THEME='dark'` + `index.html class="dark"` would activate every `dark:` variant
   inside the hosted `ChatArea`; SparkQuill is light-first with its own toggle → surface toggles `documentElement`
   class on mount/unmount, or `ThemeProvider` becomes surface-aware.
6. Move `public/sparkquill-*.svg` + `public/lib/` (JSXGraph, 1 MB) → `frontend/public/`; drop the duplicate
   `import '../../../src/index.css'`; re-establish `runtimeConfig.ts`'s before-React ordering or drop it
   (`index.html:18` already loads `/runtime-config.js`); fold `VITE_SPARKQUILL_BACKEND`/`VITE_FAMILY_API`/
   `VITE_PLATFORM_API` into the main env surface (mostly deletable once standalone is gone).
7. Delete `frontend/learning-app/{package.json,package-lock.json,vite.config.ts,tsconfig.json,tailwind.config.js,
   postcss.config.js,index.html,node_modules}`; retarget `sparkquill-desktop.yml:20,80-90` and `dev-setup.sh:14-19`
   to `frontend/` + `frontend/dist`; confirm `check:bundle-budget` (lazy surface stays out of the eager set).

**Server side (the ~10-line change that makes this work):** `server.go:2514-2521` must emit `enabledProductSurfaces`,
`defaultProductSurface`, `appName`, `faviconUrl` (from env — `AGENTWORKS_APP_NAME`/`AGENTWORKS_FAVICON_URL` already
exist in `run_server_with_logging.sh:1013-1015` but Go ignores them — or from the manifest's `branding`/`ui.surface`,
finally giving those keys meaning). With `enabledProductSurfaces:['sparkquill']` the switcher shows one entry and
`App.tsx:97-110` pins the surface; the desktop loads plain `/`, same SPA as AgentWorks.

**Effort:** ~6–9 days after the bubble-chat deletion (≈half is preflight/theme); the move + rewire + config deletion is
~2 mechanical days. **Risks:** preflight regression (230 hand-written colours, ~80 selectors); `dark` leakage;
stale persisted surface on upgrade (store `migrate` + version bump); eager-bundle budget; runtime-config keys not yet
served by Go.

## A8. Feature parity — `family-server` → platform (`cmd/server` + workspace-server + `internal/sparkquillproduct`)

Ledger of what's stubbed: `frontend/learning-app/src/api/platformApi.ts` (`notYet()` at `:35`, used `:375-378`).

| Area | Status | Detail |
|---|---|---|
| Chat turns / steer / streaming | **done** | `/api/agent-profiles/{id}/query`, `/api/sessions/{id}/live-input` (+`status.can_steer`), `/events/stream`. Parent = singleton conversation key `main`; child = keyed by project, resolved from `activities/<slug>/product.json` (`product_conversation_registry.go:315-418`, written by `tools.go:340-352`). |
| Engine onboarding | **blocking gap** | `enginedetect` is family-server-only. Platform equivalent exists — `GET /api/llm-config/providers` (`llm_provider_manifest.go:26-53`: `runtime_available, auth_configured, usable, setup_hint` per provider) — but the adapter fakes `engines()` from `provider_options` (`platformApi.ts:276-281`), `selectEngine` is a no-op, `setup()` drops the engine step (`:254-266`). No persistence of the family's engine choice anywhere (`FamilyState` `profile_runtime.go:24-30` has only child/parent_label/pin_hash/watch_sites). AgentWorks desktop does no CLI detection/login either; a fresh Mac gets nothing telling the family to install/log in to a coding CLI. **Plus the trap:** `AGENT_PRODUCTS=sparkquill` ⇒ `isSingleProductServerDeployment()` ⇒ every `claude-code` turn 400s without `CLAUDE_CODE_OAUTH_TOKEN` (`server.go:139-172, 3591-3594`; test `claude_code_token_gate_test.go`). |
| Models / Fast Mode | gap | family: `models_api.go`, `model_tier.go` (Fast Mode = lower reasoning effort, stored in `family.json`). Platform: stubbed (`platformApi.ts:334-337`); no per-user model/effort override in `agentprofiles` (`resolveProfileRuntimeModel` `agent_profile_runtime.go:111-128` accepts only exact catalog pairs). Needs new storage (extend `FamilyState`) + a runtime hook — design work. |
| Voice | **done** (same engine, same WS) | `voicestt.ServeStream` both sides; platform `GET /api/voice/stream?profile_id=sparkquill&token=` (`voice_stt_routes.go:89-104`, gated on `runtime.capabilities.voice`), `/status`, `/warm` wired. **Missing on platform:** `/unload` (menu-bar hide frees ~1 GB), `/hardware` (tiers), `/model/install|remove`, `/transcribe` (WhatsApp voice notes). |
| WhatsApp | **stub; designs incompatible** | family: one paired self-chat, `@child`/`@parent` flip a **persistent** attachment-routing mode (`whatsapp_routing.go`), media dropped into the target conversation with the path in the prompt. Platform: per-user pairing with ownership claim (`whatsapp_routes.go:52-66`), routing = `@<slug>` → **workflow** map — no (profile, conversation-key) target, no persistent mode, no media→activity step; `send_whatsapp_file` missing. Single-user pairing works (binds to `default`). Needs a SparkQuill route-table adapter in `services/whatsapp_service.go`. |
| Pulse / check-in | done structurally, 4 gaps | Both use `pkg/productschedule`; enable/disable/trigger wired (`product:sparkquill:pulse`). Gaps: quiet rule **inert** (`ProductScheduleService.sinceInteractive` nil, `product_schedules.go:113-115`) so check-ins can land mid-conversation; `cadence_hours`/`preferred_hour` not user-configurable (405, `scheduler_routes.go:1176-1179`); 3 of 6 checks dropped (watched sites, weekly schedule, backup); raw instruction shown as the user turn (no clean trigger line); **no local desktop notification channel** (`notification_destination.go:11-14` = Slack/webhook/WhatsApp/Gmail). |
| PIN / handoff / child sandbox | **done, better** | Same sha256 PIN via `family.json` (`workspace.ts:15-18,139-152`); handoff writes the same `current-activity.json`. Child sandbox is real: `server.go:5079-5100` roots the shell at `_users/<id>/Chats/SparkQuill/activities/<slug>`, strict + network off (`product.yaml` child `sandbox`); `keys/` is outside `activities/` (`activities.go:76-93`) — unreachable, unlike family-server where keys sat in-folder hidden only by prompt. **Requires `product.json` per activity** or the child gets no conversation. |
| Files / state / upload | done, one behaviour change | tree/file/raw/upload via `/api/wp`. `state/<key>.json` at family root instead of `<activity>/attempts/<key>.json`. `RenderActivityPage`, pins, progress report ported; `create-academic-map`/`create-test`/`create-study-material` skills **not** embedded (commands survive as prompts); archive = prompt concept, no auto-archive (by decision); no `/api/reset`. |
| Secrets | done | per-user store, flat namespace (SparkQuill vs AgentWorks name collisions) — open item unchanged. |
| `/api/execute`, `/api/cdp-check`, browser | done by architecture | family's loopback `browser_backend.go` deletes; real workspace-server endpoints (`workspace/handlers/shell.go:24`, `cdp_check.go:21`). `/api/browser/status` has no route (adapter hardcodes `cli_installed:false`). |
| tmux `sq-*` + sweep | delete | `cmd/server` reaps by lease/ownership, not prefix; carrying `sq-*` over would blind the reaper. |
| Week / child-schedule / activity log | not in `FamilyApi` | standalone-only; port-or-drop decision. |

## A9. Data migration `~/.sunlit-learning` → `<DOCS_DIR>/_users/default/Chats/SparkQuill/`

- **Where user paths resolve:** workspace-server rewrites `Chats/…` → `_users/<uid>/Chats/…` from `X-User-ID` (`workspace/utils/path.go:135-181`; header stamped by the agent server's proxy from the verified identity, `workspace_proxy.go:60-63`); agent side does the same for tool roots (`agentProfileRuntimeWorkspace` `agent_profile_runtime.go:102-109`). Single-user id = `default` (`DEFAULT_USER_ID`); workspace-server pre-creates `_users/default/{Chats,memories,chat_history}`.
- **Template:** `cmd/family-server/migrate.go` (825 lines): destination marker (`:24-45`), structural probe, `cp -a` backup then abort-on-failure (`:70-79`), never-overwrite `mergeMove` with `.migrated-dup[N]` (`:126-165`), `uniqueDir`/`uniqueBase`, fuzzy key→activity matching (`:461-560`), unplaceables → `_legacy/`, `[migrate]` logging on every decision.
- **Mapping:** `family.json` field-filtered (keep `child` (drop `created_at`), `parent_label`, `pin_hash`; `pulse.watch_sites ∪ school_portal_url` → top-level `watch_sites`; drop `engine/selected_models/fast_mode/child_fast_mode/pulse.*/schedule/whatsapp_voice_enabled` — no platform home). Optionally seed `_users/default/chat_history/product-schedules.json` with `enabled`+`last_run_at` so the check-in doesn't fire immediately. `Subject/Topic/<slug>/` → `activities/<slug>/` (lowercase, validate `activitySlugPattern` `activities.go:52`, numeric suffix on collision; Subject/Topic already live in `activity.json`). **Synthesize `activities/<slug>/product.json`** `{schema_version:1, product:"sparkquill-child", id, title, description, session_id:"product-<uuid>"}` (`activities.go:39-46`; title+session_id required). **Move `*-KEY.md` → `keys/<slug>-KEY.md`** via `answerKeyDestination` (`activities.go:84-90`) — security-relevant. Items verbatim (bare filenames); `attempts/` verbatim; `current-activity.json` `dir` rewritten; `materials/`, `inbox/`, `reports/`, `memory/*` verbatim (activity-log/deadlines/child-schedule kept as evidence, no consumer); `archive/` flattened with the shared slug allocator; **delete** `skills/` + `.agents/skills/` (platform embeds `SkillFiles`; note 3 skills not embedded = behaviour change to confirm); drop `whatsapp-routing.json`, session handles (`*.session.json` — CLI resume ids, one cold start), scratch dirs, `whatsapp/session.db`; secrets → **re-enter** (never copy the AES files into the docs dir).
- **Conversations: archive, don't convert.** Target is `llmtypes.MessageContent` + `ui_events` under `_users/<id>/chat_history/<date>/session-<id>-conversation.json` with an overwrite guard; session ids are minted by the registry on first use. Keep `parent.json` / `<activity>/conversation.json` as `_legacy/conversations/…` / `activities/<slug>/legacy-conversation.json` so the agent can still read them for reports. Real history import = separate later slice.
- **Where it runs:** a `cmd/server` startup hook next to the SparkQuill registration block (`server.go:1758-1771`, gated by `productEnabled("sparkquill")`), non-fatal on error (no marker → retry next boot), plus a CLI subcommand `agent-server migrate-sparkquill --from --docs-dir --user [--dry-run]`. Not the Electron shell (slug rules, `product.json` shape, `answerKeyDestination` live in Go). Idempotency: destination marker `.migrated-from-sunlit` (source path + timestamp + hash) written **only on full success**; fresh install (no source) → write marker; **non-empty target without marker → refuse and log** (never overwrite a family already on the platform). Backup `cp -a ~/.sunlit-learning ~/.sunlit-learning.pre-platform-backup` first, abort if it fails; **copy, don't move**. Tests that assert the child sandbox must build fixtures under `$HOME` (`characterization_test.go:252-263`), not `t.TempDir()` (`/var/folders` is sandbox-readable).

**Build list from parity+migration** — blocking: (1) engine onboarding + the single-product 400 trap; (2) `product.json` synthesis; (3) key relocation. Gaps ranked: (4) WhatsApp route table; (5) Pulse quiet rule / configurability / dropped checks / desktop notification; (6) model picker + Fast Mode; (7) voice unload/hardware/install/transcribe; (8) `/api/browser/status`, `/api/reset`; (9) week/schedule port-or-drop. Deletes: `browser_backend.go`, `tmux_sweep.go` + `sq-*` setenvs, `skills.go` seeding, `enginedetect` use, `secrets_store.go`, then all of `cmd/family-server`.

## A10. Phased plan

```
P0 server enablers ─┐
                    ├─► P1 shell sharing ──────────────────────────────┐
P2a delete bubble ─► P2b move learning-app ─► P3 blocking parity ─► P5 migration ─► P6 deletions ─► P7 live E2E = MVP
                                                     └─► P4 trailing gaps (after MVP)
```
**MVP (P0–P3, P4 ship-first subset, P5–P7): ~25–30 dev-days. Trailing gaps: ~8–11 days.**

**First shippable milestone:** a family installs `SparkQuill.dmg`; onboarding detects/selects an engine via
`/api/llm-config/providers`; `~/.sunlit-learning` is migrated on first boot (or a fresh family scaffolded); the parent
chats in the shared `ChatArea` (tool rows, pills); handoff opens the child's sandboxed activity conversation; check-ins
run with the quiet rule; voice warms/unloads with window visibility; AgentWorks + SparkQuill run side by side. Trailing:
WhatsApp, Fast Mode/model picker, cadence configurability, desktop notifications, voice tiers/install, browser status,
week/schedule (dropped).

### M1 first — the cheap live build (1–2 days, before P0/P1): platform binaries + existing learning-app dist
Rollout review verdict: get a family-installable build on the platform **before** the shell/frontend refactors, with
almost nothing thrown away. All in `desktop-sparkquill/main.js` (replacing `startServer` `:94-119`) + one preload line:
1. `preload.js:8-11` adds `backend: () => 'platform'` — the learning app is already platform-capable
   (`api/index.ts:11-14`, `platform/runtimeConfig.ts:8-12`); nothing else in the frontend changes.
2. Stage `frontend/learning-app/dist` as `Resources/static` (not `resources/web`) and set the agent server's cwd to
   Resources (`server.go:2533` serves `./static/`); ship `../agent_go/configs → configs` like `desktop/package.json:45-48`.
3. **Generate + persist `AUTH_SECRET`** in `<userData>/config.json` (mirror `desktop/main.js:308-318,496-504`) — the
   server **fatals** on an empty/default secret (`server.go:1525`, `auth_middleware.go:143-150`); the SparkQuill shell has
   no reference to it today.
4. Spawn `workspace-server server --port <detect(45779)> --docs-dir <userData>/workspace-docs` (env `DOCS_DIR`,
   `DATA_DIR=<userData>/data`, `NATIVE_WORKSPACE=true`, `WORKSPACE_API_TOKEN=<32 B hex>`; ready on `DynamicPort:`), copy
   `Resources/configs/mcp_servers_clean.json` → `<userData>/configs/mcp_servers.json`, then spawn `agent-server server
   --port <detect(45778)> --log-file … --mcp-config …` with `AUTH_SECRET`, `WORKSPACE_API_URL`, `WORKSPACE_DOCS_PATH`,
   `DOCS_DIR`, `LOG_FILE`, `NATIVE_WORKSPACE=true`, `WORKSPACE_API_TOKEN`, `AGENTWORKS_SKIP_GLOBAL_BROWSER_CLEANUP=true`,
   `AGENTWORKS_BROWSER_SESSION_PREFIX=sparkquill`; `MULTI_USER_MODE` and **`AGENT_PRODUCTS` unset**. Health on
   `/api/health`, then load `http://127.0.0.1:<agentPort>/?v=<version>`. Keep `Resources/lib` (voice dylibs, same
   `build-darwin-voice-binary.sh` the AgentWorks release uses).
5. Add a shell unit test asserting the spawn env contains no `AGENT_PRODUCTS`.
**Gate:** `SPARKQUILL_PLATFORM_URL=http://127.0.0.1:<port> npx vitest run platformApi.live` against the packaged app's
server + a live parent turn and a child handoff turn observed in ChatArea (agentic sign-off). Throwaway: only the
`web→static` staging line; the spawn code becomes `desktop/lib/spawnServers.js` in P1. Note: without P3, a family on a
fresh Mac has no engine onboarding (see P3) — M1 is for families who already have `claude`/`codex` logged in.

### P0 — Server-side enablers (Go) — 2 days
- `/runtime-config.js` (`server.go:2514-2521`) also emits `enabledProductSurfaces`, `defaultProductSurface`, `appName`,
  `faviconUrl` from env (`AGENTWORKS_ENABLED_PRODUCT_SURFACES`, `AGENTWORKS_DEFAULT_PRODUCT_SURFACE`, `AGENTWORKS_APP_NAME`,
  `AGENTWORKS_FAVICON_URL` — names already used by `run_server_with_logging.sh:1011-1021`), keys omitted when unset so
  AgentWorks output is byte-identical. New `cmd/server/runtime_frontend_config.go` + test. Manifest `ui.surface` fallback
  only if it stays ~15 lines (`branding.favicon` is an icon *name*, not a URL).
- `STATIC_DIR` env for the SPA mount (`server.go:2533`) so the shell can use `userData` as cwd and drop
  `unifyAgentLogsDir` (`desktop/main.js:891-924`); `mcp_servers_clean.json` path → `getResourcesDir()` (`main.js:1215`).
- `registeredProductIDs()` includes `sparkquill` (`user_directory.go:827-835`).
- `AGENTWORKS_CLI_SECURITY_DIR` override in `pkg/clisecurity/store.go:40-46` (today hardcoded + `log.Fatalf`).
- Voice-model first-download lock in `pkg/voicestt/model.go` (~`:253,282`): `O_EXCL` lock beside `dest`, second process waits.
- **Decision — token gate:** leave `AGENT_PRODUCTS` **unset** in the SparkQuill shell (no exemption code); the frontend
  allowlist hides the other surfaces. `claude_code_token_gate_test.go:69` already pins the unset-exempt case.
- **Isolation env the shell passes** (= descriptor `extraServerEnv`; recipe `scripts/run-local-instance.sh:277-297`):
  per-product `userData` (→ separate `RUNLOOP_DOCS_DIR`), preferred ports 45778/45779,
  `AGENTWORKS_SKIP_GLOBAL_BROWSER_CLEANUP=true`, `AGENTWORKS_BROWSER_SESSION_PREFIX=sparkquill`, `AGENT_BROWSER_CONFIG`,
  `AGENTWORKS_CLI_SECURITY_DIR`, `AGENTWORKS_STRICT_PROCESS_OWNERSHIP=true`. **Not** `*_SESSION_PREFIX=sq-*`, not `TMUX_TMPDIR`;
  `WHATSAPP_ENABLED`/`SCHEDULER_ENABLED` at defaults (Pulse needs the scheduler).

### P1 — Shell sharing: one `desktop/` emits both apps — 3–4 days
- Add `desktop/lib/{loginEnv,boundedLog,health,externalNav,spawnServers,updater,productJson}.js` extracted from
  `desktop/main.js` (`:54-92, :165-202, :1358-1389, :1797-1838, :1108-1356, :1401-1525+1675-1706`), `updater` with
  `updateMode: install|notify`.
- Add `desktop/products/{agentworks,sparkquill}.js` descriptors (`appId, productName, userDataDirName, preferred ports,
  windowTitle, backgroundColor '#fbf7ef', theme, trayLabels, iconPath, tagPrefix, dmgName, installScriptUrl,
  closeBehavior quit|hide, voiceLifecycleOnVisibility, menuFolderLabel, updateMode, extraServerEnv`); selection
  `AGENTWORKS_PRODUCT` env → `Resources/product.json` → `agentworks`.
- Add `desktop/build/{agentworks,sparkquill}.yml` (differ in `appId`, `productName`, category, icon, `extraResources`
  incl. `product.json`); scripts `dist:agentworks` / `dist:sparkquill` (`electron-builder --config build/<id>.yml`).
- Modify `desktop/main.js` to use `lib/*` and descriptor-conditional bits (close-to-tray, tray/menu labels, background,
  delete `unifyAgentLogsDir` once `STATIC_DIR` is passed). **Voice warm/unload on show/hide:** shell emits an IPC
  `window-visibility` event via `preload.js`; the SparkQuill surface (holding the JWT) calls `/api/voice/warm|unload` —
  no auth plumbing in the shell (`/api/voice/*` is behind `AuthMiddleware`, unlike family-server).
- `install.sh` parameterized (`APP_NAME`, `TAG_PREFIX`, `DMG_NAME`, `ENV_PREFIX`); `install-sparkquill.sh` becomes a
  3-line wrapper (public curl URL stays). `sparkquill-desktop.yml` mirrors `desktop-release.yml:60-106` then
  `npm run dist:sparkquill`, keeps `gh release create sparkquill-v*`. `desktop/dev-setup.sh` builds `agent-server` via
  `build-darwin-voice-binary.sh` so `resources/lib` exists locally (fixes the known clean-build failure) and accepts
  `--product sparkquill`.
- `desktop-sparkquill/` keeps nothing; deleted in P6 after P1 is verified live.

### P2a — Delete the standalone bubble chat in place — 2 days (before moving anything)
- `learning-app/src/api/index.ts`: remove the `backend` switch and token mirroring; single `createPlatformApi`.
  Delete `apiBase.ts`, `VITE_SPARKQUILL_BACKEND/VITE_FAMILY_API/VITE_PLATFORM_API`, `api/standaloneApi.ts`(+test),
  `stores/useParentChatStore.ts`, `useChildChatStore.ts` (fold surviving child-activity/viewer fields into
  `useFamilyStore`/the renamed workspace store).
- `LearningApp.tsx`: remove every `backend !== 'platform'` branch and standalone effect (history loads `:1144,1685,
  1771,1934,2545`; watchers `:1733,1822`; send/steer `:2211-2421`; JSX `:2829-2875, 3936+`; guards `:1768,1810,1933`);
  `PlatformChat` (`:2939`) and `ChildPlatformChat` (`:4080`) become unconditional.
- `familyApi.ts`: drop chat methods `:94-108` and their `platformApi.ts:296-311` implementations; keep
  `engines/validateEngine/selectEngine` for P3. CSS: bubble-only ranges (`:149-220`, child bubble ~`:596-668`) —
  confirm each class by grep after the TSX deletion; `.fl-thread/.fl-msg/.fl-bubble` stay (landing greeting).
- `platformApi.live.test.ts:60-61`: drop the stale `week` assertion. Two commits (stores, then JSX) with the parent
  `tsc` as the guard — this file has never been type-checked.

### P2b — Move `learning-app/src` → `frontend/src/products/sparkquill/` — 6–8 days
Mechanical (~2 d): `git mv`; rename `useWorkspaceStore` → `useSparkQuillWorkspaceStore`, `stores/types` →
`sparkQuillTypes`; rewrite `../../../src/X` → `../../X` (4 files); check `persist` keys vs `frontend/src/stores`;
`SparkQuillSurface.tsx` = `LearningApp` renamed; delete `main.tsx`, `platform/runtimeConfig.ts`, `BuildUpdateNotice`
(→ `UpdateProgressToast`/`staleChunkReload`); register the surface (`productSurfaceConfig.ts:1`, store union/version 4/
migrate, `ProductSurfaceSwitcher.tsx:22-32` + `SparkQuillMark`, `App.tsx:45-47,908-914`) and update their tests; move
`public/{sparkquill-*.svg,lib/jsxgraph*}` → `frontend/public/`; delete the learning-app root configs/`node_modules`/`dist`;
retarget `dev-setup.sh`. Real work (~4–6 d): scoped preflight resets under `.learning-app` (~10 element types) +
mandatory visual pass on all five screens; CSS tokens (`:4-72`) + `.fl-platform-chat` (`:1228-1294`) → lazy
`sparkquill.css`, drop globals (`:1-2`, duplicate `index.css` import), nest ~80 unscoped selectors; theme: surface
toggles `documentElement` light/dark on mount/unmount (`ThemeContext.tsx:4` forces dark); first-ever `tsc` on the
moved code (0.5–1 d); bundle budget (`check-bundle-budget.mjs` eager gzip ≤ 1,030,000 — keep everything lazy);
trivial dedupe now (`SyntaxHighlightedCode`). Larger dedupes (MicButton, ChatRenderer vs MarkdownRenderer, file viewer,
secrets UI) and a shared `useProductChatTab(profileId)` across the four surfaces = post-MVP slice (2–3 d).

### P3 — Blocking parity — 3–4 days
1. **Engine onboarding + persisted choice:** add `Engine` to `FamilyState` (`profile_runtime.go:24-30`, required or
   `set-child-profile` re-serialization drops it); `engines()` → `GET /api/llm-config/providers`
   (`llm_provider_manifest.go:26-53`) ∩ parent `runtime.provider_options`; `validateEngine` → `usable`; `selectEngine`
   → `family.json.engine`; `setup()` reinstates `next_step:'engine'`; existing `.engine-card` UI shows `setup_hint`.
   Turn wiring **A (ship):** `PlatformChat`/`ChildPlatformChat` set the tab's provider/model from `family.json.engine`
   at tab creation (`PlatformChat.tsx:83-88`; server honours exact `provider_options` pairs,
   `agent_profile_runtime.go:111-128`). **B (later, with P4-6):** server-side per-user product runtime preference so
   Pulse runs honour it too. **Verify live first** that the agent-profile `/query` handler forwards `req.Provider`
   (`server.go:3578-3580` falls back to it) — the one assumption under A.
   **Facts that shape this:** today a fresh Mac with no CLI reaches the parent screen (engine step skipped), and the first
   turn either fails with a dev-worded spawn error or, with `claude` installed but not logged in, **hangs on the CLI's
   login prompt for up to the adapter's 20-min inactivity timeout** (`platformApi.ts:40`; described at `server.go:3585-3589`).
   `handleQuery` has no runtime pre-check. In the manifest `providerAuthConfigured` returns `true` unconditionally for
   `claude-code`/`codex-cli` (`multiagent_llm_tools.go:427-439`), so `usable == runtime_available` (`LookPath`) — real
   login state needs `POST /api/llm-config/validate-key` → `validateClaudeCodeCLI` (`llm_config_handlers.go:1023`, a real
   turn, bounded) — same as `enginedetect.Validate` did. `setup_hint` copy is developer-worded; keep the learning app's
   parent-facing copy per engine (`pres()` `LearningApp.tsx:4527`). Reuse AgentWorks' status semantics
   (`components/llm/providerStatus.ts:7-19`), not its Model Library modal.
2. **`product.json` synthesis:** extract `EnsureActivityProject(...)` from `tools.go:340-352`; call from
   `create-learning-activity`, the migration, and as self-heal on `open-activity`/handoff.
3. **Key relocation:** reuse `ws.relocateAnswerKeys` (`tools.go:356`) + `answerKeyDestination`; also on handoff.

### P4 — Gaps: ship-first vs trail
| Gap | Call | Effort |
|---|---|---|
| Pulse quiet rule inert | **ship-first** — wire `ProductScheduleService.sinceInteractive` (`product_schedules.go:117`, used `:325`) to last user-message time per (user, profile) | 0.5–1 d |
| `/api/voice/unload` | **ship-first** — `Manager.Unload()` exists (`voicestt/manager.go:212`); handler beside `handleVoiceWarm` (`voice_stt_routes.go:124`) | 0.5 d |
| Desktop notification for check-ins | trail — renderer toast + preload → Electron `Notification`; no new destination kind | 1 d |
| Cadence / preferred-hour configurability | trail — per-user override where `scheduler_routes.go:1176-1179` 405s | 1–2 d |
| Dropped checks / clean trigger line | drop the three checks (decision); trigger line trails | 0.5 d |
| Model picker + Fast Mode | trail — extend `FamilyState` + server runtime hook (also gives P3-B); hide Settings rows until then | 2–3 d |
| WhatsApp `@child/@parent` route table | trail — **hide the WhatsApp button/modal for MVP**; adapter in `services/whatsapp_service.go` (`resolveSlugRoute:583`) mapping to (profile, conversation key) + persistent mode + media→activity + `send_whatsapp_file`; single-user pairing would bind to the default AgentWorks chat — don't half-ship | 3–4 d |
| Voice `/hardware`, `/model/install|remove`, `/transcribe` | trail; `/transcribe` only with WhatsApp voice notes | 1–2 d |
| `/api/browser/status`, `/api/reset` | browser status trails (query `cdp_check.go:21`), hide row; **`/api/reset` dropped** | 0.5 d |
| Week / child-schedule / activity log | **dropped**; `memory/*` kept as evidence | 0 |

### P5 — Data migration (Go): startup hook + CLI — 3–4 days
`agent_go/internal/sparkquillproduct/migrate.go` (+test), same package as `activitySlugPattern`/`activityProject`/
`answerKeyDestination`/`EnsureActivityProject`. **Move** the generic primitives from `cmd/family-server/migrate.go`
(`copyDirCmd:114`, `mergeMove:131`, `copyFile:187`, `uniqueDir:370`, `uniqueBase:384`, fuzzy matching `:561-596`,
marker pattern `:29-47`). Direct filesystem I/O under `$WORKSPACE_DOCS_PATH/_users/default/Chats/SparkQuill/`; skip
when the env is unset (cloud never migrates). Mapping per A9 (+ carry `engine` into `family.json` for P3). Seed
`product-schedules.json` (`productScheduleStatePath` `product_schedules.go:100-102`, key `sparkquill/pulse`) with
`enabled` and `last_run_at=now` so the first check-in doesn't fire mid-onboarding. Safety: backup then abort-on-failure;
copy not move; marker `.migrated-from-sunlit` only on full success; fresh install → marker; **non-empty target without
marker → refuse + log**; unplaceables → `_legacy/`. Entry points: startup hook inside the `productEnabled("sparkquill")`
block (`server.go:1758-1771`), gated on `NATIVE_WORKSPACE=true` and `MULTI_USER_MODE` unset, non-fatal; env
`SPARKQUILL_LEGACY_DIR` (default `~/.sunlit-learning`), `SPARKQUILL_SKIP_MIGRATION`; cobra subcommand
`migrate-sparkquill --from --docs-dir --user [--dry-run]` (`agent_go/cmd/root.go:104-106`). Tests: fixture builder,
idempotency, refuse-non-empty, dry-run, slug collision, key relocation, `product.json` with `product-` id, archived
conversations; the sandbox assertion uses a `$HOME` fixture (`characterization_test.go:252-263` pattern, ported).

### P6 — Deletion order and gates — 1–2 days
`cmd/family-server` is one binary: piecemeal deletions inside it buy nothing, and `internal/enginedetect` has no other
importer — treat A8's per-file list as a **ledger of behaviours that stop existing** and delete the directory in one
flag-day commit. All code deletions are git-reversible; the family's data is not — hence copy-not-move in P5.

| Step | Delete | Gate before |
|---|---|---|
| D1 | standalone-gated effects in `LearningApp.tsx` (`:1768-1830` 20 s poll + `watchChild`, `:1933`, `:2102`, `:2486-2491`, `:2541-2549`) | M1 live gate passed |
| D2 | bubble branches (`:2938-2941` → `PlatformChat` only, `:4078-4080` → `ChildPlatformChat`), `useParentChatStore`/`useChildChatStore` message state (keep `focusInput`/`childSending`, used `:962-964`), CSS `.fl-thread/.fl-msg/.fl-bubble` `:297-323,634-641`, `.fl-tmsg/.fl-tbubble` `:597-628`, `.chat-bubble` `:201-207` (keep `--reply-bubble-*` tokens if `.fl-platform-chat` references them) | vitest green + visual pass on the 5 screens |
| D3 | `standaloneApi.ts`(+test), `apiBase.ts`, backend switch in `api/index.ts`, `VITE_SPARKQUILL_BACKEND/VITE_FAMILY_API`, `FamilyApi` narrowing | D2 merged |
| D4 | Settings rows already hidden on the platform (`:2829-2845` cadence/hour, `:3936` Fast Mode/model picker), WhatsApp QR section (`:3816` renders a broken `<img>` from `whatsappPairImageUrl()=''`) | per-behaviour decision below |
| D5 **(done 2026-09-06)** | whole `agent_go/cmd/family-server`, `internal/enginedetect`, `desktop-sparkquill/` shell duplication (P1; `updater.js` stays, it is the only copy), `install-sparkquill.sh` data-path line, `sparkquill-desktop.yml` retarget | migration verified on a copied real `~/.sunlit-learning` (P5); every family on the new build ≥ 1 release |
| D6 | `frontend/learning-app` root (after the P2b move) | P2b preflight/theme pass |

Behaviours with no platform replacement — decisions: **week/child-schedule/activity log** → ship without (agent tools
only; the UI never called `/api/week`; keep `memory/child-schedule.json` as evidence). **WhatsApp** → hide the connector
section in platform mode (today the poll fails silently and the QR is broken); if the migration finds
`whatsapp/session.db` in the source, show a one-time "WhatsApp comes back in the next update" notice. **Desktop
notification** → stub in M2 with a `local` destination (osascript, gated on `NATIVE_WORKSPACE=true`, ~40 lines in
`services/notification_destination.go`); check-in is off by default (`product.yaml:133`) so not M1-blocking. **Model
picker / Fast Mode** → already hidden; ship without. **Voice unload/install/hardware** → the shell's hide→unload gets a
harmless 404 in M1 (~1 GB stays resident while hidden); add `/api/voice/unload` in M2. **`/api/reset`** → drop (a hidden
"Reset setup" that deletes `family.json` via `/api/wp` if ever needed). Sweep `family-server` mentions across `docs/`,
`ROADMAP.md`, `task.md`, `family-learning-architecture.md`, workflows; update this doc's status.

### P7 — Verification — 3 days (interleaved; final live pass last)
- Go: `go test ./cmd/server/... ./internal/sparkquillproduct/... ./pkg/clisecurity/... ./pkg/voicestt/... ./pkg/browser/...`;
  new tests for runtime-config emission, `registeredProductIDs`, clisecurity override, download lock, migration suite.
- Frontend: `npm run build` (tsc + vite + bundle budget), `npm run lint`, `npm test` (moved tests; live test via env URL);
  surface tests for five surfaces.
- **Live Electron E2E** (project rule: LLM-driven paths verified only by a live run with agentic sign-off): fresh install
  → onboarding shows engine detection, child, PIN; run with a **copied** real `~/.sunlit-learning` → migration log,
  marker, backup, `product.json` + `keys/`, archived conversations; parent turn with tool rows + pills; handoff → child
  turn with celebrate/scene; from the child shell assert `keys/` unreachable; check-in "run now" → one saved check-in summary;
  relaunch restores history; AgentWorks + SparkQuill simultaneously (distinct ports/userData/docs roots, no browser
  kill cross-fire, tmux intact); hide → `/api/voice/unload`, show → `/warm`.

### Implementation status (2026-09-06, uncommitted branch work)

Executed in the order **M1 → P0 → P3 → P5** (not the P2-first chain above): the engine onboarding and the data
migration do not depend on the learning-app move, and running them first puts a real family on the platform
before the CSS/consolidation work. Then P4, P2a, P1, P6 and P2b (all below). P7 remains.

- **M1 done, live-verified.** `desktop-sparkquill/main.js` spawns `workspace-server` (45779) + `agent-server`
  (45778), persists a generated `AUTH_SECRET` in `<userData>/config.json`, serves `frontend/learning-app/dist` as
  `Resources/static`, never sets `AGENT_PRODUCTS` (`lib/agentEnv.js` + `node --test`). `preload.js` reports
  `backend: 'platform'`. Onboarding, PIN, a parent turn (Claude Code, tool rows) and relaunch-with-history all
  observed in the real Electron window.
- **P0 done.** `runtime_frontend_config.go` (`AGENTWORKS_ENABLED_PRODUCT_SURFACES` / `_DEFAULT_PRODUCT_SURFACE` /
  `_APP_NAME` / `_FAVICON_URL`, omitted when unset), `STATIC_DIR`, `registeredProductIDs()` + `sparkquill`,
  `AGENTWORKS_CLI_SECURITY_DIR`, voice-model download lock (`pkg/voicestt` `acquireModelDownloadLock`).
- **P3 done, live-verified — with one contract change the plan missed.** The product-chat route
  (`AgentProfileChatRequest`, strict decode) had *no* field through which a client could pick a provider, so
  "turn wiring A" could never have worked: `queryRequestForAgentProfileChat` always left `Provider/ModelID` empty
  and `resolveProfileRuntimeModel` always fell through to the `default: true` option. Added `engine` (one of the
  profile's `provider_options[].id`; undeclared → 422; raw `provider` still → 400) and threaded it
  `family.json.engine` → tab metadata `agentProfileEngine` → `buildAgentProfileChatRequest` → `/query`.
  `platformApi.ts` now really implements `setup()` (engine step first), `engines()` (`/api/llm-config/providers`
  ∩ `provider_options`), `validateEngine()` (`/api/llm-config/validate-key`), `selectEngine()`. `FamilyState` gained
  `Engine` so Go-side rewrites of `family.json` keep it. Settings changes propagate to open tabs
  (`applyFamilyEngineToOpenTabs`).
- **P5 done, verified against the real `~/.sunlit-learning`** (into a scratch docs dir: 16 live activities all
  with synthesized `product.json`, 91 archived, 68 keys relocated to `keys/` with none left in reach, 0 session
  handles, 0 secrets, pointer rewritten, `engine: codex-cli` carried, check-in seeded as just-run; source
  byte-count unchanged). `internal/sparkquillproduct/migrate.go` + `cmd/server/sparkquill_migration.go`
  (startup hook gated on `NATIVE_WORKSPACE=true` + single-user; CLI `agent-server server migrate-sparkquill
  --from --docs-dir --user --dry-run --allow-existing`). Deviations from A9/P5 as written: **no `cp -a` backup
  step** — the migration is copy-only and never touches the source, which is the safety property (a 3.3 GB home
  full of voice models is not worth duplicating); `_legacy/` receives unplaceables verbatim; the startup hook
  refuses a non-empty unmarked target and points at `--allow-existing`, which merges never-overwriting and lets
  existing `family.json` choices win.
- **P4 ship-first done, live-verified.** The quiet rule was not just switched off on the platform; it had two
  further defects fixed here: (1) only a *successful* run moved `last_run_at`, so a failing check-in re-fired on
  every 60 s tick — `productschedule.Inputs` gained `LastAttempt`/`ConsecutiveFailures` and `Decide` applies an
  exponential retry backoff (30 min doubling, capped at 6 h / the cadence); (2) nothing survived a restart, so
  every due check-in fired on the first tick after launch — `cmd/server/product_interactions.go` keeps a per-user
  `chat_history/product-interactions.json` (stamped by the product chat and conversation-open handlers only, never
  by scheduled runs, so a check-in's own turns cannot defer it) and feeds `ProductScheduleService.sinceInteractive`.
  Deferral is surfaced as `deferred_reason` on the job response. Observed live: overdue check-in + app just opened
  → `schedule.log`: "deferring … user active 59s ago (overdue by 1h1m, forcing after 4h)". `/api/voice/unload` added
  (gated like warm); the shell now sends a `window-visibility` IPC and `learning-app/src/platform/voiceLifecycle.ts`
  calls unload on hide / warm-if-installed on show — observed live: `released=true` on hide, `loading=true` on show.
  Note for P1: the scheduler writes `logs/schedule.log` relative to the server's cwd (= `Resources/`), the same cwd
  coupling `STATIC_DIR` was added to remove; the shell should pass a `LOG_DIR`/cwd under `userData` once it moves.
- **Found by the first real family turn on Codex (2026-09-06):** `product.yaml` pinned the Codex option to
  `gpt-5.4`, which Codex 0.147 rejects for a ChatGPT-account login ("not supported when using Codex with a ChatGPT
  account"); the family's own choice was `gpt-5.6-sol`, and that pin is now `gpt-5.6-sol` for both profiles. Worse,
  the failure was invisible: Codex records a failed turn as `task_complete` with `last_agent_message: null` and the
  API error in `error`, then shows the ready prompt again, so the interactive adapter returned an empty reply that
  `provider_runtime.go` accepted as a launch-only response (a session handle was attached) and the UI showed a bare
  "LLM Generation End · No content generated" card. `multi-llm-provider-go` now reads `task_complete.error`
  (`readCodexRolloutTurnError`) and fails the turn with the unwrapped API message when there is no final answer.
  Open (P4 trail / model picker): the option is still a hardcoded pin; the catalog exposes tier aliases
  (`high`/`medium`/`low`) that would track the provider's defaults instead.
- **P2a done.** The standalone bubble chat is gone: every `backend !== 'platform'` branch, the parent/child bubble
  renderers and composers, the `sendParentText`/`sendChildText`/kickoff handlers, the 20 s pollers and SSE watchers,
  queue drains, auto-scroll, upload/paste/mic composer plumbing, `useParentChatStore`, the message fields of
  `useChildChatStore`, `standaloneApi.ts` (+test), `MicButton.tsx`, `TurnCollector` and the turn-transport types
  (`TurnStreamEvent`/`TurnResult`/`TurnMessage`/`ToolEvent`), `FamilyApi`'s chat methods (only `loadChildConversation`
  survives, for the continue-or-fresh question), and 100 CSS rules that only styled those. `api/index.ts`,
  `runtimeConfig.ts` and `apiBase.ts` are platform-only (same-origin by default, `VITE_PLATFORM_API` for a browser dev
  session). `LearningApp.tsx` 4,637 → ~3,220 lines; 2,673 lines deleted overall. Guarded by `tsc` (the file was never
  type-checked before; only the two pre-existing `TerminalEventTranscript` prop errors remain), the Vite build and the
  learning-app vitest suite. Found on the way: importing the shared `src/api/secrets` client from a bare test hits a
  pre-existing circular import in `src/services` (`api.ts` ↔ `mcpConfigApi.ts` ↔ `useMCPStore`); the adapter now loads
  it lazily. Not done here (P2b/P4 trail): the voice Settings section still talks to family-server-only routes
  (`/api/voice/model/*`, unauthenticated `/api/voice/stream` in `useMicDictation`).
- **P1 done, live-verified.** The two Electron shells were hand-copied files that had already drifted (the
  SIGTERM/SIGINT orphaned-server fix existed in one and not the other). The shared mechanics now live in
  `desktop/lib` as the `agentworks-desktop-lib` package (login-shell env import, bounded log writer, `spawnServer`
  with dynamic ports, `waitForHealth`, external-navigation interception, `installSignalShutdown`), consumed by both
  `desktop/main.js` and `desktop-sparkquill/main.js` (`file:../desktop/lib`; electron-builder packs it into
  `app.asar`, verified). `desktop-sparkquill/lib/agentEnv.js` stays: it is SparkQuill's own env builder.
- **Codex 0.153 (found live during P1 verification, fixed in `multi-llm-provider-go`).** The CLI upgrade broke
  every pane heuristic the tmux transport relied on: a new fixed composer placeholder, a "usage limit resets"
  footer that classified as quota exhaustion, a "Conversation interrupted" notice that swallows the next Enter (the
  previous answer was then returned as the new reply), `turn_aborted` with no `task_complete` (the turn hung),
  and a "Resuming session…" banner that read as a ready composer. Submission is now confirmed from the rollout's
  `task_started`, completion is bound to that turn_id, aborts fail the turn explicitly, resumed sessions pin their
  thread at creation, and a Codex pane left behind by a previous server process (which makes `codex resume` die
  with "already has an active writer") is reaped by owner tag before relaunch. See `codexcli_v0153_*_test.go`.
- **P6 done.** `agent_go/cmd/family-server` (68 files, 11,144 lines) and `internal/enginedetect` deleted;
  `go mod tidy`; `sparkquill-desktop.yml` builds `agent-server` (cgo, from `agent_go`) + `workspace-server`, stages
  `resources/static` and `resources/configs`, runs both shells' unit tests, and triggers on `desktop/lib` and
  `frontend/shared` too; `dev-setup.sh` was already the local mirror. Comments and docs that described the
  standalone as current were reworded; historical design records (`family-learning-architecture.md`, Part B
  below, `docs/bugs`, `docs/refactor`) keep their references and are marked superseded where they had a status.
  Deliberately not replaced (A8 decisions): WhatsApp, `/api/reset`, week/child-schedule routes, model picker.
  **Stopped existing without a platform equivalent:** the standalone's `file://` guard on `agent_browser`
  (`validateBrowserFileURLs`); the platform's browser tool has none, the child profile has `browser: disabled`,
  the parent's browser can open local files. Open item, recorded in `docs/core/browser.md`.
- **P2b done, visually verified.** `frontend/learning-app/src` is `frontend/src/products/sparkquill/` and SparkQuill is
  a product surface of the one Vite build (`SparkQuillSurface.tsx`, lazy; registered in `productSurfaceConfig`, the
  surface store (version 4), the switcher with `SparkQuillMark`, `App.tsx`). The desktop shell pins the build to it
  through `AGENTWORKS_ENABLED_PRODUCT_SURFACES/_DEFAULT_PRODUCT_SURFACE/_APP_NAME/_FAVICON_URL` (`lib/agentEnv.js`);
  `dev-setup.sh` and the workflow build `frontend` and stage `frontend/dist`. Deleted: the learning-app Vite root and
  its configs, `main.tsx`, `platform/runtimeConfig.ts` (runtime-config.js does this now), `BuildUpdateNotice`
  (`staleChunkReload` does this now), the duplicate `index.css` import. First `tsc` under the main config: three real
  errors fixed (a parameter property, a stale `autoScrollMode` prop). The voice modules now call the platform's
  authenticated routes (`/api/voice/stream?profile_id&token`, warm as "install"), closing the P2a trail. CSS: the
  main build ships preflight, which the app was never built against, so `learning-app.css` opens with a scoped
  browser-defaults block (`:where(.learning-app) h1 … { UA values }`, zero-specificity-plus-one so every app rule and
  Tailwind utility still wins, excluding `.fl-platform-chat`), `line-height: normal` for the app's own markup, and the
  app's own copy of the chat resets is gone (loading after the utilities it beat them: the send button lost its fill).
  `useWorkspaceStore` → `useSparkQuillWorkspaceStore`; `.lead` → `.fl-lead` (collided). `LearningApp` syncs the
  document theme class while mounted (ThemeProvider forces dark) so the shared chat follows the family's theme.
  Visual pass: before/after screenshots of parent, activities, settings, child, dark mode, and the three onboarding
  steps on a throwaway profile (`SPARKQUILL_USER_DATA_DIR`, dev-only). Not done: the react-refresh lint findings the
  moved files already had, and the larger dedupes (MicButton, ChatRenderer vs MarkdownRenderer, file viewer).

## A11. Migration worst cases, multi-instance verdict, top risks

**Migration worst cases (always against `cp -a ~/.sunlit-learning <fixture>`; source flag → the copy, `--docs-dir` → a
scratch root; the original is never touched):**
- Target `_users/default/Chats/SparkQuill/` non-empty and unmarked → refuse + log + UI notice; test asserts zero writes.
- Slug collisions / invalid slugs → lowercase + numeric suffix via `activitySlugPattern` + `uniqueDir`; rewrite
  `current-activity.json.dir` to the **allocated** slug; test with two `fractions` under different subjects.
- Missing `product.json` → synthesize (else the child gets `project "<slug>" was not found`); invariant test + live handoff.
- Keys left in-folder → `answerKeyDestination` → `keys/`, **plus an idempotent startup key-sweep in `cmd/server`** (protects
  partial migrations and future regressions); Go test under a `$HOME` fixture (none exists in `cmd/server` today —
  port `characterization_test.go:250-263`); live: child `cat ../../keys/…` denied.
- Stale/old-layout `current-activity.json` → rewrite or delete the pointer (client returns null → "no activity").
- `family.json` fields: keep `engine` when it is a known `provider_options` id; drop `fast_mode/selected_models/pulse.*/
  schedule/whatsapp_voice_enabled` with a one-time in-app notice.
- Secrets: never copy `secrets.key`/`secrets.enc.json` into the docs dir; re-enter via the platform store; test asserts
  neither file exists under `<docsDir>`.
- Check-in firing immediately: `Decide`'s cadence branch runs now on zero `LastRun` (`productschedule/schedule.go:178-190`)
  → seed `last_run_at=now` with `enabled`; the quiet rule is inert until P4-ship-first lands.
- Two desktops: voice-model download race (`flock` on `<modelDir>.lock`; SparkQuill delays `warm` until
  `/api/voice/status.installed`), CLI-security store (`AGENTWORKS_CLI_SECURITY_DIR`).

**One app with a product switch vs two apps from one shell codebase:** one app wins on memory (one voice engine, one
server pair), zero collisions, one updater; it loses on everything the family sees — name/icon/tray/menus,
close-to-tray vs quit, light-first theme vs the forced `dark` class, a product switcher visible to a child, and coupled
release cadence. **Verdict: two apps from one shell codebase** (P1 descriptors); revisit after P2b and runtime-config
product pinning exist.

**Top risks (ranked) → mitigation → proof**
1. `AGENT_PRODUCTS`/token gate 400s every turn → never set it in the shell → `claude_code_token_gate_test.go` + shell env
   test + live parent turn.
2. Fresh Mac with no/unauthenticated CLI → silent failure or 20-min hang → P3 onboarding (`providers` + `validate-key` +
   `family.json.engine`) → frontend mapping test with fixture manifests; live with `claude` off PATH shows "Not installed",
   unauthenticated `claude` fails within the validate bound.
3. Answer keys readable by the child after a partial migration → relocation + startup sweep → `$HOME`-fixture sandbox test
   + live denied read.
4. Migration clobbers or double-runs a family already on the platform → marker-only-on-success, refuse non-empty,
   backup-then-abort, copy-not-move → run-twice / non-empty / read-only tests, then the copied-real-data dry run + run
   with a file-count diff.
5. Server refuses to start in the packaged app (empty `AUTH_SECRET` fatal `server.go:1525`, CLI-security `log.Fatalf`
   `:1713-1721`, chat store needs workspace-server first, missing `configs/`) → mirror AgentWorks' startup order and
   settings persistence → live packaged launch on a clean userData: both `DynamicPort:` lines and `/api/health` 200 within 90 s.

---

# Part B — Research record: teaching `family-server` the AgentWorks session protocol (rejected 2026-09-06)

# SparkQuill standalone: teach `family-server` the AgentWorks session protocol (Option B)

## Context

SparkQuill ships as a **standalone consumer desktop app** (own `.dmg`, `install-sparkquill.sh`, own dock/tray; README: "not a fork" of the AgentWorks desktop). Its Electron shell (`desktop-sparkquill/main.js`) spawns `agent_go/cmd/family-server`, a deliberately small Go binary, and on 2026-09-03 the decision was **desktop keeps family-server** (not the full platform).

The user wants **one chat UI to maintain**: SparkQuill must render the *exact* AgentWorks `ChatArea` + `ChatInput` (and its transcript, tool-call rows, etc.), not the bespoke `.fl-msg/.fl-bubble` chat in `frontend/learning-app/src/LearningApp.tsx`.

That frontend already exists: `frontend/learning-app/src/platform/PlatformChat.tsx` / `ChildPlatformChat.tsx` mount the real `ChatArea` (product variant). It is selected when `backend === 'platform'` (`frontend/learning-app/src/api/index.ts`, via `VITE_SPARKQUILL_BACKEND` or `window.sparkquill.backend()` — `desktop-sparkquill/preload.js` does not expose `backend()` today, so the desktop always falls to `standalone`). It does **not** run against family-server because `ChatArea` is wired to the AgentWorks server protocol (agent-profile conversations, session event polling/SSE, session status/live-input, chat-history, `/api/wp` workspace documents, product schedule jobs, bearer-token login), none of which family-server serves.

Two options were weighed:
- **Spawn the real `cmd/server` (+ separate `workspace-server`) from the SparkQuill shell.** Technically possible (same Go module; `desktop/main.js` already shows the spawn pattern), but `StreamingAPI` (`agent_go/cmd/server/server.go:333`, ~200 fields, no exported constructor) drags the entire platform surface (cost ledger, Gmail, notifications, multi-user auth, tmux reapers) into a family's install — the opposite of why family-server exists. Rejected for a standalone product.
- **Teach family-server the protocol** — chosen. Feasible because the risky pieces are already shared: turns run via the same `mcpagent`/`internal/agentsession` engine, tool calls already use the canonical `events.ToolCallRecord` wire type, and family-server already imports `pkg/productschedule` (the identical scheduling engine behind the main server's product schedules).

Outcome: the desktop app keeps its small backend, and `PlatformChat` becomes THE SparkQuill chat; the bubble chat in `LearningApp.tsx` is deleted once the new path is verified live.

## Established facts (from exploration)

**family-server today** (`agent_go/cmd/family-server`):
- Turn engine: `runParentTurn` `chat.go:587`, `runChildTurn` `child.go`, seam `newAgentSession` `turn_session.go:23` (`agentsession.Config`, `internal/agentsession/agentsession.go:65`); one global `agentTurnMu` `chat.go:446`; steering via `steer.go`.
- Streaming: bespoke fan-out SSE hub `status_stream.go:36` emitting only `status|delta|tool_call`, **no history/cursor** (a late subscriber misses everything; final reply returns in the POST body).
- Tool calls: `toolCallCollector` `tool_call_events.go:14` (mcpagent `AgentEventListener`, `DirectToolExecutionEvents:true`), type `events.ToolCallRecord` (mcpagent) — same as main server.
- Storage: JSON files. Parent = `~/.sunlit-learning/workspace/conversations/parent.json` (single canonical thread, `conversation_store.go:32`); child = `<activityDir>/conversation.json`. Message type `enginedetect.ChatMessage` (`internal/enginedetect/chat.go:19`). Family state `~/.sunlit-learning/family.json` (`state.go:52`: engine, child, pin_hash, parent_label, pulse{…, watch_sites}, schedule…). Activities = `Subject/Topic/<slug>/activity.json` at workspace top level (`activity.go:57-91`), pointer `current-activity.json`.
- Workspace HTTP: its own `/api/workspace/tree|file|raw|state` + `/api/upload` (`workspace.go`, `upload.go`) — a different protocol from `/api/wp/*`.
- Pulse check-in: `pulseRunner = productschedule.NewRunner(...)` `pulse.go:317`, config in `family.json` (`PulseConfig` `state.go:138`), routes `/api/pulse/run|status|config`.
- Router: plain `http.ServeMux`, no path variables, no auth; CORS allow-list hardcoded to `:5174` (`main.go:19-22`). Optional static SPA via `FAMILY_WEB_DIR`.
- Imports already: `internal/agentsession`, `internal/enginedetect`, `internal/sparkquillproduct` (only `SkillFiles`), `pkg/productschedule`, `pkg/voicestt`, whatsapp pkgs. **Not** imported: `pkg/agentprofiles`, `internal/events`, `pkg/chathistory`, `pkg/schedulerstate`, `pkg/workspace`.

**Main server** (`agent_go/cmd/server`, importable `package server`, same module):
- Agent profiles: `AgentProfileRoutes(router, registry)` `agent_profile_routes.go:69` (GET list/validate/`{id}`) — reusable with `pkg/agentprofiles.Registry` + `internal/sparkquillproduct.BuiltinAgentProfiles()/RegisterProductSkills()/RegisterAgentProfileRuntime(...)` (`server.go:1758-1771`). `/query`, `/conversation`, `/conversation/new` are `*StreamingAPI` methods → not reusable; `/query` builds a `QueryRequest` (`AgentMode:"multi-agent"`, `SelectedFolder: conversation.WorkspacePath`, `X-Session-ID`) and calls `handleQuery`.
- Sessions: `GET /api/sessions/{id}/events` (`polling.go:119`), `/events/stream` SSE (`sse.go:48`), `/status` (`polling.go:606`), `POST /live-input` (`server.go:8614`). Backed by `internal/events.EventStore` (`events.NewEventStore(max)`, importable) + `StreamingAPI` maps (not reusable). Frontend: `frontend/shared/session/events.ts:19` (`?since=`), `sse.ts:67`, `platformApi.ts:189,191`.
- Chat history: `GET /api/chat-history/sessions/{id}` → `server.ReadChatHistoryConversation(userID, sessionID, workspacePath)` `chat_history_persistence.go:1977` (exported, importable).
- `/api/wp/*` = reverse proxy (`workspace_proxy.go:15`) to the **separate module** `/Users/mipl/ai-work/mcp-agent-builder-go/workspace` (package main, spawned by `desktop/main.js:1108` as `workspace-server`). Not importable; family-server must serve the same document/upload contract itself.
- Scheduler: `SchedulerRoutes` → product job id `product:<profile>:<schedule>` (`product_schedules.go:83`) → `ProductScheduleService` (needs `*StreamingAPI`). Frontend uses `CHECKIN_JOB_ID = product:sparkquill:pulse` (`platformApi.ts:233`): GET → `{enabled,last_run_at}`, POST enable/disable/trigger.
- Auth: `POST /api/auth/login` single-user path ignores body, returns a JWT for the default user (`user_auth_routes.go:223-238`); `GetUserIDFromContext` falls back to `GetDefaultUserID()`.

**Frontend platform mode** (`frontend/learning-app/src/api/platformApi.ts`, `api/platform/workspace.ts`): `FAMILY_ROOT='Chats/SparkQuill'`, `family.json` at `Chats/SparkQuill/family.json` (same sha256 PIN hashing — deliberate compat), activities flat under `Chats/SparkQuill/activities/<slug>/`, state blobs `state/<key>.json`, uploads → `inbox`. Only three FamilyApi methods are still `notYet(...)`: WhatsApp status/pair/unpair.

## Reuse seams confirmed (this is what makes Option B tractable)

- **Event bridge is importable and already the right shape.** `agent_go/internal/events/event_observer.go:21/30` — `events.NewEventObserver(store, sessionID)` / `NewEventObserverWithLogger(...)` implements `mcpagent.AgentEventListener` by wrapping each `*mcpagent/events.AgentEvent` verbatim into `events.Event` and calling `store.AddEvent`. This is exactly what the main server's chat path does (`cmd/server/server.go:6014,6047`). `EventStore.AddEvent` (`event_store.go:669→257,383-385`) auto-stamps `ExecutionID="main:"+sid`, `ParentExecutionID="session:"+sid`, `ExecutionKind="main_agent"` — precisely what `working_set=session` filtering keeps — and assigns `Sequence`. Store API: `NewEventStore(max)` `:575`, `GetEvents(sid, GetEventsOptions{SinceIndex,Limit,Offset})` `:918`, `Subscribe/Unsubscribe` `:643/:655`, `SetEventAddedCallback` `:634`, `InitializeSession` `:869`, `GetSessionStatus` `:1190`.
- **family-server's hook already exists**: `agentsession.Config.Observers []mcpagent.AgentEventListener` (`internal/agentsession/agentsession.go:117`, passed unfiltered at `:288-290`). Appending one `EventObserver` next to the existing `toolCallCollector` (`chat.go:668-669`, `child.go`) yields the FULL canonical stream for coding-CLI providers (streaming is auto-forced on for them: `mcpagent/agent/agent.go:2062-2178`): `agent_start`, `user_message`, `conversation_start`, `llm_generation_start/end`, `conversation_thinking`, `streaming_chunk`, `tool_call_start/end/error`, `status_line`, `token_usage`, `unified_completion`, `agent_end`. **No event synthesis needed.**
- **One dedup decision**: `DirectToolExecutionEvents:true` makes the bridge executor emit a second, authoritative `tool_call_*` pair (`ServerName=="direct_execution"`, real args/result/duration) alongside the CLI-transcript pair; `toolCallCollector` today keeps only `direct_execution`. An `EventObserver` stores both → either mirror whatever `cmd/server` does for its product chat path, or wrap the observer to drop transcript-side `tool_call_*`. (Verify main server's setting at implementation.)
- **Stable session ids exist**: parent = `parentConversationID = "parent"` (`conversation_store.go:32`; Pulse/WhatsApp already use it), child = `currentActivityDir()` (`activity.go:185`). These are already what `Config.SessionID` uses (`chat.go:657`, `child.go:253`) so event-store keys == CLI-resume keys for free. `/conversation` response shape to mirror: `{conversation_id, conversation_key, session_id}` (`cmd/server/agent_profile_routes.go:306-310`).
- **Router**: `github.com/gorilla/mux v1.8.1` is already a direct dep (`agent_go/go.mod:17`); module is `go 1.26` so stdlib `ServeMux` patterns (`"GET /api/sessions/{id}/events"`, `r.PathValue`) also work with zero new imports and no churn on the ~50 existing fixed routes.
- **Workspace chokepoint**: `resolveWorkspacePath(rel)` (`materials.go:13`) is the single path-safety gate every handler uses; `treeNode{name,path,type,children,size}` (`workspace.go:12-25`), `handleWorkspaceFile` `{path,is_text,content|size}`, `handleUpload` (`upload.go:53`, fields `file`,`scope`; parent→`inbox/<safeName>`), `handleWorkspaceRaw` rewrites relative asset URLs to `/api/workspace/raw?path=` (`conversation_store.go:344`) — a `/api/wp` raw shim must NOT apply that query-string rewrite.
- **CORS gaps for the platform client**: `allowedOrigins` hardcoded to `:5174` (`main.go:19-22`); `Allow-Headers: Content-Type` only (no `Authorization`), no `PUT`. Irrelevant when the SPA is served same-origin via `FAMILY_WEB_DIR` (the desktop case), needed for Vite dev.
- **Tests**: `cmd/family-server/characterization_test.go:27-92` — `fakeSession` implements `turnSession`, `installFakeSession(t, reply, deltas...)` swaps the `newAgentSession` seam, `setupFamily(t)` uses `FAMILY_DATA_DIR=t.TempDir()`, `postJSON`; full-turn tests `TestParentTurnPromptToolsAndPersistence :114`, `TestParentTurnStreamsDeltasToStatusSubscribers :292`. `tool_call_events_test.go:11` feeds synthetic `AgentEvent`s into a listener — template for asserting `EventStore.GetEvents` output. **Acceptance test already exists**: `frontend/learning-app/src/api/platformApi.live.test.ts`, gated by `SPARKQUILL_PLATFORM_URL`, drives `createPlatformApi` end-to-end (conversation → query → events?since&working_set=session + SSE → status → live-input → chat-history → `/api/wp` tree/upload/raw). It asserts: tree has node `family.json`; upload returns `path==='inbox/live-test.txt'`; `rawUrl` contains `/api/wp/api/documents/Chats/SparkQuill/inbox/live-test.txt/raw?token=`; `week(0).days.length===7`; monotonic `last_processed_index` (SSE baseline before query, `platformApi.ts:144-162`).

## Frontend contract in platform mode (what family-server must serve)

Two clients hit the same origin: SparkQuill's `platformApi.request()` (`Authorization: Bearer`) and AgentWorks' axios `agentApi`/`workspaceApi` (`Authorization: Bearer` from `localStorage['auth_token']`, plus `X-Session-ID` on every request; `workspaceApi` adds `X-User-ID` decoded from the JWT's `user_id`/`sub` — `src/services/api.ts:605-634`). `platformApi.ensureSession()` writes the token to both keys before `ChatArea` mounts.

**Fatal if missing (chat never renders — `PlatformChat.tsx:151-153`):**
- `POST /api/auth/login` (body-less) → `{token}` non-empty.
- `POST /api/agent-profiles/{id}/conversation` (`{}` parent / `{conversation_key}` child; header `X-Session-ID` when a tab exists) → `{conversation_id, conversation_key, session_id}`.
- `GET /api/sessions/{id}/events?working_set=session&limit=300&offset=0` (initial hydrate, fires twice) → `{events[], session_status, last_processed_index, has_more}`; 404 tolerated only if chat-history returned something.
- `GET /api/chat-history/sessions/{id}?resume_turns=100&include_ui_events=1&workspace_path=Chats/SparkQuill` → 2xx `{session_id, conversation_history: [...]}` **or 404**; any other error is fatal.
- `POST /api/agent-profiles/{id}/query` (`{message, conversation_key?}`, header `X-Session-ID`) → `{status:"started"|"workflow_started"|"live_input_delivered", session_id, query_id, message?, delivery_status?}`; any other `status` renders an inline error.
- Live stream: `GET /api/sessions/{id}/events?working_set=session&since=N` polled every 750 ms while a turn runs (also 500 ms SSE-fallback), and SSE `GET /api/sessions/{id}/events/stream?working_set=session&since=N&token=<jwt>` (EventSource, `withCredentials`). SSE frames: named `event` / `status` (unnamed ⇒ `event`); `id:` = **store index** used as resume cursor (`Last-Event-ID` must be honored like `?since=`); `event` data `{events, session_status?, display_status?, last_processed_index, has_running_background_agents?, is_synthetic_turn?, can_steer?, runtime_state?}`; `status` data same minus events; `:` comment lines OK as keep-alive.
- Cursor/status sentinels: `last_processed_index:-1` means "session unknown" (client resets cursor / follower gives up) — never send it for a healthy empty session; `session_status ∈ running|completed|error|stopped|inactive`; `completed|error` with `has_running_background_agents:false` stops the catch-up loop — a turn that never reaches a terminal status leaves the client polling forever. Turn-finished event = `unified_completion` (`data.data.final_result`/`status`) or `agent_end`/`conversation_end`/`conversation_error`/`agent_error`.
- `PollingEvent` fields read: `id,type,timestamp,session_id,event_index` and `data.data.{content,tool_name,tool_call_id,tool_params.arguments,result,error,duration,final_result,status,chunk_index,is_delta,is_tool_call,source,kind,payload,metadata.assistant_turn_text}`.

**Optional but noisy (stub cheaply):** `GET /api/header-summary` every 60 s (+ after each submit) → `{active_sessions:[],total:0,schedule_summary:{...zeros}}`; `GET /api/llm-config/providers` on chat mount → `{providers:[]}`; `GET /api/agent-profiles/{id}` (+`?version=N`) → declaration (`tools[].interaction/presentation`, `commands[]`, `runtime.provider_options[]`, `runtime.capabilities`, `schedules[]`) — used for suggestion pills, quick commands, engines list, mic gate, check-in cadence; `/api/commands`, `/api/skills` on demand.

**Session control (needed for Stop / steering):** `POST /api/session/cancel-turn` + `POST /api/session/stop?cancelAgents=true` (header `X-Session-ID`), `POST /api/sessions/{id}/dismiss`, `GET /api/sessions/{id}/status` (`{status, agent_mode, last_activity, can_steer}`), `POST /api/sessions/{id}/live-input` (`{message}` → `{delivery_status: sent_to_cli|queued_for_injection|next_turn_started, message_id, success}`; only used while a turn streams — failure falls back to `/query`).

**Workspace (`/api/wp`, prefix `Chats/SparkQuill/`, segments percent-encoded, `/` kept):** `GET /api/wp/api/documents/{path}` → `{success, data:{filepath,content,is_binary,size}, error}`; `PUT` same path `{content}`; `GET /api/wp/api/documents?folder=<enc>&max_depth=-1|1|2` → `{success, data:[{filepath,type:"folder"|"file",size,children[]}]}`; `POST /api/wp/api/upload` multipart `file`, `folder_path`, `commit_message?` → `filepath|data.filepath|data.file_path`; `GET /api/wp/api/documents/{path}/raw?[download=true&]token=<jwt>` raw bytes with `Range` support (`<img>/<video>/<audio>/<iframe>` — header auth impossible). No DELETE issued.

**Settings-only:** `/api/voice/status` (path already matches family-server; platform reads `available,installed,downloading,loading,ready,got_bytes,total_bytes,size_mb`), `/api/voice/warm`, WS `/api/voice/stream`; `/api/secrets/stored|encrypt|store|store/{name}`; `/api/scheduler/jobs/product:sparkquill:pulse` GET (`{enabled,last_run_at}`) + `/enable|/disable|/trigger` (id percent-encoded as `product%3Asparkquill%3Apulse`).

**Not called on the product surface:** `/api/terminals`, main-terminal WS, `/api/capabilities`, `/api/secrets/decrypt`, ui-control.

**Legacy duplicate traffic (frontend cleanup item):** `LearningApp.tsx` gates the *child* legacy `FamilyApi` effects on `backend==='platform'` (1768,1810,1933) but not the parent ones — `loadParentConversation` on mount + every 20 s (`:1144,:1685`), `watchParent` (a second SSE stream via fetch transport, `:1733`), `steerParent` (`:2211`) still fire. Non-fatal, but gate them the same way (or delete with the bubble chat).

**CORS (Vite dev only; desktop serves same-origin via `FAMILY_WEB_DIR`):** echo `Origin` `http://127.0.0.1:5174`, `Allow-Credentials: true`, headers `Authorization, Content-Type, X-Session-ID, X-User-ID, Accept`, methods `GET, POST, PUT, DELETE, OPTIONS`.

**Tool-call dedup — resolved: mirror the main server.** `pkg/agentwrapper/llm_agent.go:151` sets `DirectToolExecutionEvents: true` on the main chat path too, and the CLI-transcript pair carries the same `chunk.ToolCallID` (`mcpagent/agent/llm_generation.go:690-760`), so the transcript's `pairToolCalls` merges both pairs by id. Family-server stores both (no wrapper); `toolCallCollector` stays as-is for its own POST response.

## Main-server wire contracts to mirror (source of truth = these files; read them when implementing)

- **Events poll** `GET /api/sessions/{id}/events` — `cmd/server/polling.go:119`. Params: `since` (poll mode) **or** `limit`(>0)/`offset`(≥0) (page mode); neither → `400`; `working_set=session` → `events.FilterSessionWorkingSet`. Envelope `GetEventsResponse` `polling.go:96` (`events, has_more, session_id, session_status, display_status, last_processed_index, has_running_background_agents, is_synthetic_turn, can_steer, runtime_state`). Unknown session → `200 {events:[], has_more:false, last_processed_index:-1}` (`:214`). Element = `internal/events.Event` (`event_store.go:200`) with **custom `MarshalJSON` `:217`** (always `id,type,timestamp,session_id`; conditionally `error,execution_id,parent_execution_id,execution_kind,terminal_owner_id,terminal_id,sequence>0,data`); `data` = `*mcpagent/events.AgentEvent`, typed payload flat at `data.data` (+ `BaseEventData` fields incl. `metadata`). `GetEvents` already applies `NEVER_SHOW_EVENTS`/`HIDDEN_EVENTS` (`event_store.go:18,59`: streaming_*, llm_generation_start, agent_start, system_prompt, conversation_start/turn hidden from polling; user_message, tool_call_*, llm_generation_end, agent_end, unified_completion, token_usage kept), `InitialEventsLimit=300` + `STRUCTURAL_EVENTS`, cap 1000. `Sequence` assigned in `AddEvent` `:683`.
- **SSE** `GET /api/sessions/{id}/events/stream` — `cmd/server/sse.go:48`. `since` or `Last-Event-ID` (non-int → 400); `?token=` auth; headers `text/event-stream`, `no-cache`, `keep-alive`, `X-Accel-Buffering: no`. `writeSSEEvent` `:285`: `id: <n>` (only n≥0) / `event: <name>` / `data: <json>`. Names: `event` (`sseEventMessage` `:16`, backfill N events with `IncludeStreaming:true` `:112`, then one event per frame; **no `session_id` field**), `status` every 2 s (`sseStatusMessage` `:29`, no `id:`), `cursor` (`data: {}` + `id:` when working_set filtered everything, `:118,:221`). Heartbeat = comment `: heartbeat <ts>` every 15 s (`:273`). Cold/unknown session → one `event` frame, no id, `last_processed_index:-1` (`:162-172`). Stream never self-terminates.
- **Status** `GET /api/sessions/{id}/status` — `polling.go:605`. **404 for unknown session** (unlike events). Hand-built map keys: `agent_mode, can_steer, created_at, display_status(busy|idle|stopped), has_retained_tmux_session, last_activity, query, runtime_state, session_id, status(running|completed|error|stopped|inactive)`. `RuntimeSnapshot` `runtime_coordinator.go:58` (+ `RuntimeForegroundSnapshot{busy,has_cancel,can_steer,synthetic}`, phases `starting|running|waiting|idle|completed|failed|canceled`).
- **Live input** `POST /api/sessions/{id}/live-input` — `server.go:8614`; `LiveInputRequest{message}` `:1387`; `LiveInputResponse{success,message,delivery_status,provider,message_id,query_id}` `:1391`; statuses `sent_to_cli | queued_for_injection | next_turn_started`; `message_id="steer-message-<nanos>"`; 409 plain text when no live turn. Frontend unblocks composer only on `sent_to_cli|next_turn_started`.
- **Agent profile** `GET /api/agent-profiles/{id}[?version=N]` — `agent_profile_routes.go:426`; body = `agentprofiles.Profile` (`pkg/agentprofiles/types.go:299`) marshalled directly (tools[].presentation/interaction, commands[], runtime.provider_options[], runtime.capabilities, schedules[] = `productschedule.Schedule`); errors `{"error":...}` JSON; `OPTIONS`→204. Reusable via `server.AgentProfileRoutes(router, registry)` (`:69`).
- **Conversation** `POST /api/agent-profiles/{id}/conversation[/new]` — `:275/:315`; strict decode (`DisallowUnknownFields`, ≤2 MiB) of `{conversation_key?}`; response exactly `{conversation_id, conversation_key, session_id}` (`:33`); honors `X-Session-ID` hint (`:359-374`).
- **Query** `POST /api/agent-profiles/{id}/query` — `:200`; strict `{message, conversation_key?}` (extra keys → 400); overwrites `X-Session-ID` with the resolved session; returns `handleQuery`'s JSON ack `QueryResponse` `server.go:1257`: `{query_id:"query_<nanos>", session_id, status:"started", message:"Query processing started. Use polling API to get real-time updates."}`. Result arrives via events/SSE.
- **Chat history** `GET /api/chat-history/sessions/{id}?workspace_path&resume_turns&resume_offset&include_ui_events=1` — `chat_history_routes.go:462`; 404 plain `"Session not found"`; 200 = **raw on-disk `conversation.json` bytes** (`:507`) built at `chat_history_persistence.go:151-179`: `{session_id, agent_mode, conversation_history:[llmtypes.MessageContent → {"Role":"human"|"ai"|"tool","Parts":[{"Text":…}|ToolCall|ToolCallResponse]} (no json tags → capitalized; reader accepts either casing `:716`)], updated_at, runtime?, ui_events?:[internal/events.Event]}`. `resume_turns=N` projection (`:589`) strips ui_events/terminal_snapshots, keeps last N user-anchored turns with `resume_order`, adds `history_source_message_count` + `history_pagination{has_more,next_offset,start_turn,total_turns}`; `include_ui_events=1` re-attaches filtered `ui_events`.
- **Workspace (`/api/wp` → module `workspace`, gin)**: `handlers/documents.go`. Envelope `APIResponse[T]{success,message,data?,error?}` (`models/document.go:31`), `Document{filepath,content?,type?,children?,is_image?,is_binary?,size?,mime_type?,encoding?,last_modified?}` (`:6`). GET single (`:787`): text → `encoding:"utf-8"`; image → `data-url`; binary → `content:""`,`is_binary:true`; **missing file → `200 {success:true,message:"File does not exist",data:{filepath:""},error:"File not found: …"}`**; traversal → 400. Listing (`:402`) `?folder&max_depth` (limit/offset ignored) → full hierarchical tree, folders `type:"folder"` + `children`, no content; missing folder → 200 `data:[]`. PUT (`:990`) `{content}` creates dirs/file → `data:{filepath}`. DELETE needs `?confirm=true`. Upload (`:1927`) multipart `file` + `folder_path` (10 MiB, allow-list) → `data:FileUploadResponse{filepath,filename,file_size,content_type,folder}`. `/raw` → bytes (family-server: use `http.ServeContent` for Range).
- **Product schedule** `/api/scheduler/jobs/product:sparkquill:pulse` — `scheduler_routes.go:1103` `handleProductScheduleJob` (id parsed on last `:`). GET/enable/disable → `ScheduledJobResponse` (`:21`; frontend reads only `enabled`, `last_run_at` `*time.Time`); trigger → `{session_id}`; already running → **409 plain text** `productschedule: a run is already in progress` (`pkg/productschedule/runner.go:25`). Persisted state shape `productScheduleUserState` (`product_schedules.go:40`).
- **Login** `POST /api/auth/login` — `user_auth_routes.go:212`; single-user branch `:222-238` ignores body → `AuthResponse{token, user:UserInfo{id,username,…,can_*}}` (`:27,:33`). **Token must be a real HS256 JWT** (`GenerateJWTWithProvider` `auth_middleware.go:300`, key = `AUTH_SECRET`, claims `UserClaims{user_id,username,email?,provider?,scope?,scope_workspace?}` + `sub=userID, iss="mcp-agent-builder", iat/nbf/exp(+7d)`); `workspaceApi` decodes `user_id`/`sub` client-side. Middleware (`:162`) accepts `Authorization: Bearer` or `?token=`; 401 bodies `{"error":"Invalid or expired token"}` / `"Session expired: …"` — frontend axios drops the token only when the 401 body contains "expired"/"invalid".
- **Turn event order** (mcpagent `agent/conversation.go`): `agent_start`(hidden) → `user_message` → `conversation_start`,`system_prompt`(hidden) → per LLM call `llm_generation_start`(hidden), `streaming_start/chunk/end`(SSE only), `conversation_thinking` → `llm_generation_end`(visible) → per tool `tool_call_start` → `tool_call_end|tool_call_error` → `conversation_turn`(hidden) → `token_usage` → **`unified_completion`** (terminal, structural; `data.data.{final_result,status,question,duration,turns,error}`) → `agent_end`. Frontend "turn finished" = `llm_generation_end|unified_completion|agent_end|conversation_end|conversation_error|context_cancelled` (`ChatArea.tsx:1703`); transcript answer types `unified_completion|agent_end|orchestrator_agent_end|background_agent_completed` reading `content||final_result||result`; an empty successful `unified_completion` is dropped. Composer busy/idle comes from `display_status`/`runtime_state.foreground_turn`, not events.

**Import vs copy decision:** family-server is in the `agent_go` module, so import `internal/events` (store/observer — needed for sequencing + identical `MarshalJSON`), `pkg/agentprofiles`, `internal/sparkquillproduct`, and `mcpagent/events`. For the thin wire structs (`GetEventsResponse`, SSE messages, `RuntimeSnapshot` subset, `LiveInput*`, `AgentProfile*`, `QueryResponse`, `AuthResponse/UserInfo/UserClaims`, `ScheduledJobResponse` subset) prefer **copying into `cmd/family-server/platform_wire.go`** with `// mirrors cmd/server/<file>:<line>` comments rather than linking all of `package server` into the desktop binary (binary size + package-level init side effects). Guard drift with a `platform_wire_sync_test.go` that imports `package server` **only in tests** and asserts json-tag parity via reflection. Exception: `server.AgentProfileRoutes` / `server.GenerateJWT` may be imported if their transitive init cost proves acceptable (measure binary size; decide in Phase 0).

## Known mismatches to reconcile (workspace layer)
1. Transport/envelope: `/api/wp/api/documents` `{success,data:{filepath,type,content,size,is_binary,is_image,children}}` vs family-server `treeNode{name,path,type,children,size}`.
2. Root prefix `Chats/SparkQuill/` (frontend) vs bare workspace root (family-server); `familyRelative()` tolerates absence.
3. `family.json` location (`~/.sunlit-learning/family.json` vs `<workspace>/Chats/SparkQuill/family.json`) and `watch_sites` nesting (`pulse.watch_sites` vs top-level).
4. Activity layout: `Subject/Topic/<slug>/` vs flat `activities/<slug>/`.

## Design decisions (resolved; call out at approval)

1. **Router**: Go 1.22+ `http.ServeMux` patterns (`"GET /api/sessions/{id}/events"`, `r.PathValue`, `{path...}` wildcard). Zero churn on the ~50 existing fixed routes; percent-encoded ids (`product%3Asparkquill%3Apulse`) unescape via `PathValue`. gorilla/mux not adopted.
2. **No `import "…/cmd/server"` in the binary.** Reusable handlers are 20–60 lines; importing `package server` drags the platform dependency graph + `init()` side effects into a 93 MB consumer binary. Import only leaves: `internal/events`, `pkg/agentprofiles`, `internal/sparkquillproduct`, `pkg/orchestrator/events`, `pkg/productschedule`, `github.com/golang-jwt/jwt/v5` (already `go.mod:12`). Wire structs are copied into `cmd/family-server/platform_wire.go` with `// mirrors cmd/server/<file>:<line>` comments; a **test-only** import of `package server` in `platform_wire_sync_test.go` asserts json-tag parity by reflection (drift guard without linking it into the app).
3. **Session ids**: parent = `"parent"` (unchanged). Child platform sid = **`child:<slug>`** — the shared client interpolates the raw sid into `/api/sessions/${sessionId}/…` (`frontend/src/services/api.ts:713,986,1292`, verified), so a slash-containing `currentActivityDir()` can never route. `agentsession.Config.SessionID` stays the real activity dir (CLI-resume key unchanged); the `EventObserver` is keyed by the platform sid (independent of `Config.SessionID`, `event_observer.go:21`). `/conversation` resolves slug → activity dir; 422 if unknown.
4. **`/query` semantics**: mark `session_status="running"` **synchronously in the handler before the ack** (verified: `ChatArea.tsx:2220-2224` stops the 750 ms loop on `completed|error|stopped|inactive` with no bg agents — if the turn goroutine is still waiting on `agentTurnMu` (Pulse can hold it minutes) and status still reads `completed`, the UI goes silent). Goroutine moves it to `completed|error`. Ack `{status:"started", session_id, query_id}`.
5. **Second `/query` while a turn runs**: try `trySteer` (`steer.go:77`) → `{status:"live_input_delivered", delivery_status:"sent_to_cli"}`; else accept as `started` and let a per-session `turnMu` queue it. Never 409.
6. **Pre-agent failure** (engine unset, `newAgentSession` error): no agent event exists, so store one `agent_error` event with the friendly message so the transcript shows something; status → `error`.
7. **Auth**: family-server mints its own HS256 JWT (per-install random secret at `~/.sunlit-learning/auth.secret`, 0600; claims `sub`+`user_id`="default", `username`="user", `iss="mcp-agent-builder"`, 7 d). `server.GenerateJWT` is not usable (hard-requires `AUTH_SECRET`, `auth_middleware.go:295-302`). Validation **enforced** on the platform prefixes only (`/api/sessions/`, `/api/agent-profiles/`, `/api/chat-history/`, `/api/wp/`, `/api/secrets/`, `/api/scheduler/`, `/api/header-summary`, `/api/llm-config/`, `/api/commands`, `/api/skills`), header or `?token=`; 401 body `{"error":"Invalid or expired token"}`. Existing standalone routes stay open during transition.
8. **Chat history is served, not 404'd.** `hydrateTabEvents` prefers durable history and the event store is memory-only → a 404 means an empty chat after every desktop relaunch (verified `sessionRestore.ts:344-368`). Map `storedConversation.Messages` → `conversation_history[]` (~40 lines); double hydrate is deduped client-side.
9. **Product events come from the tools, not synthesis.** Suggestion pills / celebrate / scene / viewer-open in `PlatformChat`/`ChildPlatformChat` are driven by `product_interaction` / `presentation_updated` events. Emit them from family-server's existing tool sinks (`chat.go:670-697`, `child.go:154-210`) using `pkg/orchestrator/events.ProductInteractionEvent{Product:"sparkquill",Kind,Payload}` / `PresentationUpdatedEvent` (`pkg/orchestrator/events/data.go:207-238`), wrapped as `emitAgentProfileEvent` does (`cmd/server/agent_profile_runtime.go:389-398`). Kinds come from `product.yaml` (`suggestions`, `celebrate`, `scene`, `document.file`, `sparkquill.activity`) which the client reads back from `GET /api/agent-profiles/{id}` — consistent by construction.
10. **SSE `id:`** = `Sequence-1` (exact store index; `Data.EventIndex` is never set anywhere, so the main server's `lastIndex+1` scheme drifts on hidden events). Use a large `maxEvents` so no pruning; fall back to the main-server scheme only if pruning is enabled.
11. **Frontend adapter**: add `createFamilyServerApi()` = `createPlatformApi` for chat/workspace **+ family-server-native routes for settings** (`/api/setup|engines|engine/selection|engines/validate|models|fast-mode|whatsapp/*`, `/api/pulse/config`). Keep `createPlatformApi` pure so a future platform-hosted SparkQuill still works. Reason (verified): platform `setup()` never returns `next_step:"engine"` and `selectEngine` is a no-op (`platformApi.ts:254-266,290-291`), while family-server refuses turns without `s.Engine` (`chat.go:545`).
12. **Mode switch**: `desktop-sparkquill/preload.js` exposes `backend: () => 'platform'` (runtime; `runtimeConfig.ts`/`api/index.ts` already read it) — no `VITE_SPARKQUILL_BACKEND` build flag needed; `dev-setup.sh` unchanged. Bubble chat deleted only after live verification (Phase 7).
13. **`family.json` writes via `/api/wp`** (incl. `pin_hash`, same sha256-hex as `handleSetPin`) are accepted — that is how platform mode already works; server never materialises a physical `workspace/family.json`.
14. **Acceptance test fix first**: `platformApi.live.test.ts:60-61` calls `api.week(0)` but `FamilyApi` has no `week` (verified; removed in `083474b88`) — it throws before the `rawUrl`/raw assertions run. Drop lines 60-61 in Phase 0 (`/api/week` not needed). Run vitest **from `frontend/`** (that's where it's installed, `frontend/package.json:85`).
15. **Turn context**: `/query` runs the turn under `context.Background()` + `turnTimeout`, never `r.Context()` — `handleParentMessage` derives the turn ctx from the request (`chat.go:564`), and an async ack would cancel it.
16. **"Known session" is derived from conversation identity** (`parent` / a real activity dir), never from `EventStore` — `cleanupInactiveSessions` deletes zero-event sessions on a ticker (`event_store.go:1241-1255`), so an initialized-but-empty session flips back to unknown. Unknown → **404** on events/status/SSE; known-but-empty → `200 {events:[], session_status:"completed", last_processed_index:0}` in `since` mode (never `-1` for a known session). Statuses are sticky until the next `/query`; a per-session status map set in a `defer` guarantees every turn reaches `completed|error|stopped`.
17. **Mode switch naming + kill switch**: the new frontend adapter is a `desktop` backend (hybrid: platform protocol for chat + workspace docs, existing standalone routes for settings/WhatsApp/PIN verify/pulse config). `preload.js` exposes `backend: () => process.env.SPARKQUILL_UI ?? 'desktop'` with `main.js` forwarding the env, giving families a `SPARKQUILL_UI=legacy` escape hatch for one release; the switch is deleted together with the bubble chat.

## Plan (phased; Phase 0 is a go/no-go gate)

All new Go files under `agent_go/cmd/family-server/` unless noted. Reuse targets in parentheses.

### Phase 0 — spike (1 day, gate)
- Add `event_store.go`: `var platformEvents = events.NewEventStore(20000)`; `platformObservers(sid string, extra ...mcpagent.AgentEventListener) []mcpagent.AgentEventListener` appending `events.NewEventObserverWithLogger(platformEvents, sid, logger)` (`internal/events/event_observer.go:30`, `event_store.go:575`).
- Modify `chat.go:668` → `Observers: platformObservers("parent", toolCalls)`; temporary `GET /api/sessions/{id}/events` handler.
- Run one real claude-code and one codex-cli parent turn; dump `GetEvents(working_set=session)`; confirm `user_message`, `streaming_chunk` (subscriber-only), both `tool_call_*` pairs sharing `tool_call_id`, `llm_generation_end.metadata.assistant_turn_text`, `unified_completion`/`agent_end`. Measure binary size with leaf imports only. Fix the live test's `api.week`.
- Exit criterion: real events, no synthesis, no `cmd/server` import needed. If a provider path emits no `unified_completion`, Phase 1's status handling covers it.

### Phase 1 — session core (3–4 days)
- `platform_routes.go`: `registerPlatformRoutes(mux)` — the single place the contract is visible.
- `session_registry.go`: `platformSession{Scope, ConversationID, SessionID, Status, AgentMode, CreatedAt, LastActivity, Query, QueryID; cancel; turnMu}`; `sessionFor(profileID, key)` (parent → `"parent"`; child → slug via `listActivities()`/`currentActivityDir()`, `activity.go:123,185`); `setStatus`; `canSteer(sid)` from `activeTurns` (`steer.go:34-37`).
- `turn_runner.go`: `startPlatformTurn(ps, message) queryID` — set `running`, goroutine: `ps.turnMu.Lock()`, ctx with timeout stored for cancel, `messages := append(loadStoredConversation(...).Messages, user msg)`, call **existing** `runParentTurn(ctx, s, "parent", messages, "")` / `runChildTurn(ctx, s, dir, messages)` (`chat.go:587`, `child.go`) — `agentTurnMu`, steer registry, persistence, session handles, Pulse/WhatsApp callers all untouched because they live inside those functions — then `setStatus(completed|error)`.
- Modify `chat.go:668`, `child.go:261`, `pulse.go:481` (`runPulseCheckTurn`) to add the platform observer; move `setStatus(running/completed)` into `runParentTurn`/`runChildTurn` (idempotent) so WhatsApp/Pulse-started turns stream into an open tab.
- Modify tool sinks (`parent_tools.go`, `child.go:154-210`): emit `product_interaction`/`presentation_updated` per decision 9 (`suggest_actions`→`suggestions{actions}`, `set_child_profile/set_parent_label/set_child_schedule`→`family_updated`, `create_learning_activity`→`activity_created{dir,title}`, `celebrate`→`celebrate{stars,reason}`, `show_scene`→`scene{html}`, `open_file`→presentation `document.file{path:"Chats/SparkQuill/"+rel,focus}`, `open_activity`→`sparkquill.activity{dir}`); thread the sid into `parentToolSinks` and the child closure.
- `session_events.go`: `handleSessionEvents` (copy `polling.go:119-261` minus user/bg-agent/runtime branches; keep the `!exists → -1` branch), `handleSessionSSE` (copy `sse.go:48-313`: subscribe-then-backfill, `Last-Event-ID`, 2 s `status`, 15 s heartbeat, `cursor` frames), `handleSessionStatus` (404 unknown; `{session_id,status,agent_mode:"multi-agent",created_at,last_activity,query,can_steer,display_status}`), `handleLiveInput` (`trySteer` → `sent_to_cli` + `appendUserMessageToConversation`, else 409), `handleDismiss`, `handleCancelTurn`/`handleStopSession` (cancel ctx → `stopped`).
- `profile_routes.go`: registry at startup (`agentprofiles.NewRegistry()`, `sparkquillproduct.RegisterProductSkills()`, `BuiltinAgentProfiles()`; **skip** `RegisterAgentProfileRuntime` — needs a workspace API URL and registers platform tool factories family-server doesn't run); `GET /api/agent-profiles`, `GET /api/agent-profiles/{id}` (copy `agent_profile_routes.go:430-452`), `POST …/conversation` (child: slug → real dir via `listActivities()`; on slug collision prefer the current activity, then newest `created_at`, and log), `…/conversation/new` (child rotate = archive `<dir>/conversation.json` + **delete the session handle** (`session_handle_store.go`) + **close that activity's interactive session** — otherwise the warm CLI keyed by `SessionID: activityDir` resumes the old context; `agentsession.CloseAllInteractiveSessions()` exists at `state.go:400`, add a per-id variant — + `platformEvents.RemoveSession`), `…/query`.
- **Pulse**: `runPulseCheckTurn` (`pulse.go:490-500`) builds its session with no `Observers`/`StreamCallback`, so check-ins would emit nothing. Attach the platform observer **wrapped** so the `user_message` content is the parent-facing `c.trigger`, not the raw `c.instruction` (`pulse.go:30-45`); keep the parent session `running` for the whole multi-check run (Pulse holds `agentTurnMu` for minutes, `pulse.go:397-403`).
- **Waiting on `agentTurnMu`**: while `/query`'s goroutine is blocked behind Pulse/WhatsApp, emit a `status_line`/thinking event from `agentTurnBusy()` (`chat.go:519-527`) so the wait is visible instead of a silent `running`.
- **Cancel registry**: store the turn `cancel` alongside the `activeTurns` entry so `cancel-turn`/`stop?cancelAgents=true` cancel the ctx; `runParentTurn` already persists `friendlyTurnError` on ctx errors (`chat.go:724-737`).

### Phase 2 — auth, CORS, stubs (1 day)
- `auth.go`: `loadOrCreateAuthSecret()`, `mintToken()`, `requireToken(next)` on the platform prefixes (decision 7).
- `main.go:19-22,220-236` `withCORS`: `Allow-Credentials: true`; headers `Authorization, Content-Type, X-Session-ID, X-User-ID, Accept, Last-Event-ID`; methods `GET, POST, PUT, DELETE, OPTIONS`; keep allow-list.
- `platform_stubs.go`: `/api/header-summary` `{active_sessions:[],total:0,schedule_summary:{}}`, `/api/llm-config/providers` `{providers:[]}`, `/api/commands` `[]`, `/api/skills` `{skills:[]}`.
- `platform_secrets.go` (`stored`→`[{name}]` from `listSecretNames()`; `encrypt {value}`→`{encrypted}` via `secretsGCM()` `secrets_store.go:52`; `PUT store`; `DELETE store/{name}`), `platform_scheduler.go` (GET → `{id,name,enabled:s.Pulse.Enabled,last_run_at}`; enable/disable → `saveState`; trigger → `pulseRunner.RunNow(ctx, productschedule.SourceManual)` `pulse.go:558`, 409 plain text on `ErrAlreadyRunning`, → `{session_id:"parent"}`), `voice_hardware.go` superset (`available,installed,downloading,loading,ready,got_bytes,total_bytes,size_mb` from `familyVoice.Status()`).

### Phase 3 — chat history (0.5–1 day)
- `chat_history_routes.go`: `GET /api/chat-history/sessions/{id}` → sid → `(scope,id)` → `loadStoredConversation` (`conversation_store.go:34,250`). **404 only for an unknown conversation**; known-but-empty → `200` with empty `conversation_history` (client treats non-2xx/non-404 as fatal). Body `{session_id, conversation_history:[{Role:"human"|"ai", Parts:[{Type:"text",Text}], resume_order, resume_source_message_count}], history_pagination:{has_more,next_offset,start_turn,total_turns}}`; client parsing accepts either casing and collapses consecutive assistant messages (`frontend/shared/session/restore.ts:10-23,131-166`). **Honour `resume_turns`** (client sends 100) — `conversations/parent.json` is append-only forever (Pulse adds two messages per check) and is fetched twice per mount.
- `Role:"tool"` rows: `celebrate`/`scene` (`Tool:"celebrate"|"scene"`, `Stars/Reason/HTML`) → emit as `ui_events` `product_interaction` with kinds `celebrate|scene` (ChildPlatformChat already renders them); `photo/video/voice_failed` (WhatsApp media, `whatsapp_bot.go:435-447`) → a new `media` product row (`product_interaction {kind:"media", payload:{path,tool}}`, live + restored) — until built, WhatsApp media is a **known gap** in platform mode; `Source:"pulse"` badge → acceptable loss for v1 (restored user row shows the trigger label).
- **Tool-call rows after restart** (Phase 3b, can follow Phase 6): on turn end dump the session's `tool_call_*` + `product_interaction` events to a bounded sidecar (`conversations/parent.ui-events.json`) and return them as `ui_events`; the client merges/dedups by id and back-fills `tool_params.arguments` (`sessionRestore.ts:211-252`).
- **Text parity**: the live test asserts `lastAssistant.text === result.reply` (`live.test.ts:41`); the persisted reply has `appendSentFileLinks` applied (`chat.go:749`) while `unified_completion.final_result` may not — emit the completion after the links are appended, or the mapper must use the same source the live path does.

### Phase 4 — workspace `/api/wp` (2–3 days)
- `wp_routes.go`: `wpTarget(raw) (kind, rel, ok)`: strip `_users/<x>/`, then `Chats/SparkQuill` (accept bare paths); `family.json` → virtual; `activities` → virtual listing; else `resolveWorkspacePath` (`materials.go:13`).
- `wp_documents.go`: `GET …/documents/{path...}` (detect trailing `/raw` manually), listing `?folder&max_depth` → convert `buildTreeSized` (`workspace.go:45`) to `Document{filepath:"Chats/SparkQuill/"+path,type,size,children}` and **prepend a virtual `family.json` node** (live test asserts it); `PUT` → `writeFileAtomic`; missing file → `200 {success:true,message:"File does not exist",data:{filepath:""},error}` (mirror).
- `wp_upload.go`: multipart `file`,`folder_path` → `{success:true,data:{filepath:"Chats/SparkQuill/inbox/<safeName>",filename,file_size}}`; if folder == `currentActivityDir()` also `saveCurrentUpload` (parity `upload.go:101-103`); keep the 64 MB limit.
- `wp_raw.go`: token check, `http.ServeContent` (Range), `Content-Type` by ext, `inline; filename=`; `download=true` → attachment; **no** `rewriteRelativeAssetURLs`/`withDiagramLibHTML`.
- **Virtual `family.json`** (never materialised on disk): GET returns **only** the projection `{child:{name,grade,board}, parent_label, pin_hash, watch_sites: s.Pulse.Sites()}` (`Sites()` folds legacy `school_portal_url`, `state.go:165-183`; client type `workspace.ts:13`). PUT is a **whitelisted merge** under `stateMu` of exactly those four keys: `child` merges `name/grade/board` preserving `Language`/`CreatedAt` (on create set `Language:"en"`, `CreatedAt`, run `scaffoldFamilyFolders()` + `seedWorkspace()` like `handleCreateChild`, `state.go:307,326`); `pin_hash` stored as given (same sha256-hex as `handleSetPin` `state.go:350-353`); `watch_sites` → `Pulse.WatchSites` and clear `SchoolPortalURL`; everything else in the body ignored — never let the client blob overwrite `engine`, `pulse.enabled/cadence/preferred_hour`, `schedule`, `fast_mode`, `selected_models`, `whatsapp_voice_enabled`. Inject a `family.json` node into the `Chats/SparkQuill` listing.
- **Activities**: only the `activities` **listing** is virtual and its entries carry the **real** dir as `filepath` (`Chats/SparkQuill/Maths/Fractions/<slug>`, `type:"folder"`). The client never parses the `activities/` segment (`workspace.ts:118-123,31-37,171`; `platformApi.conversationKeyFor` takes the last segment), so its subsequent `activity.json` read, `current-activity.json {dir:<realDir>}` write (identical to `activity.go:177-179`), handoff `new_session` comparison and uploads all hit real paths — no other rewriting, child sandbox (`child_workspace.go:19-31`) untouched. Returning `activities/<slug>` paths instead would dangle `currentActivityDir()` and force perpetual "Start fresh".
- **`state/<key>.json`** → `<currentActivityDir>/attempts/<safeStateKey(key)>.json` when an activity is bound (keeps the child's answers inside the sandbox and `activityLastTouched` honest, `archive.go:80-86`), else workspace-root `state/<key>.json` (the live test saves `live:key` with no activity bound). Client envelope `{key,data}`; standalone adds `updated_at` — read `.data` either way.
- **Reserve `activities` and `state`** in `reservedTopLevel` (`activity.go:60-87`): today neither is reserved, so a physical folder of that name would be treated as a Subject by `isSubjectDir`, walked by `listActivities`, and archived by `archiveStaleActivities` (`archive.go:37-62`).
- **Raw HTML** (print/download/agent self-check only — the viewer renders HTML via `srcDoc` from the JSON GET, `LearningApp.tsx:3408`): reuse `withDiagramLibHTML` (`conversation_store.go:383-394`; `/lib/` ships via `extraResources`) and the `?print` script (`:441-443`); parameterize `rewriteRelativeAssetURLs` with a URL builder that emits `<dir>/<asset>/raw?token=<same token>` (its hardcoded `/api/workspace/raw?path=` at `:355` is wrong here, and a bare relative `foo.png` would otherwise resolve to the **JSON** documents endpoint). Accept `download=1|true`. Frontend nit: `platformApi.rawUrl` drops `opts.print` (`platformApi.ts:318`) → pass `print=1` through.
- Upload: infer `scope=child` when `folder_path` equals `currentActivityDir()` so `saveCurrentUpload` still queues the photo for the next child turn; keep family-server's 64 MB and no type allow-list (the workspace module's 10 MB + `isAllowedFile` would reject files families upload today).

### Phase 5 — frontend (2 days)
- `desktop-sparkquill/preload.js`: `backend: () => process.env.SPARKQUILL_UI ?? 'desktop'`; `main.js:108-112` forwards `SPARKQUILL_UI` (kill switch `legacy` for one release, decision 17). `dev-setup.sh`/CI unchanged (no `VITE_SPARKQUILL_BACKEND`).
- `frontend/learning-app/src/api/desktopApi.ts` (new) + `index.ts`: `backend === 'desktop'` → `createDesktopApi()` = `createPlatformApi` for conversations + `tree/readFile/rawUrl/upload/saveState/loadState/activities/handoff/childActivity`, and `standaloneApi` for `setup/engines/validateEngine/selectEngine/models/saveModel/fastMode/saveFastMode/secrets/voiceStatus/browserStatus/whatsapp*/pulseConfig/savePulseConfig/runPulse/verifyPin` (all same-origin, already implemented). Keeps engine-selection onboarding, models, Fast Mode, WhatsApp pairing, `preferred_hour`, and the constant-time PIN compare (`state.go:385`). `createPlatformApi` stays pure for a future platform-hosted SparkQuill. `rawUrl` passes `print`.
- `runtimeConfig.ts`: treat `desktop` like `platform` for `__APP_RUNTIME_CONFIG__` (it's the same wire protocol).
- `LearningApp.tsx`: gate the legacy parent effects `:1144`, `:1685`, `:1733`, `:2211` on `backend === 'standalone'` (same shape as the child gates at `:1768,1810,1933`); render `PlatformChat`/`ChildPlatformChat` for `backend !== 'standalone'`.
- `platformApi.live.test.ts:60-61` `week` fix (if not done in Phase 0).

### Phase 6 — tests + live verification (2 days) — see Verification.

### Phase 7 — cleanup (1–2 days, only after Phase 6 passes live, including against a copied real `~/.sunlit-learning`)
1. Delete the gated legacy parent effects (`LearningApp.tsx:1144, :1685, :1733, :2211`).
2. Delete the bubble branches: `backend !== 'platform'` blocks (`:2829-`, `:3936-`), the parent thread renderer (~`:2938-3100`, incl. photo/video/voice rows `:2990-3035` and Pulse badge `:3059-3085`), the child bubble branch (`:4078-4135`). `PlatformChat`'s landing greeting still uses `fl-msg/fl-bubble` (`:2946-2948`) → restyle those two.
3. Remove `FamilyApi` methods only the bubble used (`sendParentTurn/steerParent/watchParent/loadParentConversation`, child equivalents, `TurnStreamEvent`, `TurnCollector`/`messagesFromEvents` in `platform/events.ts`); keep `history()` only if still read.
4. CSS in `learning-app.css`: `.fl-thread/.fl-msg*/.fl-bubble*` (`297-322`), `.fl-tbubble*` (`601-666`), markdown-in-bubble rules (`634-652`), Pulse variants (`841-842`), `.chat-bubble` (`206`), streaming cursor (`164`). **Keep** `--navy`/`--cream` (`:5,:9,:57`; 19 other shell usages) and all `.fl-platform-*` (`1231-1294`). Only `LearningApp.tsx` uses these classes (grep-confirmed).
5. Remove the `SPARKQUILL_UI` switch and `standaloneApi` chat methods.
6. family-server: keep `/api/parent/message|status|steer`, `/api/child/*` one release (WhatsApp/Pulse don't depend on them; `characterization_test.go` does), then remove with `status_stream.go`.

**Total: ~12–15 days.**

## Verification

- **Phase 0 dump** proves real event shape (no synthesis) and binary-size delta.
- **Unit (Go)** — pattern `characterization_test.go:27-95` (`fakeSession`/`installFakeSession`, `httptest.NewRecorder`, `FAMILY_DATA_DIR=t.TempDir()`, `postJSON`): `platform_routes_test.go` (login→token; conversation ids parent/child; events `-1` for unknown sid, `0` for known-empty; `/query` with a `fakeSession` extended to call `cfg.Observers[*].HandleEvent` with synthetic `AgentEvent`s (`tool_call_events_test.go:13-19` constructors) → ack, `running`→`completed`, both tool_call pairs, `product_interaction` from `suggest_actions`; SSE via httptest + cancelled ctx asserting `id:` == `Sequence-1`, `Last-Event-ID` resume, status ticks; live-input steer path; cancel/stop), `event_store_test.go` (observer → `GetEvents(working_set=session)` shape, `FilterSessionWorkingSet`), `wp_routes_test.go` (tree includes `family.json`; upload → `inbox/live-test.txt`; raw 200/206 with `Range` + `?token=`; `family.json` round-trip into `familyState`; activities listing maps real dirs; traversal rejected), `chat_history_routes_test.go`, `platform_scheduler_test.go`, `platform_secrets_test.go`, `platform_wire_sync_test.go` (json-tag parity vs `package server`). `go vet`, `go test ./cmd/family-server/...`.
- **Acceptance**: `SPARKQUILL_PLATFORM_URL=http://127.0.0.1:8010 npx vitest run platformApi.live` (real engine + child set up) must pass unmodified after the `week` fix — it drives conversation → query → events/SSE → status → live-input → chat-history → `/api/wp` tree/upload/raw.
- **Live Electron E2E** (per project rule: LLM-driven code counts only with live verification, with the agent's JSON sign-off recorded): `dev-setup.sh && npm start` (platform backend via preload); parent turn → tool-call rows render (the original complaint), `suggest_actions` pill appears and submits, `open_file` opens the viewer; handoff → child turn with `celebrate`/`show_scene`; upload from composer and drawer; Pulse "Run now" streams into the open tab; **relaunch mid-conversation** → history restored from chat-history and CLI `--resume` handles still line up (`session_handle_store.go`); WhatsApp status/pair screens work via the hybrid adapter.
- **Go/no-go for Phase 7 deletion**: all of the above green + a real family workspace (`~/.sunlit-learning` copy) exercised without migration.

## Risks (ranked) and mitigations

1. **Activity path identity / handoff corruption (data).** Virtual `activities/<slug>` paths leaking into `current-activity.json` would dangle `currentActivityDir()`, break child turns and the sandbox, and force perpetual "Start fresh". → Virtual folder with **real** child paths; reserve `activities`/`state`; round-trip test `activities() → handoff → currentActivity → child /conversation`; exercise a copied real workspace before Phase 7.
2. **Turn lifecycle (correctness).** `r.Context()` would cancel the async turn; a turn that never reaches a terminal status leaves the client polling forever; Pulse/WhatsApp holding `agentTurnMu` looks like a hang. → `context.Background()`+timeout, deferred sticky status transitions, busy `status_line` event, cancel registry, unit test that `/query` acks before `Ask` completes and the turn still finishes.
3. **`family.json` dual-source merge (data).** A naive PUT-to-disk would relocate the file, clobber `engine/pulse/schedule/fast_mode/selected_models`, drop `child.language/created_at`, and expose the PIN hash. → Projection GET + whitelisted merge under `stateMu`; never write the client blob; keep `verifyPin`/Settings on standalone routes via the `desktop` hybrid adapter.
4. **Restart amnesia and transcript gaps (UX/data).** Tool rows, WhatsApp media, celebrate/scene and Pulse badges are lost or never shown; `parent.json` grows unbounded and is fetched twice per mount. → Chat-history mapper with `resume_turns`; `ui_events` sidecar for tool/product rows (3b); `media` product row; Pulse observer with trigger-label rewrite; agentic sign-off on restored transcripts.
5. **Silent Settings regression + stale acceptance test (rollout).** Pure `platform` mode nulls models/Fast Mode/WhatsApp/`preferred_hour` and skips engine selection; the live test throws on `week`. → `desktop` hybrid adapter; `SPARKQUILL_UI=legacy` kill switch for one release; fix `live.test.ts:60-61`; pass `print` through `rawUrl`.
6. **Protocol drift vs `cmd/server` (maintenance).** Copied wire structs/handlers can diverge as ChatArea evolves. → `platform_wire_sync_test.go` (test-only import of `package server`, reflection json-tag parity) + the live vitest acceptance test in CI against family-server.
7. **Pre-existing platform-mode viewer limitations (not regressions, settle with user):** raw HTML in an `<iframe>` with relative `<img src>` needs the `/raw?token=` rewrite (same on the main server); `rawUrl` ignores `print` today.
