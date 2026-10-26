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
try {
  await mkdir(output, { recursive: true })
  await server.listen()
  browser = await chromium.launch({ headless: true })
  const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.route(/^https?:\/\/[^/]+\/api\//, route => route.fulfill({ json: { items: [], sessions: [], total: 0 } }))
  const url = server.resolvedUrls.local[0] + 'scripts/test-fixtures/crew-entry-qa.html'
  await page.goto(url)
  const draft = page.getByRole('textbox', { name: 'What would you like your Crew to do?' })
  const research = page.locator('li button').filter({ hasText: 'Compare useful sources' })
  await research.waitFor()
  const row = await research.boundingBox()
  assert.ok(row.y + row.height <= 800, 'existing work is visible without scrolling on desktop')
  await page.getByRole('button', { name: /Analyse a spreadsheet/ }).click()
  assert.match(await draft.inputValue(), /check the numbers/)
  assert.equal(await page.getByLabel('Navigation result').textContent(), '', 'suggestion only fills the draft')
  await page.getByRole('button', { name: 'Create Crew', exact: true }).click()
  assert.match(await page.getByLabel('Navigation result').textContent(), /^Setup request: .*check the numbers/)
  await page.screenshot({ path: path.join(output, 'crew-entry-desktop.png') })
  await page.getByRole('textbox', { name: 'Search Crews' }).fill('weekly')
  assert.equal(await page.locator('li').count(), 1)
  await page.locator('li button').click()
  assert.equal(await page.getByLabel('Navigation result').textContent(), 'Open Crew: reports')
  for (const width of [390, 360]) {
    await page.setViewportSize({ width, height: 844 })
    await page.goto(url)
    await draft.waitFor()
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
} finally {
  await browser?.close()
  await server.close()
}
