// Real TSX output of the one-off saved-page migration and automatic generator.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { readFile } from 'node:fs/promises'
import path from 'node:path'
import { chromium } from 'playwright'
const root = process.env.DASHBOARD_MIGRATION_QA_DIR
assert(root)
const migrated = await readFile(path.join(root, 'migrated.html'), 'utf8')
const automatic = await readFile(path.join(root, 'default.html'), 'utf8')
let total = 42, queries = 0, scripts = 0
const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost')
  if (url.pathname === '/report-preview/') { res.setHeader('Content-Type', 'text/html'); res.end('<html><body><script src="/report-preview/report-preview.js"></script></body></html>'); return }
  if (url.pathname === '/report-preview/report-preview.js' || /^\/dashboard-react\.(js|css)$/.test(url.pathname)) { res.setHeader('Content-Type', url.pathname.endsWith('.css') ? 'text/css' : 'text/javascript'); res.end(await readFile(path.join('dist', path.basename(url.pathname)))); return }
  if (url.pathname.endsWith('/file')) {
    const file = url.searchParams.get('path')
    if (file === 'db/summary.json') { res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ total })); return }
    res.setHeader('Content-Type', 'text/html'); res.end(file?.endsWith('default.html') ? automatic : file?.endsWith('legacy.html') ? '<html><head><title>Existing</title></head><body><button onclick="this.textContent=\'Still working\'">Existing button</button></body></html>' : migrated); return
  }
  if (url.pathname.endsWith('/query')) { queries++; res.setHeader('Content-Type', 'application/json'); res.end(JSON.stringify({ success: true, data: { rows: [{ label: 'Day 1', value: 10 }, { label: 'Day 2', value: 20 }] } })); return }
  if (url.pathname.endsWith('/run')) { scripts++; res.setHeader('Content-Type', 'application/json'); res.end('{"success":true,"data":{"marker":"SCRIPT_OK"}}'); return }
  res.statusCode = 404; res.end('Missing fixture')
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const origin = `http://127.0.0.1:${server.address().port}`
const browser = await chromium.launch({ headless: true })
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
  const errors = []; page.on('pageerror', error => errors.push(error.message))
  const open = async (file = 'migrated.html', theme = 'dark', width = 1280) => {
    await page.setViewportSize({ width, height: 900 })
    await page.goto(origin + `/report-preview/?workspace=Crew/example&token=fixture&document=db/reports/${file}&theme=${theme}&width=${width}`)
    await page.waitForFunction(() => document.documentElement.dataset.previewState === 'ready')
    return page.frames().find(frame => frame.parentFrame())
  }
  let frame = await open()
  await frame.getByText('SCRIPT_OK', { exact: true }).waitFor()
  assert.equal(await frame.locator('.dashboard-metric').textContent(), '42')
  assert.deepEqual([queries, scripts], [1, 1])
  await frame.getByRole('textbox', { name: 'Filter' }).fill('open')
  await frame.getByRole('button', { name: 'Show details' }).click()
  await frame.getByText('Recorded values only', { exact: true }).waitFor()
  await page.evaluate(() => window.__reportPreview.seek(2))
  await frame.locator('.recharts-line-curve').waitFor()
  assert.equal(await frame.getByRole('button', { name: 'Ask about this', exact: true }).count(), 0)
  assert.equal(await frame.locator('[data-report-action="lookup"]').count(), 1)
  total = 84
  await frame.getByRole('button', { name: 'Refresh data', exact: true }).click()
  await frame.locator('.dashboard-metric').filter({ hasText: '84' }).waitFor()
  assert.equal((await page.evaluate(() => window.__reportPreview.getState())).report.state, 'ready')
  for (const theme of ['light', 'dark']) {
    frame = await open('migrated.html', theme, 390)
    await frame.getByText('SCRIPT_OK', { exact: true }).waitFor()
    assert.equal(await frame.evaluate(() => document.documentElement.scrollWidth <= 392), true)
    await page.screenshot({ path: path.join(root, `migrated-${theme}-mobile.png`), fullPage: true })
    frame = await open('default.html', theme, 390)
    await frame.getByRole('heading', { name: 'Support <b>desk</b>' }).waitFor()
    assert.equal(await frame.getByRole('button', { name: 'Ask about this dashboard' }).count(), 0)
    assert.equal(await frame.locator('[data-report-action="find_order"]').count(), 1)
    assert.equal(await frame.evaluate(() => document.documentElement.scrollWidth <= 392), true)
    await page.screenshot({ path: path.join(root, `default-${theme}-mobile.png`), fullPage: true })
  }
  frame = await open('legacy.html')
  await frame.getByRole('button', { name: 'Existing button' }).click()
  await frame.getByRole('button', { name: 'Still working' }).waitFor()
  assert.deepEqual(errors, [])
  console.log('PASS: migrated saved page, data reads, form/action, Recharts/motion, refresh, default TSX, dark/light/mobile and HTML compatibility')
} finally { await browser.close(); await new Promise(resolve => server.close(resolve)) }
