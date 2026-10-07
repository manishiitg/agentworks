<!-- Chrome extension uses the same tool and backend-owned workspace scope. -->
When `agent_browser(command="status")` reports `effective_mode: "extension"`,
use ordinary `agent_browser` commands without `--cdp`. The connected Chrome
extension exposes only tabs shared by the current account in this workspace.
Reads, snapshots, navigation, tab selection and new tabs stay in the background.
Clicks and typing need a visible page (Chrome ignores input to a hidden tab), so
the extension brings their tab to the front of its window, and restores a
minimized window, before sending them. To check whether the selected tab is
visible, run `eval document.visibilityState` (`visible` or `hidden`). If a click
or typing has no effect and the tab reads `hidden`, Chrome's window is behind
another app: say so and ask the person to bring Chrome forward rather than
retrying. Set the optional tool parameter `active: true` only when intentionally
bringing a shared tab forward for the person to see, for example
`command="tab", args=["t1"], active=true`. It applies to that call only. Inline `tab tN` actions reuse the selected tab;
after switching to a different tab, take a fresh snapshot before using refs.
Console/errors are scoped to the selected shared tab. Screenshots require an
explicit project-relative path inside your granted writable workspace, such as
`<project-workspace>/evidence/screenshot.png`; `/tmp` and global
`tool_output_folder` destinations are outside that grant.
Read a fresh snapshot before acting. A disconnected extension must be reconnected
by the user; do not switch to another browser, restart Chrome, or recover through
shell commands. `skills list` and `skills get core` (including `--full`) read
the installed CLI documentation on the server; they do not require a shared tab
or connect to Chrome. You can also read this attached platform guide through
`read_skill(skills=[{"name":"builder-reference","path":"references/browser-usage.md"}])`
and the attached `agent-browser` skill through
`read_skill(skills=[{"name":"agent-browser"}])`. Documentation reads do not
prove a browser is connected; keep the live-status check before page actions.
Extension 0.4.4 supports `record start <project-workspace>/evidence/video.webm`
(or `.mp4`), an optional HTTP(S) URL and `--fps 1-60`, then `record stop`.
It records the existing selected shared tab, retaining login and page state;
stop before selecting/creating another tab. The workspace must provide
agent-browser 0.38.2 and ffmpeg with the matching encoder. Output uses the same
workspace grants and private staging as screenshots. There is no microphone or
desktop audio, cursor overlay, contact sheet, restart or bundled capture/HAR.
A debugger loss interrupts that take; do not present it as verified evidence.
Network/HAR, trace, profiler, local upload/download transfer and teaching are not
available through this connection. This status overrides local-port setup advice
below, including on deployments where local operator CDP is disabled.

## Browser Automation

Read this skill for interactive managed browsing when you need to drive a real browser — open pages, click,
fill forms, take screenshots, upload files, scrape interactive sites, or
log into authenticated pages. Browser configuration is declared by the
workflow, but CDP reachability is live state. Query `agent_browser status`
before first use instead of relying on saved conversation or prompt state.

For repeatable browser tests, read `references/playwright-scripted.md` from
`builder-reference`. Use `@agentworks/playwright` for JS/TS or `agentworks-playwright` for Python
when available. Its live test sessions share this panel but are watch-only; the
managed commands, CDP policies, media defaults, and capture rules below apply to
agent-browser sessions. Do not apply them to a test runner's private Chromium.

In an interactive Builder with workspace-view tools, call
`perform_ui_action(action="open", view="browser")` when starting browser work so the user
can watch the live server browser in the right-hand pane. Only an `applied`
receipt confirms the UI opened. Open the view once; do not switch it back after
every command if the user chose another view. The live stream follows navigation
and tab changes automatically, so no workspace refresh is needed. Users can
choose **Take control** to interact. Scheduled or unattended runs must not try
to open the foreground UI.

`status.cdp_supported` is the deployment authority. When it is `false`, CDP
is disabled on that server: do not probe ports, install/launch CDP Chrome, or
pass `--cdp`. Use managed headless Chromium. In that deployment, workflow
`auto` mode is intentionally headless-only and `cdp`/`cdp_ports` updates are
rejected. Desktop/local deployments may still report CDP as supported.

