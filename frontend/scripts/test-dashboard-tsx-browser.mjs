// Remote Chromium proof: the real store compiles the source, local launch
// preparation builds/copies runtimes, and the official report host loads it.
// Data transport is an isolated fixture, not a deployed-account acceptance.
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { createServer } from 'node:http'
import { mkdtemp, readFile, mkdir, copyFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'

const frontend = fileURLToPath(new URL('..', import.meta.url))
const repo = path.dirname(frontend)
const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || await mkdtemp(path.join(os.tmpdir(), 'dashboard-tsx-'))
await mkdir(output, { recursive: true })
const fixture = path.join(output, 'tsx.html')
execFileSync('go', ['test', './dashboards', '-run', '^TestTSXDashboardLifecycle$', '-count=1'], { cwd: path.join(repo, 'workspace'), env: { ...process.env, DASHBOARD_TSX_TEST_FIXTURE: fixture }, stdio: 'inherit' })
execFileSync('npm', ['run', 'prepare:local-assets'], { cwd: frontend, stdio: 'inherit' })
for (const root of ['agent_go/static', 'agent_go/cmd/server/static', 'frontend/dist']) {
  for (const name of ['dashboard-react.js', 'dashboard.css', 'report-preview.js']) assert((await readFile(path.join(repo, root, name))).length > 0, `missing native asset: ${root}/${name}`)
}
await mkdir(path.join(output, 'runtime-assets'), { recursive: true })
for (const name of ['dashboard-react.js', 'dashboard.css']) await copyFile(path.join(frontend, 'dist', name), path.join(output, 'runtime-assets', name))
const html = await readFile(fixture, 'utf8')
let total = 42, reads = 0, queries = 0, runs = 0, rejectData = false
const server = createServer(async (req, res) => {
  const url = new URL(req.url, 'http://localhost')
  if (url.pathname === '/report-preview/') { res.setHeader('Content-Type', 'text/html'); res.end('<html><body><script src="/report-preview/report-preview.js"></script></body></html>'); return }
  if (url.pathname === '/report-preview/report-preview.js' || /^\/(dashboard-react\.js|dashboard\.css)$/.test(url.pathname)) {
    res.setHeader('Content-Type', url.pathname.endsWith('.css') ? 'text/css' : 'text/javascript')
    res.end(await readFile(path.join(frontend, 'dist', path.basename(url.pathname)))); return
  }
  if (url.pathname.endsWith('/file')) {
    const file = url.searchParams.get('path')
    if (file === 'db/summary.json') {
      reads++; res.setHeader('Content-Type', 'application/json')
      if (rejectData) { res.statusCode = 403; res.end('{"error":"Project data unavailable"}'); return }
      res.end(JSON.stringify({ total })); return
    }
    res.setHeader('Content-Type', 'text/html'); res.end(html); return
  }
  if (url.pathname.endsWith('/query')) { queries++; res.setHeader('Content-Type', 'application/json'); res.end('{"success":true,"data":{"rows":[{"value":42}]}}'); return }
  if (url.pathname.endsWith('/run')) { runs++; res.setHeader('Content-Type', 'application/json'); res.end('{"success":true,"data":{"marker":"SCRIPT_OK"}}'); return }
  res.statusCode = 404; res.end('Missing fixture')
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const origin = `http://127.0.0.1:${server.address().port}`
const browser = await chromium.launch({ headless: true })
try {
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
  const errors = []; page.on('pageerror', error => errors.push(error.message))
  const previewURL = origin + '/report-preview/?workspace=Crew/example&token=fixture&document=db/reports/index.html&theme=dark'
  await page.goto(previewURL)
  await page.waitForFunction(() => document.documentElement.dataset.previewState === 'ready')
  const frame = page.frames().find(value => value.parentFrame())
  await frame.getByRole('heading', { name: 'Recorded overview' }).waitFor()
  assert.equal(await frame.locator('#total').textContent(), '42')
  assert.equal(await frame.getByRole('link', { name: 'Jump to total' }).getAttribute('href'), '#total')
  assert.equal(await frame.locator('#total').evaluate(el => getComputedStyle(el).color), 'rgb(10, 120, 180)')
  await frame.getByText('SCRIPT_OK', { exact: true }).waitFor()
  assert.deepEqual([reads, queries, runs], [1, 1, 1])
  await frame.getByRole('button', { name: 'Details', exact: true }).click()
  await frame.getByText('Recorded details', { exact: true }).waitFor()
  assert.equal(await frame.getByRole('button', { name: 'Ask', exact: true }).count(), 0)
  assert.equal(await frame.locator('.dashboard-stat').count(), 1)
  await frame.locator('.recharts-line-curve').waitFor()
  assert.equal(await frame.locator('table').count(), 2)
  assert.equal(await frame.locator('[data-report-action="lookup"]').count(), 1)
  total = 84
  await frame.evaluate(() => Promise.all(window.__reportQueuedCallbacks.map(callback => window.__runReportCallback(callback))))
  await page.waitForFunction(() => document.querySelector('iframe')?.contentDocument?.querySelector('#total')?.textContent === '84')
  assert.deepEqual([reads, queries, runs], [2, 2, 2])
  await page.setViewportSize({ width: 390, height: 900 })
  await page.evaluate(() => window.__reportPreview.setWidth(390))
  assert.equal(await frame.evaluate(() => document.documentElement.scrollWidth <= 392), true)
  await page.screenshot({ path: path.join(output, 'tsx-mobile.png'), fullPage: true })
  // Rebind the real runtime to shared/owner presentation; no actual chat call.
  await frame.evaluate(async () => {
    window.__askOpened = 0
    window.report.chat = { available: true, open: async () => { window.__askOpened++ }, close() {}, mount: async () => {} }
    await Promise.all(window.__reportQueuedCallbacks.map(callback => window.__runReportCallback(callback)))
  })
  await frame.getByRole('button', { name: 'Ask', exact: true }).click()
  assert.equal(await frame.evaluate(() => window.__askOpened), 1, 'shared Ask opened more than once')
  await frame.evaluate(async () => {
    window.report.chat.available = false
    await Promise.all(window.__reportQueuedCallbacks.map(callback => window.__runReportCallback(callback)))
  })
  assert.equal(await frame.getByRole('button', { name: 'Ask', exact: true }).count(), 0)
  assert.deepEqual(errors, [])
  // The exact same source waits for data in export and surfaces real failures.
  await page.goto(previewURL + '&export=1')
  await page.waitForFunction(() => document.documentElement.dataset.previewState === 'ready')
  const frozen = await page.evaluate(() => window.__reportPreview.freeze())
  assert(JSON.stringify(frozen).includes('84'), 'export snapshot lost report data')
  rejectData = true
  await page.goto(previewURL)
  await page.waitForFunction(() => document.documentElement.dataset.previewState === 'error')
  console.log('TSX managed compile, data, React interaction, CSS, refresh, mobile, preview refusal and frozen export PASS')
} finally { await browser.close(); await new Promise(resolve => server.close(resolve)) }

// Prove the automatic default TSX dashboard and HTML pages in the browser.
execFileSync('go', ['test', './cmd/server', '-run', '^TestDefaultDashboardHTMLShowsOnlyDeclaredThings$', '-count=1'], { cwd: path.join(repo, 'agent_go'), env: { ...process.env, GOWORK: 'off', DASHBOARD_DEFAULT_TEST_FIXTURE: path.join(output, 'default.html') }, stdio: 'inherit' })
execFileSync('node', ['scripts/test-dashboard-migration-browser.mjs'], { cwd: frontend, env: { ...process.env, DASHBOARD_MIGRATION_QA_DIR: output }, stdio: 'inherit' })
