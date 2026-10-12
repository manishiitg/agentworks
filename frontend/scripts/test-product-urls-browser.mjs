// Real frontend/history/sign-in in Chromium with caller-scoped API responses.
// This verifies browser behavior, not a deployed server's authorization policy.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'product-urls-browser')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser
try {
  await mkdir(output, { recursive: true }); await server.listen()
  browser = await chromium.launch({ headless: true })
  const base = server.resolvedUrls.local[0].replace(/\/$/, '')
  async function setup(signedIn, allowed = ['agentworks', 'relays', 'bullpen', 'code']) {
    const page = await browser.newPage({ viewport: { width: 1200, height: 900 }, permissions: ['clipboard-read', 'clipboard-write'] })
    const user = { id: signedIn ? 'qa-reader' : 'qa-new-reader', username: 'reader', role: 'reader', allowed_products: allowed, can_run_workflows: true, can_write_workflows: false, is_admin: false }
    const errors = [], calls = []
    page.on('pageerror', error => { errors.push(error.message); console.error('Browser error:', error.stack) })
    await page.addInitScript(({ signedIn }) => {
      if (signedIn && !localStorage.getItem('workspace-connection-store')) localStorage.setItem('auth_token', 'qa-token')
      for (const key of ['agentworks_welcome_home_v1_dismissed', 'llm_discovery_onboarding_dismissed', 'agentworks_overview_walkthrough_v3_dismissed', 'agentworks_empty_automation_walkthrough_v3_dismissed']) localStorage.setItem(key, 'true')
      // Explicit route must override a conflicting remembered product and tab.
      if (!localStorage.getItem('agentworks-product-surface')) localStorage.setItem('agentworks-product-surface', JSON.stringify({ state: { productSurface: 'agentworks' }, version: 6 }))
      localStorage.setItem('bullpen:view', 'agents')
    }, { signedIn })
    await page.route('**/runtime-config.js*', route => route.fulfill({ contentType: 'text/javascript', body: `window.__APP_RUNTIME_CONFIG__=${JSON.stringify({ apiBaseUrl: base, workspaceApiBaseUrl: base + '/api/wp', deploymentMode: 'server', enabledProductSurfaces: ['agentworks', 'relays', 'bullpen', 'code'], defaultProductSurface: 'agentworks' })}` }))
    await page.route('**/api/**', route => {
      const url = new URL(route.request().url())
      if (!url.pathname.startsWith('/api/')) return route.continue()
      calls.push(url.pathname)
      let data = { files: [], workflows: [], agents: [], groups: [], notices: [], tokens: [], connections: [], profiles: [], providers: [], presets: [], projects: [], sessions: [], models: [], llms: [], steps: [], tools: [], servers: [], skills: [] }
      if (url.pathname === '/api/health') data = { status: 'healthy' }
      if (url.pathname === '/api/auth/mode') data = { multi_user_mode: true, providers: [{ name: 'simple', display_name: 'Password', type: 'credentials', configured: true }] }
      if (url.pathname === '/api/auth/me') data = user
      if (url.pathname === '/api/auth/login') data = { token: 'qa-token', user }
      return route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
    })
    return { page, calls, errors }
  }
  const { page, errors } = await setup(true)
  await page.goto(base + '/bullpen/connect')
  await page.getByRole('heading', { name: 'Bullpen', exact: true }).waitFor()
  assert.equal(await page.getByRole('navigation', { name: 'Bullpen views' }).getByRole('button', { name: 'Connect an agent', exact: true }).getAttribute('aria-pressed'), 'true')
  await page.getByRole('button', { name: 'Copy link', exact: true }).click()
  assert.equal(await page.evaluate(() => navigator.clipboard.readText()), base + '/bullpen/connect')
  await page.getByRole('button', { name: 'Agents', exact: true }).click()
  await page.waitForURL(base + '/bullpen')
  await page.goBack(); await page.waitForURL(base + '/bullpen/connect')
  await page.waitForFunction(() => document.querySelector('nav[aria-label="Bullpen views"] button[aria-pressed="true"]')?.textContent?.includes('Connect an agent'))
  await page.goForward(); await page.waitForURL(base + '/bullpen')
  await page.getByRole('navigation', { name: 'Bullpen views' }).getByRole('button', { name: 'Connect an agent', exact: true }).click()
  await page.waitForURL(base + '/bullpen/connect')
  await page.reload()
  await page.waitForFunction(() => document.querySelector('nav[aria-label="Bullpen views"] button[aria-pressed="true"]')?.textContent?.includes('Connect an agent'))
  await page.getByRole('button', { name: /^Switch product/ }).click()
  await page.getByRole('menuitem', { name: /^Goals(?:\s|$)/ }).click()
  await page.waitForURL(url => ['/goals', '/workflows'].includes(url.pathname))
  await page.goBack(); await page.waitForURL(base + '/bullpen/connect')
  await page.getByRole('heading', { name: 'Bullpen', exact: true }).waitFor()
  await page.goto(base + '/code/projects/missing')
  await page.getByRole('alert').filter({ hasText: 'This item is not available.' }).waitFor()
  const unavailable = await page.getByRole('alert').textContent()
  assert.equal(new URL(page.url()).pathname, '/code/projects/missing')
  await page.goto(base + '/sparkquill')
  await page.getByRole('alert').filter({ hasText: 'not enabled on this server' }).waitFor()
  await page.screenshot({ path: path.join(output, 'product-disabled.png') })
  assert.deepEqual(errors, [], 'no uncaught errors in navigation')
  await page.close()

  const restricted = await setup(true, ['code'])
  await restricted.page.goto(base + '/bullpen/connect')
  await restricted.page.getByRole('alert').filter({ hasText: 'This item is not available.' }).waitFor()
  assert.equal(await restricted.page.getByRole('alert').textContent(), unavailable, 'missing and forbidden have the same text')
  assert.equal(restricted.calls.some(call => call.startsWith('/api/bullpen/')), false, 'denied product never loads private data')
  await restricted.page.close()

  const validSession = await setup(true)
  await validSession.page.route('**/api/wp/**', route => route.fulfill({ status: 401, contentType: 'application/json', body: '{"error":"connection temporarily unavailable"}' }))
  await validSession.page.goto(base + '/code/projects/example')
  await validSession.page.getByRole('button', { name: 'Retry', exact: true }).waitFor()
  assert.equal(new URL(validSession.page.url()).pathname, '/code/projects/example', 'a valid session offers Retry without a sign-in loop')
  assert.equal(await validSession.page.getByLabel('Username', { exact: true }).count(), 0)
  await validSession.page.close()

  const expired = await setup(true)
  await expired.page.route('**/api/wp/**', route => route.fulfill({ status: 401, contentType: 'application/json', body: '{"error":"token expired"}' }))
  await expired.page.goto(base + '/code/projects/example')
  await expired.page.getByLabel('Username', { exact: true }).waitFor()
  assert.equal(new URL(expired.page.url()).pathname, '/code/projects/example', 'expired session returns to sign-in without losing its destination')
  await expired.page.close()

  const signedOut = await setup(false)
  await signedOut.page.goto(base + '/bullpen/connect')
  await signedOut.page.getByLabel('Username', { exact: true }).fill('reader')
  await signedOut.page.getByLabel('Password', { exact: true }).fill('example-password')
  assert.equal(signedOut.calls.some(call => call.startsWith('/api/bullpen/')), false, 'signed out never mounts Bullpen')
  await signedOut.page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await signedOut.page.getByRole('heading', { name: 'Bullpen', exact: true }).waitFor()
  assert.equal(new URL(signedOut.page.url()).pathname, '/bullpen/connect', 'password sign-in returns to the requested view')
  assert.deepEqual(signedOut.errors, [])
  await signedOut.page.screenshot({ path: path.join(output, 'product-sign-in-return.png') })
  await signedOut.page.close()
  console.log('Product URL Chromium checks passed: direct/reload/history/switch/clipboard/sign-in and protected destinations.')
} catch (error) {
  for (const context of browser?.contexts() ?? []) {
    for (const page of context.pages()) {
      console.error('Browser failure at', page.url(), await page.locator('body').innerText().catch(() => 'unavailable'))
      await page.screenshot({ path: path.join(output, 'product-url-failure.png'), fullPage: true }).catch(() => {})
    }
  }
  throw error
} finally {
  await browser?.close(); await server.close()
}
