// Chromium integration against the real compiled JSON renderer, official
// preview runtime, and an HTML bundle emitted by the real workspace store.
// The project-data transport is a fixture; this is not deployed-account proof.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { readFile, writeFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import path from 'node:path'
const require = createRequire(process.env.DASHBOARD_BROWSER_REQUIRE || import.meta.url)
const { chromium } = require('playwright')
const root = process.env.DASHBOARD_JSON_QA_DIR
assert(root, 'Set DASHBOARD_JSON_QA_DIR to the isolated Linux proof directory')
const html = await readFile(path.join(root, 'fixture.html'), 'utf8')
let queries = 0, scripts = 0, total = 42
const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost')
  const file = url.searchParams.get('path')
  if (url.pathname === '/report-preview/report-preview.js') { res.setHeader('Content-Type', 'text/javascript'); res.end(await readFile(path.join(root, 'report-preview.js'))); return }
  if (/^\/report-preview\/dashboard-json\.(js|css)$/.test(url.pathname)) { res.setHeader('Content-Type', url.pathname.endsWith('.css') ? 'text/css' : 'text/javascript'); res.end(await readFile(path.join(root, 'frontend/dist', path.basename(url.pathname)))); return }
  if (url.pathname.endsWith('/file')) {
    if (file === 'db/summary.json') { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ total })); return }
    res.setHeader('Content-Type', 'text/html')
    res.end(file?.endsWith('legacy.html') ? '<!doctype html><html><head><title>Existing</title></head><body><button id="legacy" onclick="this.textContent=\'Still working\'">Existing button</button></body></html>' : html); return
  }
  if (url.pathname.endsWith('/query')) { queries++; res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ success: true, data: { rows: [{ label: 'Day 1', value: 10 }, { label: 'Day 2', value: 20 }] } })); return }
  if (url.pathname.endsWith('/run')) { scripts++; res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ success: true, data: { marker: 'SCRIPT_OK' } })); return }
  if (url.pathname === '/report-preview/') { res.setHeader('Content-Type', 'text/html'); res.end('<!doctype html><html><head><title>Preview</title></head><body><script src="/report-preview/report-preview.js"></script></body></html>'); return }
  res.statusCode = 404; res.end('Missing fixture')
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const origin = `http://127.0.0.1:${server.address().port}`
let browser
try {
  browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || '/usr/bin/google-chrome', headless: true, args: ['--no-sandbox'] })
  const page = await browser.newPage({ viewport: { width: 1300, height: 900 } })
  const errors = []; page.on('pageerror', e => errors.push(e.message))
  await page.goto(origin + '/report-preview/?workspace=Crew/example&token=fixture&document=db/reports/index.html&theme=light')
  await page.waitForFunction(() => document.documentElement.dataset.previewState === 'ready')
  const frame = page.frames().find(f => f.parentFrame())
  assert(frame)
  await frame.getByText('SCRIPT_OK', { exact: true }).waitFor()
  assert.equal(await frame.locator('.dashboard-metric').textContent(), '42')
  assert.equal(queries, 1); assert.equal(scripts, 1)
  await frame.getByRole('textbox', { name: 'Filter' }).fill('open')
  await frame.getByRole('button', { name: 'Show details' }).click()
  await frame.getByText('Recorded values only', { exact: true }).waitFor()
  assert.equal(await frame.getByRole('button', { name: 'Ask about this', exact: true }).isDisabled(), true)
  assert.equal(await frame.locator('button[data-report-action="lookup"]').count(), 1)
  await frame.getByRole('slider', { name: 'Playback position' }).focus()
  await frame.getByRole('slider', { name: 'Playback position' }).press('End')
  assert.equal(await frame.locator('svg circle').count(), 2)
  assert((await frame.locator('svg').boundingBox()).width > 200)
  await page.evaluate(() => window.__reportPreview.setTheme('dark'))
  assert.equal(await frame.locator('html').getAttribute('data-theme'), 'dark')
  await page.evaluate(() => window.__reportPreview.setWidth(390))
  assert.equal(await frame.evaluate(() => document.documentElement.scrollWidth <= 392), true)
  await page.screenshot({ path: path.join(root, 'json-mobile-dark.png'), fullPage: true })
  await page.reload(); await page.waitForFunction(() => document.documentElement.dataset.previewState === 'ready')
  const reloaded = page.frames().find(f => f.parentFrame())
  await reloaded.getByText('SCRIPT_OK', { exact: true }).waitFor()
  total = 84
  await reloaded.getByRole('button', { name: 'Refresh data' }).click()
  await reloaded.locator('.dashboard-metric').filter({ hasText: '84' }).waitFor()
  const snapshot = await page.evaluate(() => window.__reportPreview.getState())
  assert.equal(snapshot.report.state, 'ready')
  await page.goto(origin + '/report-preview/?workspace=Crew/example&token=fixture&document=db/reports/legacy.html')
  await page.waitForFunction(() => document.documentElement.dataset.previewState === 'ready')
  const legacy = page.frames().find(f => f.parentFrame())
  await legacy.getByRole('button', { name: 'Existing button' }).click()
  await legacy.getByRole('button', { name: 'Still working' }).waitFor()
  assert.deepEqual(errors, [])
  const result = { json_render: true, fixture_data_query_get_run: true, local_form_action: true, legacy_html_script: true, dark_mobile_reload: true, queries, scripts, errors }
  await writeFile(path.join(root, 'browser-result.json'), JSON.stringify(result, null, 2) + '\n')
  console.log(JSON.stringify(result))
} finally { await browser?.close(); await new Promise(resolve => server.close(resolve)) }
