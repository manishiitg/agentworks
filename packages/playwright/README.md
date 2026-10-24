# AgentWorks Playwright live view

Use normal Playwright tests with an AgentWorks fixture. It streams Chromium
viewport frames into the existing workflow Browser panel. Agent-browser sessions
remain available alongside these interactive test sessions.

This package is not published to npm yet. From this repository, install it into
your test project with `npm install --save-dev /absolute/path/to/packages/playwright`.
Install `@playwright/test` and its Chromium browser in the same project. Deployed
workflows can download the release archive from
`$MCP_API_URL/tools/browser/packages/node` using their existing bearer token, save
it as `agentworks-playwright.tgz`, and install that local archive. Python suites use
the sibling `agentworks-playwright` Python package and the same Browser panel.

The local backend launcher (`agent_go/run_server_with_logging.sh`) builds both
fixture archives automatically, as does the server release build. To repair an
already running local backend, run this from the repository root (no restart is
needed when the backend uses its default `agent_go/packages` directory):

```sh
python3 scripts/build-playwright-packages.py agent_go/packages
```

`AGENTWORKS_PLAYWRIGHT_PACKAGES_DIR` can point to an existing directory containing
both archives. The launcher validates that override without rebuilding its files.

```js
import { test, expect } from '@agentworks/playwright'

test('checkout', async ({ page }) => {
  await page.goto('https://your-test-site.example/checkout')
  await page.getByRole('button', { name: 'Place order' }).click()
  await expect(page.getByText('Order confirmed')).toBeVisible()
})
```

Run with `npx playwright test` through the workflow's normal shell tool. That
shell supplies `MCP_API_URL` and `MCP_API_TOKEN`; the URL must include `/s/<session>`
or `MCP_SESSION_ID` must also be present. The server derives the user and workflow
from that active session. Never put credentials in tests, URLs or git.

The panel discovers each test context automatically and labels it with its test,
project and retry. It follows the most recently opened page (including popups),
returning to another remaining page when it closes. Watch-only users cannot click,
type or switch the test's tabs. Streaming uses internal Chromium CDP sessions;
no public CDP endpoint or agent-browser process is needed. Frames are limited to
four per second and 1280x720; slow viewers do not block tests.

The fixture leaves Playwright in charge of browser/context teardown. Every test
gets an isolated context. Video recording is enabled by default and Playwright
attaches recordings to its report. Override with `test.use({ video: 'off' })` or
`'retain-on-failure'`. This does not yet insert videos into a custom workflow
HTML dashboard; that dashboard can link to the runner's saved artifacts.

Outside AgentWorks, tests still run and record video without live view; the report
contains a `live-view` annotation. If credentials are supplied but registration fails, setup fails
clearly. If the stream disconnects after setup, the test continues and the report
records a warning. Firefox and WebKit run normally but are annotated as not
supported for live viewing. Retries and parallel tests register separate sessions.

## Existing custom fixtures or manually launched browsers

```js
import { attachLiveBrowser } from '@agentworks/playwright'

const live = await attachLiveBrowser(context, { label: 'My custom suite' })
try {
  // Existing test code; the helper also follows new pages in this context.
} finally {
  await live.stop() // Stops streaming; never closes your browser or context.
}
```

Use the same environment as above. You own browser cleanup and recording setup
when using the attachment helper directly.

## Local verification

```sh
# From packages/playwright:
npm ci
PLAYWRIGHT_SKIP_BROWSER_GC=1 npx playwright install chromium
npm test
# From the repository root, with the sibling Go modules in go.work:
AGENTWORKS_PLAYWRIGHT_LIVE_TEST=1 go -C agent_go test ./cmd/server -run '^TestPlaywrightFixtureLive$' -count=1
```

The live test runs the real fixture against an isolated Go HTTP/WebSocket server,
observes a real Chromium JPEG through the authenticated viewer, checks teardown,
and verifies a Playwright video artifact. It does not use production or paid LLMs.

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
