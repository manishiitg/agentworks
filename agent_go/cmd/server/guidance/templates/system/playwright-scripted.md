## Playwright suites and live browser viewing

Use this reference when authoring or repairing repeatable browser tests, or connecting
user-written Playwright tests to the workflow Browser panel. For interactive browsing,
use the managed `agent_browser` tool and `references/browser-usage.md`.

To watch a suite, open the workflow Browser panel and select **Follow latest test**.
While following, that option shows the fixture-provided test name, a short run ID,
Live/Replay, and Auto. Each run also has its own named option; selecting it pins
that live browser or replay. The follow option stays available between cases;
the shared agent-browser remains a separate choice. Each test still owns its
browser/context. A `pw-` registration confirms the stream source, not that the
user's panel selected it or displayed frames. Verify the selected test and a
rendered viewport before claiming the user can see it.

### Use the AgentWorks fixtures for browser suites

Keep the suite's language. Use `@agentworks/playwright` for JS/TS, and the
`agentworks-playwright` Python package for Python. Both register interactive Chromium
sessions in the same Browser panel. For a new suite without a required language,
choose the runner that fits the project. For JS/TS, import
its `test` and `expect` instead of `@playwright/test`; run the tests with the normal
Playwright runner through the workflow's shell tool. The library supplies the live-view
fixture and enables Playwright video recording. Keep test assertions and configuration
in the user's project. Respect an explicitly chosen language or runner.

```ts
import { test, expect } from '@agentworks/playwright'

test('checkout', async ({ page }) => {
  await page.goto(process.env.TEST_BASE_URL!)
  await expect(page.getByRole('heading', { name: 'Checkout' })).toBeVisible()
})
```

For JS/TS, verify the project resolves `@agentworks/playwright` and `@playwright/test`;
for Python, verify `agentworks_playwright`, `playwright`, and `websocket` import in
the actual suite interpreter. Verify Chromium is installed and the workflow supplies
`MCP_API_URL` and `MCP_API_TOKEN`. Use the session-scoped API URL or `MCP_SESSION_ID`;
never print credentials or put them in source. Saved steps may receive a child workflow
session URL; use it unchanged. The server resolves its registered parent owner for
both package downloads and live streaming. A successful fixture registration
confirms server support; installed source or a planned release does not.

Both packages ship with the deployed release and are not published on npm/PyPI.
Download the matching archive through the authenticated session bridge into an
existing authorized dependency directory. For saved Python steps:

```sh
curl --fail --silent --show-error -H "Authorization: Bearer $MCP_API_TOKEN" \
  "$MCP_API_URL/tools/browser/packages/python" \
  -o "$WORKFLOW_CODE_DEPS/agentworks-playwright-python.zip"
python3 -m pip install --target "$WORKFLOW_CODE_DEPS" --no-deps --upgrade \
  "$WORKFLOW_CODE_DEPS/agentworks-playwright-python.zip"
```

Preserve existing Playwright versions and browser installations. The Python archive
needs `playwright` and `websocket-client`; install missing dependencies into the same
environment after checking the suite's pins, rather than upgrading its browser stack.

For JS/TS, use `/tools/browser/packages/node`, save as `agentworks-playwright.tgz`,
and install that archive with `npm install --save-dev` in the test project.
Use the actual session-scoped MCP_API_URL (or construct `/s/<MCP_SESSION_ID>` when
only a base URL is supplied). Install once per package update, not on each run.
Do not print credentials, try a public-registry install for these private packages,
or invent host paths. A missing package endpoint is a release prerequisite;
report it rather than deploying automatically. A successful registration confirms
live support; source code or a planned release does not.

### Python fixtures and existing scripted harnesses

For pytest-playwright sync tests, add this to the root `conftest.py`:

```python
pytest_plugins = ["agentworks_playwright.pytest_plugin"]
```

