# AgentWorks Python Playwright live view

Use your existing Python Chromium tests with the same interactive Browser panel as
Node tests and agent-browser. Both sync and async contexts are supported; neither
helper closes your browser or changes assertions, launch arguments, or recordings.

Install this private package from a supplied archive or checkout:

```sh
python3 -m pip install /path/to/packages/playwright-python
# pytest users also need pytest-playwright:
python3 -m pip install '/path/to/packages/playwright-python[pytest]'
```

For deployed workflows, the authenticated session tool route
`$MCP_API_URL/tools/browser/packages/python` supplies `agentworks-playwright-python.zip`.
Download it into an authorized workflow directory using the existing bearer token,
then install once into the suite's persistent dependencies (`--target "$WORKFLOW_CODE_DEPS"`
for saved Python steps). This package is not published on PyPI.

## Existing sync Python harnesses

```python
from agentworks_playwright import attach_live_browser

# Keep your existing context creation and record_video_dir configuration.
with attach_live_browser(context, label="Login regression") as live:
    page = context.new_page()
    page.goto(test_url)
    # Existing assertions.
# Live view has stopped. You still own context/browser cleanup.
```

Registration uses MCP_API_URL and MCP_API_TOKEN from the workflow shell; the URL
must include `/s/<session>` or MCP_SESSION_ID must also be supplied. Never print
credentials. Explicit helpers require registration and raise on setup failure;
a later connection failure leaves the test running and is available in `live.warning`.
Use `try/finally: live.stop()` if a context manager does not fit your harness.

## Pytest fixture

With pytest-playwright installed, add to the root conftest.py:

```python
pytest_plugins = ["agentworks_playwright.pytest_plugin"]
```

Existing tests using `page` or `context` are attached automatically. Custom contexts
created with `new_context()` need an explicit helper for each context. Tests without
browser fixtures are untouched. In offline environments the plugin runs tests
without live viewing and records a test property. Partial credentials or failed registration fail setup.
Firefox/WebKit remain normal tests, annotated as unsupported for live viewing.

The plugin uses sync pytest-playwright fixtures. Use the async helper in your own
async fixture for async pytest suites. Enable saved recordings with the runner's
`--video=on` or `--video=retain-on-failure`; recording policy remains yours.

## Async contexts

```python
from agentworks_playwright import attach_live_browser_async

async with await attach_live_browser_async(context, label="Async login"):
    page = await context.new_page()
    await page.goto(test_url)
```

All Playwright calls stay on the caller's thread/event loop. A bounded transport
worker transports copied viewport frames, tab metadata and input messages, at up to four frames
per second. Sync Playwright dispatches events during Playwright calls: use its
wait methods instead of long time.sleep calls when awaiting browser changes.
Latest frames are retained even if the screen becomes static. Popups are followed;
closing one returns to a remaining page. Stops are idempotent and remove the session.

Builders can interact directly with the test browser. Agent-browser recording is separate from the test runner recording. Store
actual test-runner screenshots/videos under your authorized evidence directory;
there is no automatic workflow-dashboard upload.

Local validation: install the pytest extra in `.venv`, install Chromium with
`PLAYWRIGHT_SKIP_BROWSER_GC=1 .venv/bin/python -m playwright install chromium`,
and run `.venv/bin/python -m pytest tests/test_connection.py`.
From the repository root, `AGENTWORKS_PLAYWRIGHT_LIVE_TEST=1 go -C agent_go test
./cmd/server -run '^TestPythonPlaywrightFixtureLive$' -count=1` verifies real sync,
async, and pytest frames, popup return, recordings, and failure cleanup.

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

Live viewing is always on: schedules, function calls, webhooks and bots stream
to the Browser panel the same way Builder runs do. Builders can interact while the test runs; the helper never
changes video, trace, screenshot settings or assertions.

Builders can click, type, paste, navigate and select live tabs while tests keep running. Shared Run access can watch but cannot send input. Install package version 0.2.0 from the platform to enable this protocol. Python input runs on the original Playwright event loop; use Playwright waits instead of blocking time.sleep so browser events can run.
