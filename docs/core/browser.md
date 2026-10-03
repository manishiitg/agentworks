# Browser: ownership, automation, live control and teaching

This is the single browser guide for our products. It consolidates the former
core browser, workflow browser-authoring and live-workflow-browser guides.
Runtime behavior below is based on source review on 2026-10-03, not a fresh
verification of every deployed server. Sections marked **proposed** describe
pending work; this consolidation does not implement a new browser UI or recorder.

## Ownership and product behavior

A managed browser belongs to a workflow or product project, not to a person.
A user's identity authorizes access; it is not the browser's ownership scope.
Code's owner-only access and other product access rules continue to apply.
A workflow's Builder, runs and delegated agents resolve to the same browser.
A Crew/Code project's conversations resolve to that project's browser. Anonymous
or workspace-less conversations have a session-scoped fallback.

The resolver uses an owner-qualified physical project path so two owners'
same-named projects remain distinct. A deployment prefix separates deployments.
Different projects/workflows have separate sockets and profiles. Shared access
within one scope intentionally shares its website logins; sharing a browser does
not grant access to a workspace. Disk persistence still requires deployment
profile configuration; browser ownership alone does not preserve a login across
browser restarts.

| Product/surface | Current behavior | Work still needed |
| --- | --- | --- |
| AgentWorks workflows | Manifest mode controls browser access; new backend manifests use `auto`, while legacy missing modes and some frontend defaults use `none`. | Align defaults and remove ordinary Disable/No browser choice. |
| Code and Crew | Browser capability is preferred; runtime enables it and coerces `none` to `auto`. Workspace resolver supplies a project browser. | Save configuration durably at project scope; remove misleading disabled option. |
| Work browser panel | Saves browser settings in chat/tab configuration and updates open tabs for the same project. | Make the project preference canonical rather than depending on open tabs. |
| Video Studio | Required browser; managed workflow browser, with server CDP disabled in deployment configuration. | Apply common start/sign-in/teaching UI. |
| SparkQuill | Parent browser preferred; child browser explicitly disabled. Browser setup still includes local CDP guidance. | Make setup deployment-aware; preserve child restriction. |
| Relays | Builder exposes the browser tool, but workflow mode still gates it. | Align workflow defaults with normal browser availability. |
| Dominion | Product tool allowlist excludes `agent_browser`. | Keep this explicit product restriction unless separately changed. |

`mcpagent` provides tool transport; MCP connection pooling does not own browser
state. `multi-llm-provider-go` does not choose browser scope.

### Agreed local/server direction — proposed

- Local installations offer automatic selection, managed browser, or local CDP.
- Server installations use a managed browser; users view and control its stream
  through the app. Server UI must not ask them to install a local CDP launcher.
- Normal workflow/project setup does not need a Disable browser option. Browser
  availability does not eagerly launch Chrome; start it when the user or agent
  needs it. Explicit product/admin restrictions remain enforced.
- Browser configuration and teaching artifacts belong to the workflow/project.
  Do not introduce a per-user browser preference or merge profiles across scopes.

Existing `none` manifests still disable workflow browsing today. Removing the UI
choice requires an explicit migration policy for legacy/default `none` versus
intentional restrictions. That migration is pending; changing this guide does
not enable those workflows.

## Start browser and manual sign-in — proposed

Current live-view routes discover and control an existing browser. Opening the
Browser panel does not launch Chrome, and there is no user-start route yet.
A browser normally appears after the agent opens it.

The proposed flow is **Browser → Start browser → open site → sign in → Return
control**. It works without sending an agent message first:

1. An authenticated start request names the workspace. The agent API checks
   access and product capability, resolves the same trusted browser scope used
   by agent tools, and delegates startup to the workspace service.
2. Under a per-scope startup lock, reuse a healthy browser or launch one with
   the central launch/profile configuration. Repeated clicks return the same
   browser; do not create another daemon or reset its tabs/cookies.
3. Show the existing authenticated stream. Acquire the existing exclusive
   manual-control lease before accepting input. If busy, show the owner/busy
   state rather than silently interrupting an agent action.
4. User opens the site and signs in inside that browser. Teaching is off during
   login. Do not export credentials or cookies into a teaching artifact.
5. Returning control releases the lease. The agent uses the same browser/profile
   and can check that the site is signed in. Sites may later expire the login.

On a server, the user sees streamed pixels from server Chrome; the site is not
embedded as an iframe in our application. UI mouse/keyboard input travels through
the authenticated stream proxy and is applied to that Chrome instance. On local
CDP, the user may interact directly with the attached Chrome. The CDP browser's
own profile remains the login owner.

## Teach a browser task — proposed

The product flow is **Start browser → sign in → Teach task → describe the result
→ demonstrate → Finish → review draft → Test → save for reuse**. Start with a
browser-only implementation. Ordinary diagnostic recording remains available
separately; a video/HAR bundle is not already a learned task.

### Capture at the browser, not only at the viewer

Use a trusted recorder attached to the exact existing browser and selected
teaching tabs, through a runtime-provided private automation connection. A
Playwright-based collector is the initial implementation candidate; first prove
attachment to the installed agent-browser runtime without launching a replacement
browser, changing its profile or resetting login state. The recorder is an
internal service, not a raw CDP endpoint exposed to users or agent shell commands.
Managed agent actions continue through `agent_browser` and its control gate.

| Evidence | What to capture | Why |
| --- | --- | --- |
| Structured action | Click, final field edit, select, relevant key/submit; timestamp, page/frame ID, semantic target candidates and scoped context. | Locate and repeat the intended action. |
| Browser lifecycle | Navigation, popup/tab creation, tab switch, dialog and download metadata. | Follow activity that DOM click listeners alone miss. |
| Result evidence | Relevant before/after page snapshots and visual frames around meaningful actions. | Explain what changed and propose success checks. |
| Input fallback | Viewer pointer/keyboard metadata with viewport and event correlation; omit sensitive text. | Correlate input with a DOM target or diagnose a canvas action. |

Only collect the bounded region needed for the task. Do not dump every DOM
mutation, every page's content or every keystroke. Network/HAR diagnostics are
optional and not required to teach a browser procedure.