Tests using `page` or `context` then attach automatically. Preserve existing plugins
and custom fixtures; add this entry rather than replacing their conftest. The plugin
requires pytest-playwright. For custom contexts/new_context(), async pytest fixtures,
or a saved `main.py` harness, attach the actual context explicitly:

```python
from agentworks_playwright import attach_live_browser

with attach_live_browser(context, label="Login regression"):
    # Existing page actions and assertions; keep existing context creation.
    run_case(page)
```

For async Playwright use `async with await attach_live_browser_async(context, label=...)`.
If a context manager does not fit, use `live = attach_live_browser(context)` and
`live.stop()` in `finally` (await both calls for the async helper). Each new context
needs its own attachment. The helpers stop streaming only; the harness still closes
its own context/browser and owns record_video_dir. Pytest retains its runner-owned
lifecycle and recording policy (`--video=on` or `--video=retain-on-failure`). Do not
replace existing evidence paths or enable duplicate recording systems.

Preserve sync vs async execution. Playwright objects stay on their caller's thread;
do not drive them from a background thread. For sync browser waits use Playwright's
wait methods instead of long time.sleep calls so live events are dispatched.

### Ownership, visibility, and evidence

- Playwright Test owns its browser lifecycle and a fresh context for each test.
  Do not share live pages across tests or manually close the runner's browser.
  Configure bounded test/action/navigation timeouts and retain assertion failures.
- The fixture registers a separate test context in the same Browser panel as
  agent-browser. It follows the newest page, returning to a remaining page on close.
  Builders can click, type, navigate and select tabs while the script runs. Shared Run users watch. Managed agent-browser capture remains separate.
- When foreground workspace-view tools are available, open the Browser view once
  when starting live tests; an `applied` receipt confirms the panel opened, not that
  a test registered. Confirm the test session/frame before claiming live visibility.
  Unattended runs must not open foreground UI.
- Playwright saves recordings in its own results/report. It does not automatically
  upload them to a workflow dashboard. Link or persist actual returned artifacts
  under the workflow's authorized durable evidence location when required.
- Retries and parallel tests have separate registrations. The fixture stops streaming
  at teardown. Registration failures fail setup; a later disconnect leaves tests
  running and records a warning. Do not hide failed registration by claiming success.
- Without workflow credentials the Node/pytest
  fixtures run tests without live viewing. Explicit helpers require credentials. Firefox/WebKit remain ordinary tests; live viewing supports Chromium.

### Existing suites and custom fixtures

For existing JavaScript/TypeScript suites, preserve fixtures, assertions, configuration,
and ownership. Integrate `attachLiveBrowser(context)` into the existing fixture when
replacing its `test` import would discard custom fixtures. Call `await live.stop()` in
`finally`; this helper stops streaming and never closes the caller's browser/context.
The caller remains responsible for recording when using the helper directly.

Adapt existing Python suites through their actual shared context setup and teardown;
do not rewrite them in JavaScript. Ordinary tests without a fixture/helper still do
not register automatically. Python HTML rendering scripts need an attachment only
when live viewing is part of the request. Live registration does not require a
public CDP port or the managed agent-browser process.

### Existing Python scripted steps

A saved scripted step still executes `main.py` using the Python runner. It may drive
Python Playwright directly. The following rules apply to that manually owned harness;
JS/TS Playwright Test and pytest-playwright use their runner-owned fixture lifecycles above. Do not replace
`main.py` with a TypeScript entry point or change the saved-step execution contract.

### Runtime and dependencies

- Install Python packages once into `$WORKFLOW_CODE_DEPS`; never run `pip install` on every test run.
- Resolve the browser from `AGENT_BROWSER_EXECUTABLE_PATH` when supplied. Otherwise use a Playwright-installed browser from the workflow's persistent dependency/cache directory. Never hardcode a host-specific binary or browser version.
- Preserve the platform's short private `TMPDIR`, `TMP`, and `TEMP`. Chromium profile/socket paths have a small Unix length limit; never put the profile under a deep step path or use a process-shared fixed profile.
- Treat `VAR_*`, `SECRET_*`, `STEP_OUTPUT_DIR`, `DB_PATH`, `WORKFLOW_CODE_ROOT`, and `WORKFLOW_CODE_DEPS` as injected runtime inputs. Required values use `os.environ[...]`; do not rediscover the workflow root from directory depth.