## Runtime modes

| Mode | Browser | Visibility | Logins / cookies |
|---|---|---|---|
| **Extension** (`agent_browser`, no `--cdp`) | The account's connected Chrome or Edge extension | Background by default; `active=true` opts into foreground | Existing cookies + sessions on shared project tabs |
| **CDP** (`agent_browser` with `--cdp`) | The user's real Chrome via Chrome DevTools Protocol | User sees every action | Existing cookies + sessions are available — leverage them |
| **Headless** (`agent_browser`) | Server-side Chromium | Live in the workflow Browser view; screenshots also available | User-owned browser shared across chats and workflow steps |

The CDP row is unavailable whenever live status reports
`"cdp_supported": false`; this is a deployment policy, not a transient
connection failure.

## Version-matched agent-browser skills

First call the managed live-status operation. It does not launch a browser and
does not require `--cdp`:

```python
status = browser_raw("status", [])
```

Use `effective_mode` and `authorized_endpoints` from that response to set the
prefix for later calls. In extension mode the prefix is empty; documentation
reads are server-side and independent of tab availability. Then load the
installed CLI's current core overview:

```python
browser("skills", ["get", "core"])
```

The helper below automatically prepends the configured CDP endpoint in CDP
mode. Use `browser("skills", ["list"])` to discover specialized skills, and
load one only when needed—for example `dogfood` for
exploratory QA/bug hunts or `electron` for Electron desktop applications.
Use `core --full` only when the overview lacks an exact command or flag.
Upstream skill examples use shell syntax; translate them into managed
`agent_browser` tool calls. Never execute browser actions through shell.

For selector discovery and persistence, read the **Selector Discipline** section
of the attached `agent-browser` skill. Start with a snapshot and use fresh refs
for live actions. Saved automation may resolve fresh refs at runtime or use
verified durable locators; read-only `eval` is a fallback when the snapshot is
insufficient. Do not force CSS discovery for every step.

## `agent_browser` (CDP and headless)

Call via HTTP API. CDP sessions need an exact
`--cdp http://<host>:<port>` endpoint returned by live status on every later
call. The backend validates the configured port, rechecks auto-mode
reachability, and canonicalizes the host; a headless run cannot switch itself
to a model-selected CDP endpoint.

```python
import json, requests, os
BROWSER = os.environ["MCP_API_URL"] + "/tools/mcp/workspace_browser/agent_browser"
HEADERS = {"Authorization": f"Bearer {os.environ['MCP_API_TOKEN']}", "Content-Type": "application/json"}
def browser_raw(command, args=None, session="default"):
    resp = requests.post(BROWSER, json={"command": command, "args": args or [], "session": session}, headers=HEADERS, timeout=120)
    resp.raise_for_status()
    return resp.json().get("result", "")

status = json.loads(browser_raw("status"))
# Headless: []. CDP: ["--cdp", first live authorized endpoint].
# Never invent or probe another endpoint.
BROWSER_PREFIX = (
    ["--cdp", status["authorized_endpoints"][0]]
    if status["effective_mode"] == "cdp" else []
)

def browser(command, args=None, session="default"):
    return browser_raw(command, BROWSER_PREFIX + (args or []), session)

# Standard flow
browser("open", ["https://example.com"])
snap = browser("snapshot", ["-i"])      # interactive elements as @e1, @e2, ...
# Example only: choose the intended control from the actual snapshot.
browser("click", ["@e1"])
snap = browser("snapshot", ["-i"])      # refresh after page changes
# Verify the expected result; resolve the next target from this fresh snapshot.
browser("screenshot", ["page.png"])
```