Recorder mechanics:

1. Instrument existing allowed pages/frames immediately. Register an init script
   for future documents/frames and attach to new allowed popup targets. Observe
   actual page events in capture phase; resolve targets through their event path
   and retain role/name/label, useful attributes and containing row/card context.
   Prefer genuine user input events; script-generated events are not evidence
   that the user demonstrated an action.
2. Send event batches to the trusted collector using a page-to-host binding.
   Browser/page/frame identity and teach-session ownership come from the host,
   not fields supplied by the web page. Validate and limit payloads; page scripts
   and page content are untrusted evidence, never instructions to the learner.
3. Subscribe to navigation, tab, download and dialog events at the browser level.
   Page injection alone cannot observe Chrome's toolbar or native file chooser.
4. Normalize edits into a final `fill` action on blur, submit or relevant next
   action, preserving meaningful intermediate actions. Handle IME/paste explicitly.
   Deduplicate viewer input and matching DOM events using ordered IDs/timestamps;
   do not count one click twice. Record bounded visual/state evidence after an
   action settles; flag unmatched or missing events.
5. Persist an ordered trace with coverage information. Flush before Finish and
   finalize evidence before dispatching the learning job. Remove teaching
   listeners on stop/cancel without closing the browser.

A page listener also observes direct user interaction in local CDP Chrome; a
viewer-only logger would miss it. Explicitly scope recording to opted-in tabs
because other tabs may contain unrelated activity. Show recording state in the
app and, for direct local Chrome interaction, a visible page indicator where
injection is supported.

