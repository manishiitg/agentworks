package skills

const agentBrowserSkillContent = `# Agent Browser In Builder

This skill covers interactive managed browsing. For repeatable test suites, call
read_skill(skills=[{"name":"builder-reference","path":"references/playwright-scripted.md"}]).
Use @agentworks/playwright for JS/TS or agentworks-playwright for Python when available;
that reference owns installation, custom fixtures, visibility, and Python compatibility.
Its test sessions use the same Browser panel in watch-only mode. The managed browser
commands, CDP policies, media defaults, and capture rules below apply to agent-browser.

Use the Builder MCP tool ` + "`workspace_browser.agent_browser`" + `. Do not run the ` + "`agent-browser`" + ` CLI directly through shell. Builder's tool owns CDP validation, shared-tab locking, session isolation, and workspace file guards.

## Query Live Mode First

CDP reachability is runtime state. It is never taken from saved conversation
history. Before the first browser action—and again after any availability
error—query Builder's backend-owned status operation:

` + "```python" + `
status = agent_browser("status", [], session="main")
` + "```" + `

` + "`status`" + ` is the connectivity probe. It needs no tab and no ` + "`--cdp`" + `
argument. Do not use ` + "`snapshot`" + ` to test CDP reachability; snapshot reads a
specific page and therefore requires an explicitly owned/selected tab.

If ` + "`effective_mode`" + ` is ` + "`cdp`" + `, use one endpoint from
` + "`authorized_endpoints`" + ` on every later call. If it is ` + "`headless`" + `,
call without ` + "`--cdp`" + `. Do not probe Chrome's ` + "`/json/version`" + ` URL
through shell.

## Load Version-Matched Agent-Browser Skills

The installed CLI bundles skills that exactly match its command surface. Before the first browser action in a task, load the current core overview through the managed tool. In headless mode:

` + "```python" + `
agent_browser("skills", ["get", "core"])
` + "```" + `

In CDP mode, include the configured ` + "`--cdp`" + ` prefix as shown in the CDP wrapper below. Documentation calls do not require a selected tab.

Use ` + "`agent_browser(\"skills\", [\"get\", \"core\", \"--full\"])`" + ` only when the overview does not contain the exact command or flag. Use ` + "`agent_browser(\"skills\", [\"list\"])`" + ` to discover specialized skills and load one only when the task needs it, for example ` + "`dogfood`" + ` for exploratory QA/bug hunts or ` + "`electron`" + ` for desktop Electron apps.

Treat upstream shell examples as logical agent-browser commands. Translate ` + "`agent-browser <command> <args...>`" + ` into ` + "`agent_browser(\"<command>\", [\"<args>\", ...])`" + `; never copy those examples into ` + "`execute_shell_command`" + `.

## Microphone and Camera in the Managed Browser

Managed headless Chrome launches with synthetic microphone/camera devices and automatically accepts media permission prompts. The launch flags are configured centrally and reused by every agent_browser action, live control and recording call; do not add different launch options per command or restart the shared browser to experiment.

For mic-gated flows (such as a learner Start Simulation), stay in agent_browser and verify that navigator.mediaDevices.getUserMedia({audio:true}) resolves to a live audio track on the target HTTPS page. Stop any temporary test tracks afterward. A successful media probe does not prove the application flow succeeded; verify the learner simulation UI itself.

Synthetic audio is test audio, not the user's microphone or generated speech. It can satisfy device checks but cannot conduct a spoken conversation. Use the site's chat input when appropriate; real voice or prerecorded speech needs a separately configured audio source. Browser video capture does not promise microphone audio in its WebM.

If media still fails, inspect the exact error, page permissions and secure context through managed browser commands. Report what is blocked. Do not switch to a standalone Playwright/Puppeteer harness, raw CDP, or shell-launched browser, and do not inject a fake getUserMedia implementation to claim the real media flow works. Externally managed CDP Chrome retains its own media configuration.

## Bundled Recording in Builder

When the user/workflow requests recording or reproduction evidence in managed headless mode, prefer Builder's capture command. It uses the same recording service/state as the Browser view and includes video.webm, network.har (without response bodies), console.json, errors.json, manifest.json and capture.zip.

    agent_browser("capture", ["status"], session="main")
    agent_browser("capture", ["start"], session="main")
    # Reproduce the issue with ordinary managed browser commands.
    agent_browser("capture", ["stop"], session="main")

Open/select the intended page before starting. Keep using the same session. The workspace path is assigned automatically; do not pass a filename. User-browser capture follows the selected-tab live stream, including tab changes. It does not record background tabs simultaneously. Console/errors are exported from the cleared buffers when stopped.

Check status first. If recording is already active, reuse it only as requested and do not claim ownership or automatically stop it. Stop captures you started even when reproduction fails. After a timeout check status before retrying. On stop, inspect recording, validation, errors, directory and files. Only validation=passed establishes decodable nonblank footage; visually verify it matches this run. Never substitute an earlier run’s video. Partial errors must be reported: partial failures can leave recording active and require another stop. Report the actual returned paths. Never mix capture with separate record/HAR start/stop commands during the same capture. Stopping recording does not close the browser or clear sign-ins. Workflow permission errors must not be bypassed through shell.

This is a Builder extension, so upstream skills do not document it. CDP currently uses the separate record, network HAR, console and errors commands. The native record command remains video-only.

## Recording Context Handoff

In CDP mode, upstream ` + "`record start`" + ` creates a fresh temporary browser context/page because video cannot be enabled retroactively on the existing context. Builder detects that page and returns an ` + "`AGENTWORKS_RECORDING_CONTEXT`" + ` notice with its real tab id. Discard all refs from the original tab and call ` + "`snapshot`" + ` immediately. Builder blocks interactions until that snapshot succeeds and automatically routes subsequent page actions to the recorded context even if a stale original tab id was supplied. Do not select or create another tab while recording. On ` + "`record stop`" + `, Builder closes the temporary page, restores the original tab, and requires normal snapshot-before-interaction discipline again.

## HTTP Tool Call Pattern

In code execution mode, call the MCP bridge:

` + "```python" + `
import os
import requests

BROWSER = os.environ["MCP_API_URL"] + "/tools/mcp/workspace_browser/agent_browser"
HEADERS = {
    "Authorization": f"Bearer {os.environ['MCP_API_TOKEN']}",
    "Content-Type": "application/json",
}

def agent_browser(command, args=None, session="main"):
    response = requests.post(
        BROWSER,
        json={"command": command, "args": args or [], "session": session},
        headers=HEADERS,
        timeout=120,
    )
    response.raise_for_status()
    payload = response.json()
    if not payload.get("success", False):
        raise RuntimeError(payload.get("error") or payload)
    return payload.get("result")

` + "```" + `

## CDP Shared Chrome Rules

CDP mode controls the user's real Chrome. The user can see the browser and it may already be authenticated.

Always include an endpoint returned by the latest live status result, such as ` + "`--cdp http://localhost:9222`" + `. Workflow configuration limits which ports can be authorized; live status determines which configured ports are currently reachable. The backend rechecks every auto-mode action and rejects a missing, unavailable, or unconfigured port. When status returns multiple endpoints, they represent independent Chrome profiles/login identities for specialized multi-account testing within this workflow. Choose the intended profile on every call and keep distinct labeled tabs per account. Do not create extra CDP browsers merely because workflows or steps are concurrent.

` + "```python" + `
CDP = ["--cdp", "http://localhost:9222"]

def browser(command, args=None, session="main"):
    return agent_browser(command, CDP + (args or []), session=session)

# Documentation calls are read-only and do not need a selected tab, but still
# carry --cdp so the tool trace remains explicit about this run's mode.
core = browser("skills", ["get", "core"])
` + "```" + `

### Installing a CDP Browser on macOS

If the user wants CDP and no authorized endpoint is available, tell them to
install the default visible Chrome launcher with:

` + "```bash" + `
curl -fsSL 'https://raw.githubusercontent.com/manishiitg/coding-agent-loop/main/scripts/install-chrome-cdp-macOS.sh' | bash
` + "```" + `

For a specialized additional login identity, the same installer accepts a
port and creates a separate app and Chrome profile:

` + "```bash" + `
curl -fsSL 'https://raw.githubusercontent.com/manishiitg/coding-agent-loop/main/scripts/install-chrome-cdp-macOS.sh' | bash -s -- --port 9333
curl http://127.0.0.1:9333/json/version
` + "```" + `

Do not run the installer yourself unless the user explicitly asks you to
install and open a visible local browser. After the endpoint is reachable, the
workflow builder must add it to ` + "`cdp_ports`" + `; the current run cannot
invent or use a port that its prompt did not authorize. Extra profiles are for
specialized multi-login testing, not ordinary workflow concurrency.

Tab handling is mandatory in shared CDP mode:

1. Call ` + "`browser(\"tab\", [])`" + ` first to inspect the current tab list.
2. Reuse the workflow's existing labeled tab first, or a pre-existing tab whose displayed URL exactly matches the target. Tab-list output includes the real ` + "`tN`" + ` id and a query-free URL for this decision.
3. If no suitable tab exists, create one stable labeled tab:

` + "```python" + `
browser("tab", ["new", "--label", "workflow-step-name", "https://example.com"])
` + "```" + `

The backend performs the same exact-URL reuse check atomically before ` + "`tab new`" + `. It returns either ` + "`Reused existing CDP tab`" + ` or ` + "`Created CDP tab`" + ` with the real ` + "`tN`" + ` id and actual URL. Keep that ` + "`tN`" + ` for subsequent calls. If the tab list cannot be refreshed, creation fails instead of risking a duplicate.

4. ` + "`open`" + ` is URL-only. Correct:

` + "```python" + `
browser("open", ["https://x.com/home"])
` + "```" + `

Wrong:

` + "```python" + `
browser("open", ["tab", "t1", "https://x.com/home"])
` + "```" + `

5. After ` + "`open`" + `, include the chosen tab inline for page actions:

` + "```python" + `
snapshot = browser("snapshot", ["tab", "t1", "-i"])
# Example only: use @e1 only if this snapshot identifies it as the intended control.
browser("click", ["tab", "t1", "@e1"])
snapshot = browser("snapshot", ["tab", "t1", "-i"])
# Verify the expected state; resolve any next target from this fresh snapshot.
browser("screenshot", ["tab", "t1", "proof.png"])
` + "```" + `

If a command says shared-browser mode requires selecting or creating a tab, do not treat CDP as unavailable. Select or create a tab, then retry.

Never call the top-level ` + "`close`" + ` command in CDP mode; it can disrupt the user's real Chrome session. At normal workflow completion, leave workflow-created labeled tabs open for review. Builder automatically closes only those registered workflow-owned ` + "`tN`" + ` tabs one hour after the final run releases its browser lease; pre-existing or exact-URL-reused user tabs are never part of that cleanup. Use ` + "`browser(\"tab\", [\"close\", \"<owned-label-or-tN>\"])`" + ` only when the user explicitly requests immediate cleanup or the workflow must replace one of its own labeled tabs. Never close a pre-existing user tab.

## QA Evidence and Network Debugging

Keep these operations inside the Builder ` + "`agent_browser`" + ` tool so CDP tab selection and locking remain enforced:

` + "```python" + `
browser("network", ["tab", "t1", "requests"])
browser("console", ["tab", "t1"])
browser("errors", ["tab", "t1"])
browser("record", ["tab", "t1", "start", "qa-run.webm"])
# reproduce the issue
browser("record", ["tab", "t1", "stop"])
` + "```" + `

HAR files and videos can capture credentials or other visible secrets. Create them only when the user or workflow requested QA evidence and review before sharing.

## Headless Rules

- Managed headless browsing uses one persistent browser per workflow. Its authorized users, builder, runs and steps/groups share it; unrelated workflows have separate browsers. Session labels do not create browsers.
- Tabs are optional: reuse the current/existing tab or create one when useful. Coordinate parallel tasks and re-snapshot before interacting. Do not close/reset the browser, clear storage, or sign out merely because your step finished.
- During capture, other runs wait until the recording stops. Preserve the browser and sign-ins at completion.
- Users can watch and interact in the workflow Browser view. Use workspace view tools to show that view when appropriate.
- Verify login from page content; never assume a session is authenticated.

## Workflow Downloads

In workflow steps, use the run-scoped ` + "`Downloads/`" + ` folder given in the prompt. Do not read from or write to the root workspace ` + "`Downloads/`" + ` folder unless the prompt explicitly grants it.

CDP caveat: native Chrome downloads can land in the host ` + "`~/Downloads`" + ` folder. When the step prompt grants that host path, it may be used to read, stage, or retrieve browser files. Prefer the run-scoped ` + "`Downloads/`" + ` folder for run-owned artifacts, and do not modify unrelated host files.

The live step prompt and folder guard are authoritative. If they grant a host Downloads path, that path is readable for the current run even when an older workflow learning says it is inaccessible. After a native download, inspect the granted host folder for newly created completed files before declaring the download unavailable; do not infer current access from historical learnings.

Use workspace-relative paths for downloads/uploads. Builder securely stages
upload inputs for the persistent daemon and stages explicit ` + "`download`" + `
outputs before publishing them into the authorized run folder. A normal click
in visible Chrome can still download into host ` + "`~/Downloads`" + `, which
remains read-only to the agent.

## Selector Discipline

This is the shared selector contract for builder chats, workflow steps, saved
scripts, and browser learnings.

1. **Snapshot first.** Start with an interactive snapshot, scoped to the relevant
   region when useful. It provides roles, accessible names, state, and temporary
   refs; it does not automatically provide durable CSS selectors. Identify the
   intended control by its meaning and surrounding row/card/dialog, then act
   through the managed browser tool using its current ref.
2. **Fresh refs are valid for live actions.** A ref such as ` + "`@e1`" + ` identifies
   an element in the snapshot that produced it. Do not reuse it across tabs,
   runs, navigation, or DOM updates. Re-snapshot after page changes; if unsure
   whether an action changed the page, refresh before another ref-based action.
   A read-only command alone does not require another snapshot.
3. **Save a locating recipe, never a literal snapshot ref as reusable config.**
   Saved code may resolve a fresh ref from each current snapshot by role, name,
   and context, or use a verified semantic locator or durable DOM attribute.
   Both approaches are valid. Do not require CSS discovery for every step.
   Runtime snapshots may be retained as evidence; their refs must not become
   reusable values in ` + "`main.py`" + `, learnings, db configuration, or KB.
4. **Use read-only eval only when the snapshot is insufficient.** Inspect the
   relevant region for test attributes, semantic id/name, aria-label, labels,
   or other identifying attributes. Eval can execute arbitrary page JavaScript;
   selector discovery must not click, submit, mutate the DOM, or read entered
   credentials. Avoid full-page DOM dumps. Use the installed core skill for
   exact command syntax and keep CDP endpoint/tab arguments on managed calls.
5. **Validate the target, not just the selector syntax.** Prefer meaningful
   role/name or label locators and verified test attributes or semantic id/name.
   Scope repeated buttons to the intended row/card and require one intended,
   actionable match. A test ID or aria-label is not proof of stability: Like
   may become Unlike after an action. Avoid generated IDs, hashed classes, and
   positional CSS/XPath; use structural fallbacks only when necessary and
   document their fragility. If the target is missing or ambiguous, inspect
   again instead of choosing the first match.
6. **Verify the outcome.** Check the expected page or business state after the
   action. A successful click command alone is not a success receipt. For a
   toggle, re-identify the same item through its stable surrounding context and
   inspect its current state before considering any retry. If a click or typing
   reported success but nothing changed, run eval document.visibilityState
   before blaming the selector or the site: Chrome ignores input to a hidden
   page. With the Chrome extension the tab is brought forward automatically, so
   "hidden" means Chrome's window is behind another app; ask the person to bring
   Chrome forward instead of retrying or switching to workarounds.

For example, save "find the card for this action, then its Approve button;
expect status Approved" rather than "click @e12" or "click the third button".
In learnings, record the verified locator or fresh-snapshot resolution recipe,
scope, precondition, expected postcondition, and known state changes. Mark
unverified fallbacks as candidates; one successful run cannot prove permanence.

## Auth Checks

For authenticated sites, verify by page content, not just URL load. For X/Twitter, ` + "`https://x.com/home`" + ` loading is not enough; confirm the authenticated home feed or account UI is visible. A sign-in page means connected but not authenticated.
`
