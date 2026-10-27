import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'account-availability-browser')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser, page
try {
  await mkdir(output, { recursive: true }); await server.listen()
  browser = await chromium.launch({ headless: true })
  page = await browser.newPage(); page.setDefaultTimeout(15000)
  const errors = []; page.on('pageerror', error => errors.push(error.message))
  let saved = [], audience = { users: ['person-z'] }
  await page.route('**/api/**', async route => {
    const request = route.request(), url = new URL(request.url())
    let payload = {}
    if (request.method() === 'PATCH') {
      audience = request.postDataJSON().available_to; saved.push(audience)
      await route.fulfill({ status: 204 }); return
    }
    if (url.pathname.endsWith('/share-targets')) payload = { workflows: [], crews: [], users: [
      { id: 'person-a', name: 'Alex', email: 'alex@example.com' },
      { id: 'person-z', name: 'Zoe', email: 'zoe@example.com' },
      { id: 'person-email', name: '', email: 'colleague@example.com' },
    ] }
    else if (url.pathname.endsWith('/provider-connections')) payload = { connections: [{ id: 'global:codex-cli', provider: 'codex-cli', display_name: 'Company account', scope: 'global', auth_method: 'server', kind: 'admin', relation: 'server', can_manage: true, usable: true, availability_editable: true, availability: { available_to: audience, text: 'Chosen people', source: 'admin', pinned: false } }] }
    else if (url.pathname.endsWith('/costs')) payload = { providers: [] }
    else if (url.pathname.endsWith('/status')) payload = { state: 'signed_in', verified: false, checked_at: '' }
    await route.fulfill({ json: payload })
  })
  const url = server.resolvedUrls.local[0] + 'scripts/test-fixtures/account-availability-qa.html'
  for (const [name, width, height] of [['desktop', 1280, 800], ['phone', 390, 844]]) {
    await page.setViewportSize({ width, height }); await page.goto(url)
    await page.getByRole('button', { name: 'More for the admin-managed account', exact: true }).click()
    await page.getByRole('menuitem', { name: 'Who can use it', exact: true }).click()
    const form = page.locator('form').filter({ hasText: 'Who can use this account?' })
    await form.getByPlaceholder('Search people').waitFor()
    assert.equal(await form.getByRole('radio').count(), 3)
    assert.equal(await form.getByRole('button', { name: 'Save', exact: true }).count(), 1)
    assert.deepEqual(await form.locator('label').filter({ has: page.locator('input[type=checkbox]') }).allTextContents(), ['Zoe', 'Alex', 'colleague@example.com'])
    await form.screenshot({ path: path.join(output, `account-audience-${name}.png`) })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth > innerWidth), false, 'no horizontal overflow')
    await form.getByRole('radio', { name: 'Only admins', exact: true }).check()
    assert.equal(await form.getByPlaceholder('Search people').count(), 0)
    assert.equal(saved.length, name === 'desktop' ? 0 : 1, 'no autosave')
    await form.getByRole('button', { name: 'Save', exact: true }).click()
    await form.waitFor({ state: 'hidden' })
    assert.equal(saved.at(-1), 'admins')
    audience = { users: ['person-z'] }
  }
  assert.deepEqual(errors, [])
  console.log('Account audience: one question, three choices, one Save, selected-first people, no autosave and no overflow at desktop/phone widths.')
} catch (error) {
  await page?.screenshot({ path: path.join(output, 'account-audience-failure.png') }).catch(() => {})
  throw error
} finally { await browser?.close(); await server.close() }