Playwright documents [init scripts for new pages, navigations and child frames](https://playwright.dev/docs/api/class-browsercontext#browser-context-add-init-script)
and [page-to-host bindings](https://playwright.dev/docs/api/class-browsercontext#browser-context-expose-binding).
Its [test generator](https://playwright.dev/docs/codegen) provides a reference for
semantic locator generation and ambiguity handling; using those APIs does not
by itself give us a complete teaching recorder.

### Ownership, lifecycle and sensitive input

Starting Teach requires workspace write/control permission and an exclusive
teaching lease for that browser, layered on the manual-control gate. It prevents
agent actions and a second demonstrator from interleaving with the demonstration.
Read-only viewers can watch according to existing product grants. Start, Finish,
Cancel and status must be idempotent and scoped to the owning workspace and
teach ID. A disconnect releases manual control and pauses teaching until an
explicit resume; a crash or timeout marks evidence interrupted rather than
silently producing a verified skill. Set a bounded duration/artifact quota and
keep completed evidence available for authorized review/deletion.

Sign in before Teach. Suppress password/OTP field values before buffering or
writing event data, never collect cookies/auth headers, and redact sensitive URL
parameters. Provide Pause/Resume for sensitive steps and stop visual capture
while paused. DOM redaction cannot guarantee that video hides secrets already
visible elsewhere on a page; automatic masking needs separate qualification.
Learning artifacts name required login/access as a precondition, not credential
values or recorded login keystrokes.

### From demonstration to repeatable procedure

Use a versioned artifact manifest with workspace/browser scope, tab/frame map,
recording times, ordered action IDs, interruptions and capture coverage. Proposed
raw artifact location: `<workspace>/browser-demonstrations/<teach-id>/`, including
`actions.jsonl`, `manifest.json`, selected snapshots/frames and optional video.
Publishing uses the existing guarded artifact path; raw evidence is not a global
per-user library and must not escape the scope's file permissions.

After Finish, a learning job receives the user's goal and sanitized trace. It
produces a draft with starting conditions, variable inputs, ordered semantic
steps, candidate locators, explicit waits, result checks and known failure cases.
Each step references its evidence. One demonstration does not prove every branch
or retry rule; missing conditions remain questions or limitations in the draft.

For example, typing a particular customer and selecting a date range should
become `customer` and `date_range` inputs, with a scoped row match and an export
result check. Do not infer that every demonstration value is a variable; let the
user review suggested parameters. Never persist `@e1`, raw screen coordinates
or a generated CSS path as the sole reusable locator. Resolve fresh targets on
replay and require one intended match before acting.

Store the accepted procedure through the existing learning system:
workflow `learnings/_global/` with a short skill index and focused reference;
Crew/Code through the project's existing reference-skill/instruction convention.
Reusable code is optional. Do not create a separate user-wide learned-skill store.
See [workflow learning](../workflow/learning_architecture.md) and
[project instructions](../design/project_instruction_files.md).

A Test run performs the procedure through managed `agent_browser` on the same
scope's signed-in browser, using fresh snapshots/locators and supplied inputs.
Check the expected page/business outcome and any output artifact, not merely
that clicks completed. Replay can perform real writes; the Test UI must state
what will run and use an example the user chose. Track `draft`, `tested`, and
`needs repair` with browser/site preconditions and last test evidence. A failed
or ambiguous step stops and returns to review; do not silently retry a submit or
claim that a valid video proves task success. Later website changes may require
repair and another test.

### Delivery order and acceptance

| Stage | Deliverable | Acceptance evidence |
| --- | --- | --- |
| 1. Browser access | User Start/reuse, server-aware UI, persistent scope profile, shared manual sign-in. | Start without agent; repeated start reuses; correct scope isolation; login survives configured restart; agent sees same sign-in. |
| 2. Recorder proof | DOM/input/visual trace in the existing managed browser. | Click, edit, paste/IME, navigation, popup, frame, tab switch and download trace; no duplicate actions; existing cookies/tabs preserved. |
| 3. Teach and draft | Goal, exclusive lease, Pause/Finish/Cancel, trace finalization, learning job and review. | No mixed agent/user actions; sensitive values omitted; direct local CDP capture; interrupted sessions visible; no cross-scope evidence. |
| 4. Replay | Parameter review, managed execution, outcome checks, saved project/workflow procedure. | Two runs with different inputs; stable scoped targeting; clear failure on ambiguity/changed page; checked output, not just successful tool calls. |

First qualify ordinary web forms and tables, including supported frames and open
shadow roots. Closed shadow DOM, canvas-only controls, native dialogs, protected
pages and unsupported frames need an explicit coverage/fallback indication;
video alone must not make them look deterministically replayable. Add visual
reasoning fallback after the structured path is qualified. Scheduling can use
a tested procedure through existing workflow scheduling; it is not needed for
the first teaching release.

### External reference and evidence limits

[Grok Bot's official teaching flow](https://docs.x.ai/grok-bot/skills-routines-and-automations#teach-a-workflow-by-demonstration)
records visible interaction for up to ten minutes without microphone audio,
then produces a draft skill for testing. An
[unofficial reconstruction's recorder](https://github.com/b-nnett/grok-bot-0.18-reconstructed/blob/a9f633e09d49a85829b8236331b9e21f7e612634/source/host/extensions/teach-recording/teach-recording-service.ts#L141)
uses FFmpeg/X11 video and dispatches `learn-from-demonstration`; it does not show
DOM-event recording in that service. Its
[provenance](https://github.com/b-nnett/grok-bot-0.18-reconstructed/blob/a9f633e09d49a85829b8236331b9e21f7e612634/PROVENANCE.md)
is a partial reconstruction, not official source. The learning skill is fetched
separately, so its analysis internals are unverified. Our structured recorder is
our proposed design, not a claim about Grok's latest implementation.

## Current automation reference

All normal automation uses the managed `agent_browser` tool and matching
`agent-browser` skill. Chrome can run headlessly in the workspace or, on local
installations where permitted, attach to a visible Chrome through CDP.

## Modes

| Mode | Behavior | Typical use |
|---|---|---|
| `none` | Workflow browser tools are disabled today. | Legacy/internal configuration; ordinary UI removal is proposed above. |
| `auto` | Use a reachable configured CDP browser; otherwise use headless. | Default. |
| `headless` | Use the workflow/project’s managed Chromium. | Background and scheduled runs. |
| `cdp` | Attach to the configured Chrome debugging port. | Existing logins, visual QA, and sites that reject headless browsers. |

The workflow manifest stores the mode under
`capabilities.browser_mode`. Browser steps attach the `agent-browser` skill.

## Starting a CDP browser

On macOS, install the default launcher on port `9222` with:

```bash
curl -fsSL 'https://raw.githubusercontent.com/manishiitg/coding-agent-loop/main/scripts/install-chrome-cdp-macOS.sh' | bash
```

Install another independent launcher/profile by passing a port:

```bash
curl -fsSL 'https://raw.githubusercontent.com/manishiitg/coding-agent-loop/main/scripts/install-chrome-cdp-macOS.sh' | bash -s -- --port 9333
```

Each CDP profile must use its own port and `--user-data-dir`. The usual port is
`9222`; the port-specific installer creates a separate application and profile.

For a specialized workflow that needs multiple login identities, launch more
profiles on different ports, for example `9222` and `9333`, then configure:

```json
{
  "browser_mode": "cdp",
  "cdp_ports": [9222, 9333]
}
```

The runtime accepts at most four configured ports. Ordinary workflow
concurrency does not require multiple profiles: workflows share one CDP browser
and use labeled tabs plus a per-port select-and-act lock.

## Managed tool

Do not run the `agent-browser` CLI through the shell for browser actions. Call
the managed `agent_browser` tool. The runtime injects and validates the CDP
endpoint, applies session limits, serializes shared-tab actions, and authorizes managed
upload/output paths. `file://` navigation has the separate limitation below.

To check whether CDP is reachable, use the backend status operation:

```text
agent_browser(command="status", args=[], session="default")
```

`status` needs no tab and no `--cdp` argument. `snapshot` is not a connectivity
probe: it reads one specific page, so in shared CDP mode it must name the tab.

Before the first browser action, load the installed CLI's matching command guide:

```text
agent_browser(command="skills", args=["get", "core"])
```

The common flow is:

```text
agent_browser(command="open", args=["https://example.com"])
agent_browser(command="snapshot", args=["-i"])
agent_browser(command="click", args=["@e1"])
agent_browser(command="snapshot", args=["-i"])
```

In CDP mode, list and reuse a suitable tab before asking to create one. Include
the returned real tab ID (`t1`, `t2`, and so on) inline for every page action.
`open` itself remains URL-only. The inline system prompt gives the exact
endpoint and argument form for the active session.

## Shared CDP tab lifecycle

One visible Chrome is shared safely by verifying and acting under a per-port
lock. A workflow must not assume that the tab selected during its previous tool
call is still active: the user, the website, or another workflow may have
changed Chrome in the meantime. The backend therefore reads the real tab state
immediately before every page action while it holds the shared lock. It keeps
using the requested real `tN` when that tab is already active, and switches only
when another tab is active. This avoids repeatedly bringing Chrome to the
foreground on macOS without allowing one workflow to act in another tab.

The normal flow is:

1. Call `agent_browser(command="tab", args=["--cdp", "<endpoint>"])` once to
   inspect real tab IDs and query-free display URLs.
2. Reuse the workflow's already-owned labeled tab when one exists. It may be
   navigated to the requested URL.
3. Otherwise, reuse a pre-existing tab only when its normalized URL exactly
   matches the requested URL.
4. If neither matches, request a stable labeled tab with
   `agent_browser(command="tab", args=["--cdp", "<endpoint>", "new",
   "--label", "<workflow-label>", "https://target.example"])`.
5. Keep the returned real `tN` and provide it inline on subsequent actions.

The backend repeats the list-and-reuse check atomically before executing
`tab new`. It refuses creation if the real tab list is unavailable or invalid,
rather than risking a duplicate. A label collision with a pre-existing tab at a
different URL is also an error. An arbitrary same-origin tab is deliberately
not reused because navigating it could destroy unrelated user state. URL query
parameters are hidden from model-facing tab lists, but the backend retains the
full normalized URL for exact-match decisions.

`tab new` arguments are parsed and rewritten into the canonical
`new --label <label> <absolute-url>` order before reaching agent-browser. This
prevents a misplaced URL or option from being interpreted as the page to open.

### Model-context behavior

Tab management is intentionally compact:

| Operation | Returned to the agent |
|---|---|
| Explicit `tab` list | At most 20 compact lines; labels, titles, and URLs are individually truncated. |
| Select one tab | A short selected-tab message, not the raw tab list. |
| Automatic active-tab verification before a page action | Nothing extra; the internal tab-state/selection response is discarded. |
| Atomic reuse check before `tab new` | Nothing extra; only the reused/created tab summary is returned. |

Consequently, a large Chrome window does not add every tab to context on every
browser action. Repeated explicit list calls can still accumulate in the
conversation history, so agents should list once, retain the returned `tN`, and
list again only when the tab disappears or the target is genuinely unknown.

### Ownership and cleanup

Only tabs actually created by a workflow are registered for automatic cleanup,
and ownership is recorded against the real `tN` ID returned by agent-browser.
A pre-existing tab reused by exact URL remains user-owned and is never enrolled
in cleanup.

After the final browser-owner lease is released, created tabs remain available
for review for one hour and are then closed by real `tN` ID. Concurrent runs
delay that timer until the final lease ends. Already-missing tabs, including
agent-browser's `No tab with label` response, are retired from the registry
instead of being retried forever. Never call the top-level browser `close` in
CDP mode because it can terminate the user's real Chrome session. Close a
specific workflow-owned tab immediately only when the user requests it or the
workflow must replace it.

## State and isolation

- CDP mode uses the user's real Chrome cookies and login state.
- Managed headless mode uses one browser per workflow or product project. Authorized callers share that scope; unrelated scopes are isolated. Disk-persistent profiles require `AGENT_BROWSER_SHARED_PROFILE`. Tabs are optional; reuse the current tab or create one when useful.
- Shared CDP concurrency is isolated by real tab IDs plus a per-port
  select-and-act lock; labels are aliases, not durable tab identities.
- Delegated agents inherit the workflow/project browser. Explicit session labels do not create independent browsers. Preserve it at workflow completion. Configured CDP profiles retain their separate specialized login behavior.
- Workflow-created CDP tabs are closed automatically one hour after the final
  run releases its lease; reused user tabs are preserved.

Browser session tracking lives in `agent_go/pkg/browser`. MCP subprocess
connection pooling in `mcpagent` is independent of browser state.

### `file://` URLs are not path-restricted (deliberate, not an oversight)

In CDP mode the browser is the user's **own** Chrome — a host process this app
neither owns nor sandboxes — so `agent_browser` can read any file on the
machine via a `file://` URL, including files the shell tool is explicitly
denied. SparkQuill's standalone server rejected any `file://` URL resolving
outside the workspace or host Downloads (`validateBrowserFileURLs` in its
`browser_tool.go`), verified against a real exploit: the shell tool refused a
decoy file outside the workspace while the browser read the same path's
contents straight back.

That server was deleted on 2026-09-06 and the platform's `agent_browser` has
no equivalent guard: a product's browser access is all-or-nothing
(`runtime.browser` in its `product.yaml`; SparkQuill's child profile sets it
to `disabled`, the parent profile to `preferred`). This gap was raised with
SparkQuill's owner on 2026-09-06 and left unrestored by their explicit choice
— they'd rather the model use its own judgment about which files to read than
have a hard path guard. Not a platform default recommendation for other
products; a single-user deployment's owner deciding what their own AI may
read on their own machine.

**Canonical reference:** [`docs/agent-execution-architecture.html`](../agent-execution-architecture.html)
§4 — the measured before/after, the full list of rejected spellings, and how
this relates to the shell and image-sub-agent boundaries. Keep the detail
there, not here.

## Debugging and evidence

Use agent-browser's managed diagnostic commands so they operate on the same tab
and session as the workflow:

- `network` for requests and HAR capture;
- `console` and `errors` for page diagnostics;
- `screenshot` for visual evidence;
- `record` for video evidence when the user or workflow explicitly requests it;
- `trace` and `profiler` for deeper debugging.

HAR and video artifacts may contain credentials, cookies, page content, or
personal data. Review them before sharing.

### Persistent browser artifact handoff

The agent-browser daemon may outlive the workflow process and therefore cannot
safely rely on that process's current directory or inherited FolderGuard. For a
named screenshot or recording, the managed adapter rewrites the browser output
to a unique file under `/tmp/agentworks-browser-artifacts`. The trusted
workspace server then validates that the staged file is regular, non-empty, of
the expected image/video type, and that the requested destination is covered by
the current request's write paths and is not blocked. It publishes the artifact
atomically into the workflow workspace and removes the staged source.

Screenshots are finalized in the same call. Video recording uses an
owner-and-session-scoped lease: `record start` stores the staged source and
`record stop` finalizes that exact source into the requested workspace path.
This handoff applies to both headless and CDP modes.

In CDP mode, agent-browser recording creates a fresh temporary browser context
and tab. The managed adapter diffs the real tab set, pins the workflow to the
new recording `tN`, rejects interactions until a fresh snapshot succeeds, and
routes stale original-tab arguments to the recorded context. `record stop`
closes the temporary tab and restores the original selection. Abandoned runs
are stopped and cleaned by delayed ownership cleanup so the shared CDP session
cannot remain stuck in an active recording.

## Recent failure findings and fixes

| Finding | User-visible symptom | Current fix |
|---|---|---|
| A tab label was sometimes stored as though it were a real tab ID. | Delayed cleanup called `tab close <label>`, failed, and tabs remained open. | Parse both direct `tab new` and tab-list JSON, persist the returned real `tN`, and treat missing-label errors as already cleaned. |
| Cached backend active-tab state was trusted between calls. | A page action could affect the wrong tab after Chrome changed externally. | Read the real active tab before every page action under the shared lock; select the resolved `tN` only when it is not already active. |
| The resolved `tN` was explicitly selected before every action even when already active. | Visible Chrome repeatedly stole macOS focus while the user typed in another app. | Preserve the current tab when the real state confirms it is already active; tab creation or a genuine tab change may still foreground Chrome once. |
| Agents could request `tab new` without a fresh reuse decision. | Repeated workflows accumulated duplicate tabs. | Perform an atomic owned-tab/exact-URL reuse check; fail closed when listing is unavailable. |
| Flexible or malformed `tab new` argument ordering could reach the CLI. | A new tab sometimes opened an unintended URL. | Validate an absolute URL and canonicalize the command before execution. |
| Raw tab output was suspected of entering context on every selection. | Concern about context growth with many Chrome tabs. | Return tab lists only for explicit list calls, cap them at 20 compact entries, and discard internal selection/reuse responses. |
| A persistent daemon resolved named evidence paths from stale sandbox state. | Named screenshots or recordings failed with path/`getcwd` errors. | Use the guarded staging-and-finalization artifact handoff described above. |
| Upload paths were forwarded unchanged while the command working directory pointed at the run Downloads folder. | Workspace-relative paths could resolve as `Downloads/Workflow/...`, and a daemon launched by an older step could not see a newly granted input folder. | Resolve and authorize upload sources in workspace-api, copy them into short-lived managed staging with the original basename, and remove staging after the command. |
| CSS ID selectors were not shell-quoted. | `upload #file path` was parsed by the shell as a comment and agent-browser reported missing arguments. | Treat `#`, backslashes, and home-prefix characters as shell-sensitive arguments and quote them. |
| Brokered output destinations were joined to the browser working directory. | A requested `Downloads/report.csv` could be published as `Downloads/Downloads/report.csv`. | Resolve brokered screenshot/video/download destinations once from the workspace root. |
| `record start` created a fresh context while selected-tab enforcement returned actions to the original tab. | A valid WebM recorded an idle page while the real reproduction happened outside the video. | Detect the new active `tN`, require a fresh snapshot, pin actions to it until stop, close it afterward, and fail closed if the handoff cannot be identified. |

## Live E2E contract

Run the real managed-browser contract with:

```bash
scripts/run-browser-e2e.sh
```

The test launches a dedicated temporary headless Chrome profile on a random CDP
port. It then exercises the production path from the managed executor, through
the real workspace `/api/execute` handler, into the installed agent-browser CLI
and Chrome. Two simulated workflow owners share that same CDP daemon and issue
overlapping requests. It verifies:

- exact-URL reuse does not create a duplicate or claim a user-owned tab;
- flexible input is canonicalized before a new tab opens;
- newly created tabs are tracked by their real `tN` IDs;
- changing Chrome's active tab externally cannot redirect the next managed
  action;
- selecting one tab returns a compact response rather than the all-tabs JSON;
- parallel page actions verify and, only when needed, switch onto each workflow's own `tN` tab;
- uploads from two newly granted, disjoint workspace trees cross an older daemon
  sandbox while preserving both filename and file content;
- cross-workflow upload reads and artifact writes are rejected by FolderGuard;
- parallel screenshots and explicit downloads are published only into each
  workflow's authorized evidence/Downloads folders and have valid content;
- video recording produces real WebM files, remains exclusive to one workflow
  at a time, rejects another workflow's start/stop calls, requires a fresh
  recording-context snapshot, routes stale original-tab actions to the new
  context, restores the original tab, and decodes the final frame to prove the
  visible test interaction was actually captured;
- shared reset is rejected while another workflow owns the CDP port;
- delayed cleanup closes each workflow's created tab independently and preserves
  both the other live workflow tab and the reused pre-existing user tab.

The test never attaches to the default port or normal Chrome profile. Override
Chrome discovery with `BROWSER_E2E_CHROME_BINARY=/path/to/chrome` when needed.

## File uploads and downloads

Use workspace-relative paths such as `Downloads/report.pdf` or
`Chats/output.csv`. Upload with the `upload` command. Browser downloads for a
workflow run are routed into its execution `Downloads` directory.

Upload paths are not passed directly to the persistent daemon. The workspace
server resolves each source against the workspace root (with a run working-dir
fallback for a bare filename), checks the current FolderGuard read grants,
rejects blocked paths and symlinks, and copies the file into short-lived managed
staging. The daemon receives that staged path with the original basename, and
the staging slot is removed as soon as the command finishes. This allows a
later workflow step to upload from its own authorized folder even if the daemon
was originally launched under a different step's sandbox.

There are two CDP download paths:

- A normal click in visible Chrome may place a file in the user's system
  Downloads folder. That folder is exposed read-only when explicitly granted;
  copy the required file into the run-scoped workspace before processing it.
- `agent_browser(command="download", args=[..., "<selector>",
  "<workspace-path>"])` is an explicit managed download. Its output is written
  to backend staging and atomically published into the requested authorized
  workspace path, like screenshots. It never writes through the persistent
  daemon directly into an arbitrary workspace folder.

## Operational rules

- Use snapshots and current refs for live actions. Re-snapshot after navigation,
  DOM updates, tab changes, or when ref freshness is uncertain.
- Persist durable selectors or parse fresh refs at runtime; never save a literal
  `@e1`-style ref as reusable configuration in a workflow script. Scoped read-only
  `eval` is a discovery fallback when snapshots are insufficient, not a required
  step. Verify locator uniqueness, intended context, and the action's outcome.
- Poll for page state instead of relying on long fixed sleeps.
- Never connect to the CDP WebSocket directly for normal actions; that bypasses
  tab locking and can race other workflows.
- On a local installation, a site rejecting managed headless browsing may
  require `cdp`; record that precondition in its learnings. A server with external
  CDP disabled cannot use that workaround.

## Workflow authoring

### Authoring sequence

1. Load the installed command guide with
   `agent_browser(command="skills", args=["get", "core"])`.
2. Open or select the workflow's labeled tab.
3. Take an interactive snapshot.
4. Identify the intended control and act with its current ref or a verified locator.
5. Verify the expected state; re-snapshot after navigation or DOM updates,
   switching tabs, or when ref freshness is uncertain.
6. Save stable site knowledge to the workflow's learnings.

### Persisted scripts

Snapshot refs such as `@e1` are valid only for the current page state. A saved
script may resolve a fresh ref from the current snapshot by role, accessible
name, and surrounding context, or use a verified semantic locator or DOM hook.
Save this locating recipe, never a snapshot's literal ref as reusable config.
Runtime snapshots can remain as evidence. CSS discovery is not required for
every browser step.

Candidates include role plus accessible name, labels, test attributes,
hand-written semantic `id`/`name`, and `aria-label`. None guarantees stability:
a Like button may become Unlike. Scope repeated controls to their intended
row/card and require one intended actionable match; do not choose the first
match when the target is ambiguous. Verify the resulting page/business state
before recording success or retrying.

Avoid generated framework IDs, hashed class names, and `nth-child` chains.
When the accessibility snapshot is insufficient, use a scoped read-only `eval`
to inspect relevant DOM attributes. Do not dump the full page, read credentials,
or click/submit through a discovery probe. Treat any discovered locator as a
candidate to verify. The attached `agent-browser` skill's **Selector Discipline**
section is the shared authoring and learning contract.

## Live browser and manual control

The Browser panel discovers managed browsers after `agent_browser open` runs.
Select a session to watch its active tab; settings are behind the gear button.
Opening the panel currently does not launch a browser. Closed/reaped sessions
disappear. Browser viewing is available through the shared workflow/project
components, subject to product access rules.

Watch mode cannot send input. Take control requires workspace write access and
an exclusive lease. Browser commands wait while manual control is held; unrelated
workflow work can continue. Return control or Escape releases the lease. Closing
the viewer, switching sessions or losing its connection also releases it.

### Sessions and tabs

A workflow/project resolves to one managed browser, which can have multiple
tabs. Discovery may also include isolated Playwright test sessions. The session
selector chooses the tracked browser to watch. The tab strip
shows that session's tabs, with the active tab highlighted. The viewer displays
one active tab at a time; it does not display all tabs simultaneously.

Normal browser commands update the existing session's live view automatically.
There is no separate viewer initialization command for each action. Session
discovery refreshes every five seconds while the panel is mounted. Live frames
arrive over WebSocket, with the proxy requesting a maximum of 10 frames per second.

Watching follows the agent's active tab. Switching tabs changes the actual
browser's active tab, so tab selection is enabled only after taking control.
Users with control can click, type, scroll, and select another tab. Escape returns
control to the agent. Manual control holds browser commands for that session;
it does not pause the entire workflow or unrelated sessions.

### Architecture

```text
Workflow Browser tab
    │ authenticated WebSocket through the app's existing HTTPS endpoint
    ▼
Agent API
    │ checks session owner and workflow; enforces watch/control mode
    │ connects using WORKSPACE_API_URL and WORKSPACE_API_TOKEN
    ▼
Workspace service (same host/container environment as agent-browser)
    │ resolves the session's .stream file to a loopback port
    ▼
agent-browser stream → managed headless Chrome
```

| Endpoint | Purpose |
| --- | --- |
| `GET /api/browser/live/sessions?workspace_path=Workflow/<folder>` | List authorized tracked browser sessions for the selected workspace. |
| `GET /api/browser/live/{session}/stream?workspace_path=Workflow/<folder>` | Authenticated viewer WebSocket on the agent API. |
| `GET /api/browser/live/:session/stream` | Internal workspace-service proxy to the local session stream. |

The frontend sends a heartbeat every 10 seconds. The server releases manual
control after a disconnect or 45 seconds without an incoming message. Only one
viewer connection can hold control of a session at a time. Other connections
can continue watching.

### Viewer controls

Clicking an inactive tab requests exclusive control before switching it. A busy
agent/controller is reported rather than interrupted. **Fill width** uses the
panel width with vertical scrolling; **Fit page** keeps the whole viewport visible.
Browser is a workspace view (the toolbar group is currently Pulse), not a Setup
page; mode/connection settings remain behind its gear button.

### Runtime mode changes

The RTS UI check exposed a missing-tool bug when a chat started with **No
browser** and was later changed to **Automatic**. Persistent CLI turns reused
the original tool registration. The follow-up release keeps the workflow
browser tool registered and reads the current manifest on each invocation.
Disabled, missing, or unreadable configuration cannot launch a browser. The
regression test covers enabling and disabling the same tool instance without
creating another chat. Shared fix: `961d22b5a`.

### Builder view switching

Interactive Builder guidance requests `open_workspace_view(view="browser")`
when beginning browser navigation for the user. The tool opens the full browser
panel; the stream updates automatically, without repeated refresh requests.
Scheduled and unattended runs do not manipulate the foreground UI. The Builder
should respect subsequent user view changes.

When a builder workspace-view action changes the visible panel, a small toast
identifies it, for example “Builder opened Browser”. Acknowledged UI actions
notify only after an applied result. Reopening the same visible view or
refreshing it does not create another switch notification.

## Persistent managed profiles

Set `AGENT_BROWSER_SHARED_PROFILE` to an absolute dedicated directory outside
release folders, for example `/data/video-studio/browser-profile`. Unset it to
retain ephemeral profiles; workflow/project browser scope is still enforced.
A filesystem root or relative path is rejected.

In persistent headless mode, all agent session names within one workflow map to
that workflow's `workflow-<hash>--browser` identity. Authorized users share its
logged-in accounts; write access is still required for input and recording.
Workflows use directories beneath
`<configured-profile>-workflows/`; projects use
`<configured-profile>-projects/`. Legacy fallback identities may use
`<configured-profile>-users/`. Browser actions share a
per-workflow control lock, and workflow completion preserves the profile. Agents
should inspect existing tabs and avoid reset/close or clearing storage without a
user request.

The shared launch settings are defined once in `workspace/browserconfig` and
used by automation, viewer tab controls, recording, and the browser supervisor.
Shared mode keeps Chrome's native Linux/version user agent, `en-US`, a default
1280x720 viewport, and UTC timezone. It retains the existing AutomationControlled
flag. No third-party stealth plugin is installed; detection avoidance is not
guaranteed. Browser upgrades may change the fingerprint. Existing isolated
profiles are not merged into the new shared profile.

The legacy dedicated/shared-browser deployment can install the user unit
`deploy/aws-ec2/server/video-studio-browser.service` and
run `video-studio-browser` from the release's `bin` directory. It supervises
Chrome separately from the agent/workspace services and gracefully closes it
on service stop. Enable the unit for the user's default target to restart it
on boot. The rootless release build includes the supervisor binary.
The profile directory must remain on persistent storage across deployments.
Cookies and local storage were verified across a real Chrome restart; sites
can still expire sessions or require MFA. This is login-session persistence,
not a separately configured password manager.

Do not use a global shared-browser profile as the default for new workflow/project
launches. A dedicated legacy supervisor is distinct from per-scope browser startup.
Profiles from older per-chat/global browsers are not automatically merged. The
prior global “all users” rollout does not define current ownership.

## Diagnostic recording (existing)

Use **Start recording** / **Stop recording** in the viewer for the same
bundle. Recording requires write access and runs on the server even if the
viewer disconnects. It preserves the existing browser and sign-ins. This is
diagnostic evidence capture; the proposed Teach flow adds structured actions
and learning on top of browser access, without redefining diagnostic recording.

The existing `workspace_browser.agent_browser` tool supports Builder's bundled
`capture` command in managed headless mode (ephemeral or persistent profiles):

```json
{"command":"capture","args":["status"],"session":"main"}
{"command":"capture","args":["start"],"session":"main"}
{"command":"capture","args":["stop"],"session":"main"}
```

Ask the agent, for example: “Record the browser, network and console while you
reproduce this issue, then save the recording.” The browser must already be
running with the intended page selected. The agent uses the same workspace
recording endpoint as the UI, so the Browser view's existing status polling
reflects chat start/stop operations. No new tool or CLI binary is needed.

The managed handler derives the owning workflow from trusted session settings
and sends its current folder permissions to the workspace service. Output normally
lands in `Workflow/<name>/browser-recordings/<capture-id>/`; a step with narrower
write access uses its authorized working directory's `browser-recordings/`.
The response supplies the actual directory and files. Read-only sessions may
inspect authorized recording status but cannot start or stop a capture. Another
workflow cannot stop an active recording or retrieve its paths. A completed
recording does not prevent another workflow from starting a new capture.

Start begins video and HAR capture and clears console/error buffers. Stop exports
`video.webm`, `network.har`, `console.json`, `errors.json`, `manifest.json`, and
`capture.zip`. HAR response bodies are excluded. Console/error output is a
buffer export, not an unlimited log stream. Selected-tab video and recovery
semantics are described below.

Start/stop are idempotent. After a timeout, query status before retrying. A partial
stop returns errors and may leave `recording=true`; inspect the result and retry
stop when necessary. Stop captures started for the task even if reproduction
fails, but do not automatically stop a recording that was already running. Never
mix a bundled capture with separate native `record`/HAR start/stop commands.
Stopping capture preserves the browser and sign-ins.

This extension belongs to Builder's handler and is documented in the tool schema,
Builder browser skill, and browser guidance. Upstream `agent-browser skills` does
not define it. Native `record` remains video-only; external CDP currently uses
its existing separate video/HAR/console commands instead of bundled `capture`.

### Ownership and capture recovery

Workflow/agent cleanup preserves workflow browsers. Idle reaping still applies when no
capture is active. Browser commands and manual control share a per-browser lock;
this serializes individual actions, not an entire snapshot-to-click conversation.
Agents still need fresh snapshots and coordination for concurrent tasks. A capture
started by a tool pins the browser to its owning root run until stopped; viewer-started
capture is workflow-scoped. Manual user control remains available.

Bundled user-browser capture encodes the selected-tab WebSocket stream with ffmpeg,
so tab switches appear in the same video. Native `record start` remains a separate,
single-target command; don't mix it with bundled capture. Background tabs are not
recorded simultaneously. The manifest records browser identity, owner session,
source, timestamps, frame count and validation. `validation=passed` means nonblank
frames and a decodable video, not proof that the workflow achieved its goal; inspect
the actual footage before claiming success. Never substitute an earlier run's video.
HAR/console/error exports retain the upstream CLI's scope and may be partial.

An encoder or browser disconnect marks the capture interrupted on the next status,
stop or start call. A service restart cannot revive a cached active marker. Starting
again creates a fresh directory/file and discards abandoned HAR state. ffmpeg is
required in the workspace service PATH. Deploy both backend services for this change.

Regression coverage: common user/alias/child routing, group-to-builder identity,
profile isolation, viewer authorization, cleanup and capture ownership, real ffmpeg
encoding across a simulated selected-tab change, blank footage, disconnects and stale
capture retries. A live smoke test is required after deployment; the old rollout
status is not a current deployment inventory.

## Synthetic microphone and camera

Managed headless browser launches (including the shared supervisor, tool commands,
UI tab controls and recording) include these Chrome flags through the central
`workspace/browserconfig.HeadlessArgs()` helper:

- `--use-fake-device-for-media-stream`
- `--use-fake-ui-for-media-stream`

These provide synthetic media devices and automatic media permission handling,
so server-side flows that require a microphone can obtain a stream. They do not
supply the user's voice or meaningful spoken dialogue. External CDP Chrome keeps
its own launch configuration. An already running Chrome needs one graceful
restart to apply the flags; the persistent profile is retained, while in-memory
page state and ongoing calls may need to be resumed.

The Builder agent-browser skill explains how to verify getUserMedia and the
application outcome without switching to an unrelated Playwright harness. A
microphone permission success alone is not evidence that an RTS simulation started.

References: [agent-browser launch options](https://agent-browser.dev/configuration),
[Chromium media switches](https://chromium.googlesource.com/chromium/src/+/main/media/base/media_switches.cc).

## Playwright test browsers

JavaScript/TypeScript tests can import `test` and `expect` from the local
`@agentworks/playwright` package instead of `@playwright/test`. See
[fixture installation and usage](../../packages/playwright/README.md). Existing
custom fixtures can call `attachLiveBrowser(context)` explicitly. Ordinary
Playwright tests do not register themselves. Python sync/async and pytest suites use
[the Python fixture/helper](../../packages/playwright-python/README.md). Both packages
are distributed from the release through authenticated session package endpoints.

The fixture connects to `/s/{session_id}/tools/browser/live` using the runner's
existing MCP bearer credentials. The agent API derives user/workflow ownership
from that server-owned active session and allocates a separate `pw-` session for
each test context. The producer sends Chromium JPEG frames and page metadata,
with no browser command channel and no public CDP port. Producer disconnect or
45 seconds without heartbeat removes its discovery entry and closes viewers.

The existing authenticated session-list/viewer endpoints include these sessions
alongside agent-browser, including deployments using shared Chrome. Viewers see
only their own workflow's test sessions. Control and agent-browser recording
routes are rejected for these sessions on the server; the frontend also hides
those controls. Playwright owns isolated contexts, teardown, and video artifacts
in its test report. No automatic upload into a custom dashboard is performed.

Validation: `AGENTWORKS_PLAYWRIGHT_LIVE_TEST=1 go -C agent_go test ./cmd/server
-run '^TestPlaywrightFixtureLive$' -count=1` runs a real Chromium test against an
isolated local publisher/viewer server.

## Deployment and verification

This uses the shared agent API and workspace service, so the same implementation
works in native/rootless and Docker deployments. Deploy both backend
binaries and the frontend together. Install a streaming-capable agent-browser in
the workspace service's environment; tested with 0.37.0 and headless Chrome.
Existing native installations may need an agent-browser upgrade; presence-only
install checks do not upgrade an older binary. New images install the current
agent-browser package as before.

No dashboard daemon, public browser port, configured CDP endpoint, or local
Chrome is required. `AGENT_BROWSER_CDP_ENABLED=false` remains supported. The
headless browser uses its own internal browser protocol; that is independent of
the app's shared-CDP mode.

The browser connects to `/api/browser/live/{session}/stream` on the normal app
API with the existing login. The agent API checks the workflow and session owner,
then connects to `/api/browser/live/:session/stream` on `WORKSPACE_API_URL`,
carrying `WORKSPACE_API_TOKEN`. The workspace service resolves the session's local
`.stream` metadata and proxies to loopback. This keeps working when the agent API
and workspace service are in separate containers. Reverse proxies must forward
WebSocket upgrades on `/api/` (as for the existing live terminals); ingress must
allow an idle timeout longer than 45 seconds. No session port is published.

The generic workspace proxy rejects this internal stream route. The live viewer
only forwards viewport, tab, URL and connection status messages, and accepts
input only while its connection holds control. Shared desktop/CDP sessions are
not listed; they can contain tabs belonging to other workflows. Discovery exposes only browsers in workspaces the caller
can access.

### Troubleshooting

| Symptom | Check |
| --- | --- |
| No browser sessions appear | Have an enabled agent open the managed browser; a user-start action is proposed above. Confirm that the caller can access the selected workspace and its browser identity. A closed session is not a replayable recording; use the saved artifacts. |
| Session appears but live view cannot connect | Check the agent-browser version, its session `.stream` metadata, and whether streaming is enabled in the workspace service's runtime environment. |
| WebSocket connection fails | Check the existing HTTPS proxy's upgrade forwarding, `WORKSPACE_API_URL`, and matching workspace service tokens. Use Reconnect after correcting the issue. |
| Take control reports busy | Let the current browser action finish, or have the existing controller return control, then retry. |
| Cannot switch tabs while watching | Take control first; switching tabs affects the browser the agent is using. |
| Take control is unavailable or denied | Confirm workflow write access. Session visibility alone does not grant control. |

### Implementation files

- [WorkflowLiveBrowser.tsx](../../frontend/src/components/workflow/WorkflowLiveBrowser.tsx): session discovery, viewport, tab strip, input, and connection lifecycle.
- [WorkflowCapabilitiesPanel.tsx](../../frontend/src/components/workflow/WorkflowCapabilitiesPanel.tsx): embeds the viewer above browser settings.
- [Agent API browser_live.go](../../agent_go/cmd/server/browser_live.go): workflow-scoped discovery, authenticated stream relay, and manual control.
- [live_control.go](../../agent_go/pkg/browser/live_control.go): exclusive control gate shared with managed browser commands.
- [executor.go](../../agent_go/pkg/browser/executor.go): waits on the control gate before running headless browser commands.
- [Workspace browser_live.go](../../workspace/handlers/browser_live.go): resolves local stream metadata and proxies within the workspace environment.
- [workspace_proxy.go](../../agent_go/cmd/server/workspace_proxy.go): prevents bypassing viewer access checks through the generic workspace proxy.

### Existing verification commands

- `cd workspace && go test ./handlers -run TestBrowserLive`
- `cd agent_go && go test ./pkg/browser -run TestBrowserControl`
- `cd agent_go && go test ./cmd/server -run TestLiveBrowser`
- `cd agent_go && RUN_LIVE_BROWSER_E2E=1 go test ./cmd/server -run TestLiveBrowserRealHeadless`

The opt-in integration test launches and closes its own headless browser and
checks live frames, tabs, mouse focus and typing through the workspace proxy.

Frontend validation: `cd frontend && npm run build`.

Historical validation (2026-09-09, agent-browser 0.37.0): real headless
frames, tab discovery, mouse focus, typing, workflow isolation, watch-mode input
blocking, exclusive control, and disconnect recovery. Backend race checks and
desktop/mobile UI checks also passed. Container topology support comes from
routing through the workspace service; it has not been verified by a live
rollout to each deployment.

## Source map and historical rollout notes

- [Conversation ownership resolver](../../agent_go/cmd/server/browser_conversation_isolation.go).
- [Workflow browser runtime](../../agent_go/cmd/server/workflow_browser_runtime.go).
- [Product capability runtime](../../agent_go/cmd/server/agent_profile_runtime.go).
- [Launch/profile configuration](../../workspace/browserconfig/launch.go).
- [Work browser settings](../../frontend/src/products/work/WorkWorkspacePane.tsx).
- [Workflow learning](../workflow/learning_architecture.md).

The former live guide contained RTS releases and rollout observations from
2026-09-09 (`0b9593dd0`, `504c35a5e`, `34a9b1d17`, `66c9a7e07`), including a
legacy global shared-profile deployment and then-pending Dominion/Confida work.
Those are historical evidence in Git history, not statements of current server
status. Deployment work must inspect the actual host and verify the current
per-workspace identity, streaming/control and recording before reporting success.
