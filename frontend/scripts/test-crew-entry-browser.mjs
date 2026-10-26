// Synthetic admitted inventory and real entry component; no account or server writes.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'crew-entry-browser')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser
let page
try {
  await mkdir(output, { recursive: true })
  await server.listen()
  browser = await chromium.launch({ headless: true })
  page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
  page.setDefaultTimeout(30000)
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.route(/^https?:\/\/[^/]+\/api\//, route => route.fulfill({ json: { items: [], sessions: [], connections: [], total: 0,
    resolved_features: ['files', 'memory', 'skills', 'dashboard', 'live-chat', 'coding', 'browser', 'secrets', 'mcp', 'knowledgebase', 'attached-folders', 'workflow-references', 'terminal', 'voice', 'models', 'schedules', 'condition-triggers', 'triggers', 'bots', 'database', 'costs', 'background-work', 'views'].map(id => ({ id })) } }))
  const url = server.resolvedUrls.local[0] + 'scripts/test-fixtures/crew-entry-qa.html'
  await page.goto(url)
  const draft = page.getByRole('textbox', { name: 'What would you like your Crew to do?' })
  const research = page.locator('li button').filter({ hasText: 'Compare useful sources' })
  await research.waitFor()
  const row = await research.boundingBox()
  assert.ok(row.y + row.height <= 800, 'existing work is visible without scrolling on desktop')
  const useCases = page.getByLabel('Crew use cases')
  assert.equal(await useCases.getByRole('button').count(), 7, 'original use cases remain reachable')
  await useCases.getByRole('button', { name: /Cowork/ }).click()
  assert.match(await draft.inputValue(), /research, prepare documents/)
  assert.equal(await page.getByLabel('Navigation result').textContent(), '', 'suggestion only fills the draft')
  await page.getByRole('button', { name: 'Create Crew', exact: true }).click()
  assert.match(await page.getByLabel('Navigation result').textContent(), /^Setup request \(cowork\): .*research, prepare documents/)
  console.log('Entry request forwarded; checking selected setup type.')
  const setup = page.getByRole('dialog')
  await setup.getByRole('heading', { name: 'Cowork', exact: true }).waitFor()
  console.log('Selected type is visible; checking compact capabilities.')
  const included = setup.locator('details').filter({ has: page.getByText('What’s included', { exact: true }) })
  assert.equal(await included.getAttribute('open'), null, 'feature list starts collapsed')
  assert.equal(await setup.getByRole('button', { name: 'Files: how to use', exact: true }).count(), 0)
  await page.screenshot({ path: path.join(output, 'crew-setup-compact.png') })
  console.log('Compact setup saved; expanding included capabilities.')
  await included.locator('summary').click()
  await setup.getByRole('button', { name: 'Files: how to use', exact: true }).waitFor()
  assert.equal(await setup.getByRole('button', { name: /: how to use$/ }).count(), 23, 'all enabled capabilities remain in optional details')
  await setup.getByRole('button', { name: 'Files: how to use', exact: true }).click()
  await setup.getByText('Summarize the document I attached.', { exact: false }).waitFor()
  await page.screenshot({ path: path.join(output, 'crew-setup-features-open.png') })
  await setup.getByRole('button', { name: 'Close', exact: true }).click()
  await page.screenshot({ path: path.join(output, 'crew-entry-desktop.png') })
  await page.getByRole('textbox', { name: 'Search Crews' }).fill('weekly')
  assert.equal(await page.locator('li').count(), 1)
  await page.locator('li button').click()
  assert.equal(await page.getByLabel('Navigation result').textContent(), 'Open Crew: reports')
  for (const width of [390, 360]) {
    await page.setViewportSize({ width, height: 844 })
    await page.goto(url)
    await draft.waitFor()
    await page.getByLabel('Crew use cases').getByRole('button', { name: /Chief of Staff/ }).scrollIntoViewIfNeeded()
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'phone has no horizontal overflow')
    await page.locator('li button').filter({ hasText: 'Compare useful sources' }).scrollIntoViewIfNeeded()
    await page.screenshot({ path: path.join(output, `crew-entry-mobile-${width}.png`) })
  }
  await page.setViewportSize({ width: 1280, height: 800 })
  await page.goto(url + '?empty')
  await page.getByText('Your Crews will appear here.', { exact: false }).waitFor()
  await page.screenshot({ path: path.join(output, 'crew-entry-empty.png') })
  await page.goto(url + '?run')
  await page.getByRole('heading', { name: /Your Crews/ }).waitFor()
  assert.equal(await page.getByRole('button', { name: 'Create Crew', exact: true }).count(), 0)
  assert.equal(await page.locator('textarea').count(), 0)
  assert.equal(await page.locator('li').count(), 2, 'Run account keeps its admitted work')
  await page.screenshot({ path: path.join(output, 'crew-entry-run.png') })
  assert.deepEqual(errors, [])
  console.log('Crew entry browser passed: existing work, creation draft, exact shared row, empty/Run states and desktop/phone layout.')
} catch (cause) {
  await page?.screenshot({ path: path.join(output, 'crew-entry-failure.png') }).catch(() => {})
  throw cause
} finally {
  await browser?.close()
  await server.close()
}