**Key commands (check the selected mode's supported features):** `skills`, `open`, `snapshot`, `click`, `fill`, `type`, `press`,
`screenshot`, `wait`, `get`, `scroll`, `select`, `hover`, `upload`,
`download`, `eval`, `network`, `console`, `errors`, `record`, `trace`,
`profiler`, `close`, `back`, `forward`, `reload`, `reset`.

For exhaustive command docs call `browser("skills", ["get", "core", "--full"])`.

### CDP-specific rules

- The inline prompt may authorize multiple `--cdp` endpoints only for a
  specialized multi-login workflow. Each endpoint represents a different
  Chrome `--user-data-dir`/login identity. Choose the intended endpoint on
  every call and keep distinct labeled tabs per account. Normal concurrent
  workflows should share one CDP browser.
- `open` is URL-only: `browser("open", ["https://target"])`. Do **not** pass
  `["tab", "t1", url]` to `open`.
- For every page action after `open` (snapshot/click/fill/eval/wait/screenshot),
  include `["tab", "<tab-id-or-label>", ...]` inline so the action runs against
  a known tab and respects the shared CDP lock.
- Call `browser("tab", [])` to list real tab ids and query-free URLs. Reuse the workflow's existing owned tab or an exact target-URL match; if neither exists:
  `browser("tab", ["new", "--label", "<workflow-label>", "https://target"])`.
- `tab new` performs the exact-URL reuse check again atomically and returns the real `tN` id plus actual URL. If listing is unavailable, it refuses creation instead of risking a duplicate.
- Never call the top-level `close` command in CDP mode; it can disrupt the user's real Chrome session.
- At normal workflow completion, leave workflow-created labeled tabs open for review. The backend automatically closes only their registered real `tN` ids one hour after the final run releases its browser lease; it never includes pre-existing or exact-URL-reused user tabs.
- Use `browser("tab", ["close", "<owned-label>"])` only when the user explicitly requests immediate cleanup or the workflow must replace one of its own labeled tabs. Never close a pre-existing user tab.
- The backend avoids a redundant tab switch when the requested CDP tab is
  already active, so normal actions should not repeatedly steal OS focus.
  Creating or genuinely switching tabs can still bring Chrome forward; avoid
  unnecessary tab changes while the user is typing. For unattended schedules,
  prefer headless mode.
- Keep diagnostics inside `agent_browser`; raw CDP bypasses the shared-tab
  lock and is not allowed for normal navigation or debugging.

### Installing a CDP browser on macOS

When the user wants CDP but no configured endpoint is available, provide the
dedicated installer command. Do not run it yourself unless the user explicitly
asks you to install and open a visible local browser:

```bash
curl -fsSL 'https://raw.githubusercontent.com/manishiitg/coding-agent-loop/main/scripts/install-chrome-cdp-macOS.sh' | bash
```

For a specialized additional login identity, pass an unused port to the same
installer. It creates a separate app and `--user-data-dir`:

```bash
curl -fsSL 'https://raw.githubusercontent.com/manishiitg/coding-agent-loop/main/scripts/install-chrome-cdp-macOS.sh' | bash -s -- --port 9333
```

After installation, agents verify reachability with `agent_browser status`;
they must not probe Chrome's `/json/version` endpoint through shell.

After it is reachable, configure the workflow with that port, for example
`cdp_ports=[9222,9333]`. Do not add profiles for ordinary workflow concurrency.

### QA evidence and network debugging

- Requests: `browser("network", ["tab", "<label>", "requests"])`.
- Console/errors: `browser("console", ["tab", "<label>"])` and
  `browser("errors", ["tab", "<label>"])`.
- HAR capture: `browser("network", ["tab", "<label>", "har", "start"])`,
  reproduce, then stop to a workspace-relative file. HAR files can contain
  authorization headers, cookies, and response bodies; review before sharing.
- Video evidence: `browser("record", ["tab", "<label>", "start", "qa.webm"])`,
  reproduce, then `browser("record", ["tab", "<label>", "stop"])`. Recording
  must be requested by the user/workflow because visible secrets may be captured.

### Headless-specific rules

- One managed headless browser belongs to each signed-in user. Builder, chats, workflow steps and groups share it; other users have separate browsers. Session labels do not create browsers.
- Tabs are optional: reuse the current/existing tab or create one when useful. Coordinate parallel work and take fresh snapshots before interaction. Do not close/reset the browser, clear storage, or sign out merely because your step finished.
- The user can watch through the Browser workspace view.
- Use `browser("reset")` only when the daemon is genuinely broken; otherwise
  it wastes time.

## File uploads

- Use `agent_browser`:
  `browser("upload", ["@ref", "Downloads/report.pdf"])` (CDP also takes the
  tab inline: `["tab", "t1", "@ref", "Downloads/report.pdf"]`).

Always use **workspace-relative paths** (e.g. `Downloads/report.pdf`,
`Chats/output.csv`). Do not construct absolute paths yourself. The backend
checks the current read grants and stages uploads for the persistent daemon.
To upload a file you generated, write it within the current session's
authorized output space, then pass its workspace-relative path. `Chats/` is
for chats that have that grant; a workflow step uses its own execution output
space or `db/assets/` for a durable file, not a builder-only folder.

For an explicit managed download, pass a workspace-relative authorized output
path: `browser("download", ["@ref", "Downloads/report.pdf"])` (plus the CDP
tab inline when applicable). The backend stages and publishes the completed
file. A normal click in visible Chrome may instead download into the host
Downloads folder. Use only the host Downloads grants actually provided to
this session; do not assume that workspace-relative `Downloads/` names the
same directory.

## Microphone and Camera in the Managed Browser

Managed headless Chrome launches with synthetic microphone/camera devices and automatically accepts media permission prompts. The launch flags are configured centrally and reused by every agent_browser action, live control and recording call; do not add different launch options per command or restart the shared browser to experiment.

For mic-gated flows (such as RTS learner Start Simulation), stay in agent_browser and verify that navigator.mediaDevices.getUserMedia({audio:true}) resolves to a live audio track on the target HTTPS page. Stop any temporary test tracks afterward. A successful media probe does not prove the application flow succeeded; verify the learner simulation UI itself.

Synthetic audio is test audio, not the user's microphone or generated speech. It can satisfy device checks but cannot conduct a spoken conversation. Use the site's chat input when appropriate; real voice or prerecorded speech needs a separately configured audio source. Browser video capture does not promise microphone audio in its WebM.

If media still fails, inspect the exact error, page permissions and secure context through managed browser commands. Report what is blocked. Do not switch to a standalone Playwright/Puppeteer harness, raw CDP, or shell-launched browser, and do not inject a fake getUserMedia implementation to claim the real media flow works. Externally managed CDP Chrome retains its own media configuration.

## Bundled Recording in Builder

When the user/workflow requests recording or reproduction evidence in managed headless mode, prefer Builder's capture command. It uses the same recording service/state as the Browser view and includes video.webm, network.har (without response bodies), console.json, errors.json, manifest.json and capture.zip.

    agent_browser("capture", ["status"], session="main")
    agent_browser("capture", ["start"], session="main")
    # Reproduce the issue with ordinary managed browser commands.
    agent_browser("capture", ["stop"], session="main")

Open/select the intended page before starting. Keep using the same session. The workspace path is assigned automatically; do not pass a filename. User-browser capture records the selected-tab live stream and follows tab changes. It does not record background tabs simultaneously. Console/errors are exported from the cleared buffers when stopped.

Check status first. If recording is already active, reuse it only as requested and do not claim ownership or automatically stop it. Stop captures you started even when reproduction fails. After a timeout check status before retrying. On stop, inspect recording, validation, errors, directory and files. Only validation=passed establishes decodable nonblank footage; visually check it matches this run before presenting it. Never substitute an earlier run’s video. Partial errors must be reported: partial failures can leave recording active and require another stop. Report the actual returned paths. Never mix capture with separate record/HAR start/stop commands during the same capture. Stopping recording does not close the browser or clear sign-ins. Workflow permission errors must not be bypassed through shell.

This is a Builder extension, so upstream skills do not document it. CDP currently uses the separate record, network HAR, console and errors commands. The native record command remains video-only.

## Session limits

- Default per-agent / per-workflow / global concurrency caps are enforced
  by the runtime — keep one browser open at a time per agent. Re-use the
  same session name across calls within one task.
- In managed headless mode, all names map to the workflow browser shared by its authorized users; preserve its tabs and sign-ins. Unrelated workflows use separate browsers. Tabs are optional. In shared CDP
  mode, sessions are intentionally remapped to one per port; isolation comes
  from workflow-owned labeled tabs plus the per-port select-and-act lock.

## `wait` for a fixed delay takes a bare number, not a `--ms` flag

To pause for a fixed duration (e.g. human-pacing between actions), pass the
millisecond count as a **bare positional argument** —
`browser("wait", ["1800"])` — not `--ms 1800`. `--ms` is not a real flag on
this tool at all; passing it does not error out or get ignored cleanly, it
makes the call fall through to a different, unrelated wait mode that then
fails with a misleading `Wait timed out after 25000ms` after a fixed ~25
second internal ceiling — regardless of the number that was supposed to
follow `--ms`. This has been directly reproduced: `wait --ms 1800` times out
after 25000ms; `wait 1800` (bare) returns `Done` in ~1.8s, exactly as
requested.

If a wait call times out after ~25000ms even though a short delay was
intended, check the call for `--ms` first before assuming the browser or
target page is unresponsive — it usually means the flag form was used by
mistake. The tool's other wait modes take a selector, or one of
`--url <pattern>`, `--load <state>`, `--fn <expression>`, `--text <text>`,
`--download [path]` — a bare number is specifically the fixed-delay form.

## A click response proves the command completed, not that the target's state changed

`agent_browser click`'s `success` field means the click *command* completed
successfully — it does not itself prove the click event was genuinely
delivered to the page or that anything changed as a result. On
toggle-style controls (like, follow, and similar state-flip buttons), a
successful click command can return `success=true` while the live DOM's
`aria-label`/`data-testid` on that exact element never changes: the target
still reads unliked/not-following after the call returns. Never treat a
click response alone as proof a toggle action landed, especially before
writing a durable receipt for a real public action.

Verify and recover with the same recipe five independent findings converged
on:

1. Dispatch the click.
2. Wait for the settle delay, then take a *fresh* scoped read of the same
   control — a new `snapshot`/`get` of the surrounding region, not a
   reused `@eN` reference from before the click. DOM updates can invalidate
   refs; an old ref does not establish the current state of the intended
   control. On these controls,
   `data-testid` is itself part of the state — X swaps a like button's
   `data-testid` between `"like"` and `"unlike"` as the component re-renders
   — so it cannot double as the stable selector used to re-find the
   element; locate the intended item by stable surrounding identity/context
   and its control by role/name, avoiding position alone, then read both its current
   `aria-label` and `data-testid` and check they now hold the expected
   post-action values.
3. If fresh evidence confirms the action did not land and a retry remains
   authorized and appropriate, retry at most once through the managed click
   command using the newly resolved target, then verify again. Do not blindly
   repeat a toggle or submit when its outcome is uncertain; do not treat a
   read-only discovery probe as permission to click through JavaScript.
4. If still unchanged, do not record success. Persist the attempt as failed
   with the observed before/after state — do not fabricate a landed action
   from a command-success response alone. A concurrent rate limit (HTTP 429
   / X `code:88` on the account-settings diagnostic) is one real cause and
   is itself worth recording, not silently retried away.

This is the current mitigation, not a claim that no platform-side fix could
ever exist: `agent_browser` is a third-party CLI with no vendored source in
this platform, so there is no click-mechanism internals to patch today, but
the owned wrapper around it could eventually offer an opt-in click-and-verify
operation that takes a caller-supplied postcondition. Until that exists, this
verify/recover recipe is the available mitigation.

## Common mistakes

- Calling `open` with `["tab", "t1", url]` in CDP — `open` is URL-only.
- Calling top-level `close` in CDP mode, or manually closing a pre-existing user tab. Workflow-created labeled tabs are cleaned up automatically after the one-hour review window.
- Reusing snapshot refs after navigation, DOM updates, or switching tabs.
  Refresh after page changes, and before a ref-based action when freshness is uncertain.
- Connecting directly to CDP WebSocket for click/fill/navigate — bypasses
  the shared tab lock and races with other workflows.
- Omitting the configured `--cdp` prefix or inventing another port — the
  backend rejects the call to keep prompt and runtime configuration aligned.
- Hardcoding absolute paths in `upload` — always use workspace-relative.
