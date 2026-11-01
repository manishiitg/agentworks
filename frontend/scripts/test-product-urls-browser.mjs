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
  async function setup(signedIn, allowed = ['agentworks', 'relays', 'bullpen', 'code', 'knowledgebase'], { codeProject = false, codeReports = false, defaultProduct = 'agentworks' } = {}) {
    const page = await browser.newPage({ viewport: { width: 1200, height: 900 } })
    const user = { id: signedIn ? 'qa-reader' : 'qa-new-reader', username: 'reader', role: 'reader', allowed_products: allowed, can_run_workflows: true, can_write_workflows: false, is_admin: false }
    const errors = [], calls = []
    page.on('pageerror', error => { errors.push(error.message); console.error('Browser error:', error.stack) })
    await page.addInitScript(({ signedIn }) => {
      if (signedIn && !localStorage.getItem('workspace-connection-store')) localStorage.setItem('auth_token', 'qa-token')
      for (const key of ['agentworks_welcome_home_v1_dismissed', 'llm_discovery_onboarding_dismissed', 'agentworks_overview_walkthrough_v3_dismissed', 'agentworks_empty_automation_walkthrough_v3_dismissed', 'agentworks_empty_code_walkthrough_v1_dismissed', 'agentworks_code_walkthrough_v1_dismissed']) localStorage.setItem(key, 'true')
      // Explicit route must override a conflicting remembered product and tab.
      if (!localStorage.getItem('agentworks-product-surface')) localStorage.setItem('agentworks-product-surface', JSON.stringify({ state: { productSurface: 'agentworks' }, version: 6 }))
      localStorage.setItem('bullpen:view', 'agents')
    }, { signedIn })
    await page.route('**/runtime-config.js*', route => route.fulfill({ contentType: 'text/javascript', body: `window.__APP_RUNTIME_CONFIG__=${JSON.stringify({ apiBaseUrl: base, workspaceApiBaseUrl: base + '/api/wp', deploymentMode: 'server', enabledProductSurfaces: ['agentworks', 'relays', 'bullpen', 'code', 'knowledgebase'], defaultProductSurface: defaultProduct })}` }))
    await page.route('**/api/**', async route => {
      const url = new URL(route.request().url())
      if (!url.pathname.startsWith('/api/')) return route.continue()
      calls.push(url.pathname)
      let data = { files: [], workflows: [], agents: [], groups: [], notices: [], tokens: [], connections: [], profiles: [], providers: [], presets: [], projects: [], sessions: [], models: [], llms: [], steps: [], tools: [], servers: [], skills: [] }
      if (url.pathname === '/api/health') data = { status: 'healthy' }
      if (url.pathname === '/api/auth/mode') data = { multi_user_mode: true, providers: [{ name: 'simple', display_name: 'Password', type: 'credentials', configured: true }] }
      if (url.pathname === '/api/auth/me') data = user
      if (url.pathname === '/api/auth/login') data = { token: 'qa-token', user }
      if (codeProject && url.pathname.endsWith('/api/documents')) {
        if (url.searchParams.get('folder') === 'Chats/Code/projects') {
          // Reproduce the live sequence: the product history entry is written
          // before its selected project and default Files view finish loading.
          await new Promise(resolve => setTimeout(resolve, 150))
          data = { data: [{ filepath: 'Chats/Code/projects/example/product.json', type: 'file' }] }
        } else if (codeReports && url.searchParams.get('folder') === 'Chats/Code/projects/example/db/reports') {
          data = { data: ['index.html', 'activity.html'].map(name => ({ filepath: 'Chats/Code/projects/example/db/reports/' + name, type: 'file' })) }
        } else data = { data: [] }
      }
      if (codeProject && decodeURIComponent(url.pathname).endsWith('/Chats/Code/projects/example/product.json')) data = { data: { content: JSON.stringify({ schema_version: 1, product: 'code', id: 'example', title: 'Example project', session_id: 'qa-code-session' }) } }
      if (codeReports && /\/Chats\/Code\/projects\/example\/db\/reports\/(index|activity)\.html$/.test(decodeURIComponent(url.pathname))) {
        const title = decodeURIComponent(url.pathname).endsWith('/activity.html') ? 'Activity' : 'Overview'
        data = { success: true, data: { content: `<html><head><title>${title}</title></head><body><h1>${title}</h1></body></html>` } }
      }
      if (url.pathname === '/api/knowledgebase/bootstrap') data = { chat_workspace: 'Chats/Knowledgebase', backup_configured: true, is_admin: false }
      if (url.pathname === '/api/knowledgebase/access-proposals') data = { proposals: [] }
      if (url.pathname === '/api/knowledgebase/folders') data = { folders: [] }
      if (url.pathname === '/api/knowledgebase/entries') data = { entries: [] }
      if (url.pathname.endsWith('/conversation')) data = { conversation_id: 'qa-conversation', conversation_key: 'main', session_id: url.pathname.includes('/code/') ? 'qa-code-session' : 'qa-brain-session' }
      if (/^\/api\/sessions\/[^/]+\/events$/.test(url.pathname)) data = { events: [], session_status: 'completed', has_more: false, last_event_index: -1 }
      return route.fulfill({ contentType: 'application/json', body: JSON.stringify(data) })
    })
    return { page, calls, errors }
  }
  const { page, errors } = await setup(true)
  await page.goto(base + '/agentbridge/connect')
  await page.getByRole('heading', { name: 'AgentBridge', exact: true }).waitFor()
  await page.setViewportSize({ width: 1920, height: 1080 })
  await page.screenshot({ path: path.join(output, 'bullpen-connect-wide.png') })
  assert.equal(await page.getByRole('navigation', { name: 'AgentBridge views' }).getByRole('button', { name: 'Connect an agent', exact: true }).getAttribute('aria-pressed'), 'true')
  assert.equal(await page.getByRole('button', { name: 'Copy link', exact: true }).count(), 0)
  assert.equal(page.url(), base + '/agentbridge/connect', 'the address bar exposes the shareable product link')
  await page.getByRole('button', { name: 'Agents', exact: true }).click()
  await page.waitForURL(base + '/agentbridge')
  await page.getByRole('heading', { name: 'Bring your first agent into AgentBridge', exact: true }).waitFor()
  await page.screenshot({ path: path.join(output, 'bullpen-agents-wide.png') })
  await page.goBack(); await page.waitForURL(base + '/agentbridge/connect')
  await page.waitForFunction(() => document.querySelector('nav[aria-label="AgentBridge views"] button[aria-pressed="true"]')?.textContent?.includes('Connect an agent'))
  await page.goForward(); await page.waitForURL(base + '/agentbridge')
  await page.getByRole('navigation', { name: 'AgentBridge views' }).getByRole('button', { name: 'Connect an agent', exact: true }).click()
  await page.waitForURL(base + '/agentbridge/connect')
  await page.reload()
  await page.waitForFunction(() => document.querySelector('nav[aria-label="AgentBridge views"] button[aria-pressed="true"]')?.textContent?.includes('Connect an agent'))
  await page.getByRole('button', { name: /^Switch product/ }).click()
  await page.getByRole('menuitem', { name: /^Goals(?:\s|$)/ }).click()
  await page.waitForURL(url => ['/goals', '/workflows'].includes(url.pathname))
  await page.goBack(); await page.waitForURL(base + '/agentbridge/connect')
  await page.getByRole('heading', { name: 'AgentBridge', exact: true }).waitFor()
  await page.goto(base + '/code/projects/missing')
  await page.getByRole('alert').filter({ hasText: 'This item is not available.' }).waitFor()
  const unavailable = await page.getByRole('alert').textContent()
  assert.equal(new URL(page.url()).pathname, '/code/projects/missing')
  await page.goto(base + '/sparkquill')
  await page.getByRole('alert').filter({ hasText: 'not available' }).waitFor()
  await page.screenshot({ path: path.join(output, 'product-disabled.png') })
  assert.deepEqual(errors, [], 'no uncaught errors in navigation')
  await page.close()

  const products = await setup(true, undefined, { codeProject: true, defaultProduct: 'code' })
  await products.page.goto(base + '/settings')
  await products.page.getByRole('heading', { name: 'Choose a Code workspace', exact: true }).waitFor()
  assert.equal(new URL(products.page.url()).pathname, '/code')
  assert.equal(await products.page.getByRole('button', { name: 'Create your first Code workspace', exact: true }).count(), 0)
  await products.page.getByRole('button', { name: 'Choose a Code workspace', exact: true }).click()
  await products.page.getByRole('menuitemradio', { name: 'Example project', exact: true }).click()
  await products.page.locator('[data-tour="crew-workspace"][data-ui-view="files"]').waitFor()
  await products.page.waitForURL(base + '/code/projects/example')
  await products.page.goto(base + '/agentbridge/connect')
  await products.page.getByRole('heading', { name: 'AgentBridge', exact: true }).waitFor()
  async function switchProduct(name) {
    await products.page.getByRole('button', { name: /^Switch product/ }).click()
    await products.page.getByRole('menuitem', { name: new RegExp('^' + name + '(?:\\s|$)') }).click()
  }
  await switchProduct('Brain')
  await products.page.waitForURL(url => url.pathname === '/brain')
  await products.page.getByRole('button', { name: 'Refresh Brain', exact: true }).waitFor()
  await switchProduct('Code')
  await products.page.locator('[data-tour="crew-workspace"][data-ui-view="files"]').waitFor()
  await products.page.waitForURL(base + '/code/projects/example')
  // Explicit toolbar changes still create an entry, including a return to Files.
  await products.page.getByRole('button', { name: 'Dashboard', exact: true }).click()
  await products.page.waitForURL(base + '/code/projects/example/dashboard')
  await products.page.goBack(); await products.page.waitForURL(base + '/code/projects/example')
  await products.page.locator('[data-tour="crew-workspace"][data-ui-view="files"]').waitFor()
  await products.page.goBack(); await products.page.waitForURL(url => url.pathname === '/brain')
  await products.page.getByRole('button', { name: 'Refresh Brain', exact: true }).waitFor()
  await products.page.goBack(); await products.page.waitForURL(base + '/agentbridge/connect')
  await products.page.getByRole('heading', { name: 'AgentBridge', exact: true }).waitFor()
  await products.page.goForward(); await products.page.waitForURL(url => url.pathname === '/brain')
  await products.page.getByRole('button', { name: 'Refresh Brain', exact: true }).waitFor()
  await products.page.goForward(); await products.page.waitForURL(base + '/code/projects/example')
  await products.page.locator('[data-tour="crew-workspace"][data-ui-view="files"]').waitFor()
  assert.deepEqual(products.errors, [], 'no uncaught errors through linked project hydration')
  await products.page.screenshot({ path: path.join(output, 'product-history-project.png') })
  await products.page.close()

  const reports = await setup(true, undefined, { codeProject: true, codeReports: true })
  await reports.page.goto(base + '/code/projects/example/dashboard')
  const reportSwitcher = reports.page.getByRole('button', { name: 'Dashboard: Overview', exact: true })
  await reportSwitcher.waitFor()
  assert.equal(await reports.page.getByRole('button', { name: /copy.*link/i }).count(), 0, 'dashboard toolbar has no link-copy control')
  await reportSwitcher.click()
  const reportMenu = reports.page.getByRole('menu', { name: 'Reports', exact: true })
  await reportMenu.waitFor()
  assert.equal(await reportMenu.getByRole('button', { name: /copy.*link/i }).count(), 0, 'report menu has no link-copy control')
  await reports.page.screenshot({ path: path.join(output, 'dashboard-report-menu.png') })
  await reportMenu.getByRole('menuitemradio', { name: 'Activity', exact: true }).click()
  await reports.page.getByRole('button', { name: 'Dashboard: Activity', exact: true }).waitFor()
  assert.equal(new URL(reports.page.url()).pathname, '/code/projects/example/dashboard')
  assert.deepEqual(reports.errors, [], 'report selection still works without link-copy controls')
  await reports.page.close()

  const restricted = await setup(true, ['code'])
  await restricted.page.goto(base + '/agentbridge/connect')
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
  await signedOut.page.goto(base + '/agentbridge/connect')
  await signedOut.page.getByLabel('Username', { exact: true }).fill('reader')
  await signedOut.page.getByLabel('Password', { exact: true }).fill('example-password')
  assert.equal(signedOut.calls.some(call => call.startsWith('/api/bullpen/')), false, 'signed out never mounts AgentBridge')
  await signedOut.page.getByRole('button', { name: 'Sign in', exact: true }).click()
  await signedOut.page.getByRole('heading', { name: 'AgentBridge', exact: true }).waitFor()
  assert.equal(new URL(signedOut.page.url()).pathname, '/agentbridge/connect', 'password sign-in returns to the requested view')
  assert.deepEqual(signedOut.errors, [])
  await signedOut.page.screenshot({ path: path.join(output, 'product-sign-in-return.png') })
  await signedOut.page.close()
  console.log('Product URL Chromium checks passed: address-bar links/direct/reload/history/switch/sign-in and protected destinations.')
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
