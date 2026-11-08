// Real Chromium smoke test for the semantic adapter and actual Notify component.
// No backend, credentials, generation or external sends. Full application/backend
// integration and product adapters remain separate rollout gates in PLAT-292.
import assert from 'node:assert/strict'
import { createServer } from 'vite'
import react from '@vitejs/plugin-react'
import { chromium } from 'playwright'

const fixture = { name: 'ui-control-fixture', configureServer(server) {
server.middlewares.use('/__ui-control-test', async (_req, res) => {
  res.setHeader('Content-Type', 'text/html')
  res.end(await server.transformIndexHtml('/__ui-control-test', `<!doctype html><html><body>
  <main data-ui-workspace="Workflow/test" data-ui-view="flow"><div id="root"></div></main>
  <script type="module">
    import React from 'react';
    import { createRoot } from 'react-dom/client';
    import Instructions from '/src/components/workflow/NotificationInstructions.tsx';
    import { applyUIAction } from '/src/platform/ui-control/client.ts';
    const host = document.querySelector('main');
    const root = createRoot(document.getElementById('root'));
    window.testUI = async (opts = {}) => {
      const controller = new AbortController();
      const action = { request_id: 'request', view: 'notify', action: 'expand', target: 'run_summary', expires_at: new Date(Date.now()+1000).toISOString(), ...opts.action };
      if (opts.hidden) host.style.display = 'none';
      if (opts.abort) controller.abort();
      const result = await applyUIAction(action, 'Workflow/test', view => {
        host.style.display = 'block'; host.dataset.uiView = view;
        setTimeout(() => root.render(React.createElement(React.Fragment, null,
          React.createElement('span', { hidden: true, 'data-ui-view-mounted': true }),
          React.createElement(Instructions, { title: 'Run summary', target: 'run_summary', instructions: 'Full instruction content' })
        )), opts.mountDelay ?? 10);
      }, () => ({ view: host.dataset.uiView, revision: 4, visible: true }), controller.signal);
      return { ...result, expanded: document.querySelector('details')?.open ?? false };
    };
  </script></body></html>`))
})
} }
const server = await createServer({ configFile: false, plugins: [react(), fixture], optimizeDeps: { entries: [], include: ['react', 'react-dom/client'] }, server: { host: '127.0.0.1', port: 0 } })
let browser
try {
  await server.listen()
  browser = await chromium.launch({ headless: true })
  const page = await browser.newPage()
  const url = server.resolvedUrls.local[0] + '__ui-control-test'
  const run = async opts => {
    await page.goto(url)
    await page.waitForFunction(() => typeof window.testUI === 'function')
    return page.evaluate(opts => window.testUI(opts), opts)
  }
  assert.deepEqual(await run({ hidden: true, mountDelay: 50 }), { status: 'applied', code: '', expanded: true })
  assert.equal((await run({ action: { expected_state_revision: 3 } })).code, 'stale_state')
  assert.equal((await run({ abort: true })).code, 'inactive_scope')
  assert.equal((await run({ action: { expires_at: '2000-01-01T00:00:00Z' } })).code, 'timeout')
  assert.equal((await run({ action: { target: 'pulse_review' } })).code, 'target_not_found')
  await page.goto(url)
  await page.waitForFunction(() => typeof window.testUI === 'function')
  const interrupted = page.evaluate(() => window.testUI({ mountDelay: 700 }))
  await page.waitForTimeout(100)
  await page.keyboard.press('Escape')
  assert.equal((await interrupted).code, 'user_interrupted')
  console.log('UI control Chromium checks: 6 passed (Notify adapter fixtures; not a deployed-app test).')
} finally {
  await browser?.close()
  await server.close()
}