### Isolation and ownership

- The suite owns `sync_playwright()`, each browser process, context, page, and cleanup.
- Start a fresh browser process and fresh context for every case. Do not add a `shared_browser` flag or reuse one page/context across a suite. A wedged page must not block every later case.
- Browser isolation does not imply application-session destruction. If the tested service has a server-side session, reconnect to it deliberately in the next case. Persist Playwright `storage_state` only when authentication reuse is intentional; never share live page objects.
- Close context, browser, and Playwright in `finally`. Clean up an external paid/GPU session once, in a dedicated final case or an idempotent suite finalizer.

### Bounded execution

- Set explicit Playwright action and navigation timeouts. Also give every case a wall-clock watchdog; Playwright action timeouts alone do not cover every protocol/browser hang.
- A case timeout must stop that case's browser process group, finalize its evidence/status, and let the harness either continue safely or fail the suite. The outer workspace timeout is a last-resort harness kill, not the normal case timeout.
- Use bounded infrastructure retries only with a brand-new browser. Do not retry assertion failures, and record every attempt.

### Progress, results, and provenance

- Create the suite run record first. Before launching each case, persist a `running` case record and print a flushed progress line naming the case and operation.
- Print progress before/after browser launch, context/page creation, navigation, authentication, and every major test phase. Stdout/stderr are the repair agent's primary live evidence.
- In `finally`, move each case to a terminal status and close the suite record. A later reconciliation pass may mark only an expired-heartbeat record as `harness_timeout`; an LLM must not manually rewrite a fresh running row.
- At run start, SHA-256 hash `main.py` and every workflow-owned helper imported by the suite. Store the manifest with the run summary/evidence. “main.py was unchanged” is not proof that the executed code was unchanged.
- Store screenshots, video, console/network logs, current URL, last completed operation, and the exception/traceback on failure. Redact secrets. Durable evidence belongs under `db/assets/` with a DB/report reference; `$STEP_OUTPUT_DIR` is run-volatile.

### Failure classification

Classify the terminal cause precisely: `assertion_failure`, `setup_failure`,
`browser_crash`, `case_timeout`, `harness_timeout`, or `cancelled`. A process that emitted
progress and was later killed did run; never report that the workspace “refused to start” it.
Do not weaken assertions or rewrite product code because of an infrastructure failure. Before
claiming code was unchanged, compare the stored source manifest, including shared helpers.

### Verification

Run syntax/import and dependency/browser preflight checks first. Validate the harness against a
small local fixture that exercises pass, assertion failure, and timeout finalization. Use
`execute_step(fast_path_only=true)` for the real selected group only when a live run is authorized;
do not launch an expensive or externally mutating suite merely to prove the harness compiles.

## Temporary Browser-panel replay

The shared live-browser service automatically records the streamed viewport for
both Node.js and Python, including direct attach helpers. After teardown the
Browser panel offers MP4 playback and Download video. This is a silent replay of
the live view (up to 4 fps), independent of the runner's full-quality video policy.
No changes to existing context creation, assertions, or report evidence paths are
needed. Closing the panel deletes its temporary replays, including an in-progress
recording; download anything to keep first. Abandoned replays expire after one hour.
A bounded recording that reaches the size limit is labeled partial. If recording
capacity or encoding fails, the panel reports it; do not claim a replay exists until
it is ready. Report-owned evidence is not deleted by closing the Browser panel.


### Unattended runs

Live viewing is always on, in Builder chats and in schedules, function calls,
webhooks and bots alike; there is no switch to turn it off. Builders may interact with the live browser while the script keeps running. Keep runner video/trace/screenshot settings and assertions unchanged.
