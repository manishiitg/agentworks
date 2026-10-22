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
const originalSpec = JSON.parse(html.match(/<script id="__dashboard_spec" type="application\/json">([\s\S]*?)<\/script>/)[1])
const defaultSpec = structuredClone(originalSpec)
delete defaultSpec.styles
defaultSpec.elements.page.props.align = 'stretch'
defaultSpec.elements.page.children = defaultSpec.elements.page.children.map(id => id === 'metric' ? 'stats' : id)
defaultSpec.elements.stats = { type: 'Grid', props: { columns: 3, gap: 'lg', className: 'default-stats' }, children: ['metric', 'reviewed', 'pending'] }
defaultSpec.elements.reviewed = { type: 'Metric', props: { label: 'Reviewed', value: 32, detail: 'Sample data' }, children: [] }
defaultSpec.elements.pending = { type: 'Metric', props: { label: 'Pending', value: 10, detail: 'Sample data' }, children: [] }
function specPage(spec) {
  return html.replace(/(<script id="__dashboard_spec" type="application\/json">)[\s\S]*?(<\/script>)/, (_, start, end) => start + JSON.stringify(spec).replace(/</g, '\\u003c') + end)
}
const customSpec = structuredClone(defaultSpec)
customSpec.styles = '#dashboard-root{--dashboard-page-padding:0px;--dashboard-page-width:none}.dashboard-stat{padding:3px;border-radius:0px;background:rgb(230,240,250)}.default-stats{grid-template-columns:repeat(2,minmax(0,1fr))}'
const badHTML = '<!doctype html><html><head><title>Diagnostic fixture</title><link rel="stylesheet" href="/missing.css"></head><body><div style="width:1800px;height:20px">Wide fixture</div><img src="/missing.png" alt="Missing fixture" width="40" height="40"><button style="width:40px;height:40px"></button><span style="font-size:10px">Small fixture</span></body></html>'

let queries = 0, scripts = 0, total = 42
const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost')
  const file = url.searchParams.get('path')
  if (url.pathname === '/report-preview/report-preview.js') { res.setHeader('Content-Type', 'text/javascript'); res.end(await readFile(path.join(root, 'report-preview.js'))); return }
  if (/^\/dashboard-json\.(js|css)$/.test(url.pathname)) { res.setHeader('Content-Type', url.pathname.endsWith('.css') ? 'text/css' : 'text/javascript'); res.end(await readFile(path.join(root, 'frontend/dist', path.basename(url.pathname)))); return }
  if (url.pathname.endsWith('/file')) {
    if (file === 'db/summary.json') { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ total })); return }
    res.setHeader('Content-Type', 'text/html')
    res.end(file?.endsWith('default.html') ? specPage(defaultSpec) : file?.endsWith('custom.html') ? specPage(customSpec) : file?.endsWith('bad.html') ? badHTML : file?.endsWith('legacy.html') ? '<!doctype html><html><head><title>Existing</title></head><body><button id="legacy" onclick="this.textContent=\'Still working\'">Existing button</button></body></html>' : html); return
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
  await page.setViewportSize({ width: 390, height: 900 })
  await page.evaluate(() => window.__reportPreview.setWidth(390))
  await page.waitForTimeout(100)
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
  const checks = []
  for (const theme of ['light', 'dark']) {
    for (const width of [1280, 390]) {
      await page.setViewportSize({ width, height: 900 })
      await page.goto(origin + `/report-preview/?workspace=Crew/example&token=fixture&document=db/reports/default.html&width=${width}&theme=${theme}`)
      await page.waitForFunction(() => document.documentElement.dataset.previewState === 'ready')
      const live = page.frames().find(f => f.parentFrame())
      await live.getByText('SCRIPT_OK', { exact: true }).waitFor()
      await page.evaluate(() => window.__reportPreview.seek(2))
      const ui = await page.evaluate(() => window.__reportPreview.getUIChecks())
      assert.equal(ui.viewport_width, width); assert.equal(ui.frame_width, width)
      assert.equal(ui.horizontal_overflow_px, 0)
      assert.deepEqual(ui.missing_stylesheets, []); assert.equal(ui.broken_images, 0)
      assert.deepEqual(ui.unnamed_controls, [])
      const css = await live.evaluate(() => ({
        padding: parseFloat(getComputedStyle(document.getElementById('dashboard-root')).paddingLeft),
        columns: getComputedStyle(document.querySelector('.default-stats')).gridTemplateColumns.split(' ').length,
        statPadding: parseFloat(getComputedStyle(document.querySelector('.dashboard-stat')).paddingLeft),
      }))
      assert(css.padding >= 16); assert(css.statPadding >= 16)
      assert.equal(css.columns, width < 640 ? 1 : 3)
      await page.screenshot({ path: path.join(root, `default-${theme}-${width}.png`), fullPage: true })
      checks.push({ theme, width, ...ui, defaults: css })
    }
  }
  await page.goto(origin + '/report-preview/?workspace=Crew/example&token=fixture&document=db/reports/custom.html&width=390&theme=light')
  await page.waitForFunction(() => document.documentElement.dataset.previewState === 'ready')
  const custom = page.frames().find(f => f.parentFrame())
  await custom.getByText('SCRIPT_OK', { exact: true }).waitFor()
  assert.deepEqual(await custom.evaluate(() => ({
    pagePadding: getComputedStyle(document.getElementById('dashboard-root')).paddingLeft,
    cardPadding: getComputedStyle(document.querySelector('.dashboard-stat')).paddingLeft,
    radius: getComputedStyle(document.querySelector('.dashboard-stat')).borderRadius,
    columns: getComputedStyle(document.querySelector('.default-stats')).gridTemplateColumns.split(' ').length,
  })), { pagePadding: '0px', cardPadding: '3px', radius: '0px', columns: 2 })
  await page.screenshot({ path: path.join(root, 'custom-mobile.png'), fullPage: true })
  await page.goto(origin + '/report-preview/?workspace=Crew/example&token=fixture&document=db/reports/bad.html&width=390')
  await page.waitForFunction(() => document.documentElement.dataset.previewState === 'ready')
  const bad = page.frames().find(f => f.parentFrame())
  await bad.locator('img').evaluate(image => image.decode().catch(() => {}))
  const diagnostics = await page.evaluate(() => window.__reportPreview.getUIChecks())
  assert(diagnostics.horizontal_overflow_px > 1000)
  assert.deepEqual(diagnostics.missing_stylesheets, ['/missing.css'])
  assert.equal(diagnostics.broken_images, 1); assert.equal(diagnostics.unnamed_controls.length, 1)
  assert(diagnostics.small_text_count > 0)
  await page.goto(origin + '/report-preview/?workspace=Crew/example&token=fixture&document=db/reports/legacy.html')
  await page.waitForFunction(() => document.documentElement.dataset.previewState === 'ready')
  const legacy = page.frames().find(f => f.parentFrame())
  await legacy.getByRole('button', { name: 'Existing button' }).click()
  await legacy.getByRole('button', { name: 'Still working' }).waitFor()
  assert.deepEqual(errors, [])
  const result = { checks, diagnostics, authored_css_overrides: true, json_render: true, fixture_data_query_get_run: true, local_form_action: true, legacy_html_script: true, dark_mobile_reload: true, queries, scripts, errors }
  await writeFile(path.join(root, 'browser-result.json'), JSON.stringify(result, null, 2) + '\n')
  console.log(JSON.stringify(result))
} finally { await browser?.close(); await new Promise(resolve => server.close(resolve)) }
