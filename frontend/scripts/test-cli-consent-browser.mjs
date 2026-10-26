import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'cli-consent-browser')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser, page
try {
  await mkdir(output, { recursive: true }); await server.listen()
  browser = await chromium.launch({ headless: true })
  page = await browser.newPage(); page.setDefaultTimeout(15000)
  const errors = [], decisions = []
  page.on('pageerror', error => errors.push(error.message))
  let scopes = ['messages:read', 'messages:write']
  await page.route(/^https?:\/\/[^/]+\/api\/oauth\/cli\/consent/, async route => {
    if (route.request().method() === 'POST') decisions.push(route.request().postDataJSON())
    await route.fulfill({ json: { client_name: 'AgentWorks CLI', scopes, user_code: '1234ABCD' } })
  })
  const url = server.resolvedUrls.local[0] + `scripts/test-fixtures/cli-consent-qa.html?code=cli_verify_${'a'.repeat(64)}`
  for (const [name, width, height] of [['desktop', 1280, 800], ['phone', 390, 844]]) {
    await page.setViewportSize({ width, height }); await page.goto(url)
    const approve = page.getByRole('button', { name: 'Approve connection', exact: true })
    await approve.waitFor()
    assert.equal(await page.getByLabel('Verification code', { exact: true }).textContent(), '1234ABCD')
    assert.equal(await page.locator('details').getAttribute('open'), null)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    const bounds = await approve.boundingBox()
    assert.ok(bounds.y >= 0 && bounds.y + bounds.height <= height, `${name}: approval remains visible`)
    await page.screenshot({ path: path.join(output, `cli-consent-${name}.png`) })
    await approve.click(); await page.getByRole('heading', { name: 'You’re connected', exact: true }).waitFor()
    assert.deepEqual(decisions.at(-1), { decision: 'approve' })
  }
  scopes = ['devices:connect', 'files:read']
  await page.goto(url)
  await page.getByRole('heading', { name: 'Connect this computer', exact: true }).waitFor()
  await page.getByText('No folder is shared automatically.', { exact: true }).waitFor()
  await page.getByRole('region', { name: 'Additional permissions' }).getByText('files:read', { exact: true }).waitFor()
  await page.getByText('Connection details', { exact: true }).click()
  await page.getByText(/Choose folders and Read or Read & write/).waitFor()
  await page.screenshot({ path: path.join(output, 'cli-consent-computer-details.png') })
  await page.getByRole('button', { name: 'Deny', exact: true }).click()
  await page.getByRole('heading', { name: 'Connection declined', exact: true }).waitFor()
  assert.deepEqual(decisions.at(-1), { decision: 'deny' })
  assert.deepEqual(errors, [])
  console.log('CLI consent browser passed: code, readable message/computer permissions, explicit extra scopes, details, approve/deny and phone bounds.')
} catch (error) {
  await page?.screenshot({ path: path.join(output, 'cli-consent-failure.png') }).catch(() => {})
  throw error
} finally { await browser?.close(); await server.close() }
