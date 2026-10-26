// Real Home/sidebar/readiness, synthetic admitted records. No model turn or server write.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'central-home-browser')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser, page, ready = true, sent = []
try {
  await mkdir(output, { recursive: true }); await server.listen()
  browser = await chromium.launch({ headless: true })
  page = await browser.newPage({ viewport: { width: 1440, height: 1000 } }); page.setDefaultTimeout(30000)
  const errors = []; page.on('pageerror', error => errors.push(error.message))
  await page.addInitScript(() => { window.__APP_RUNTIME_CONFIG__ = { apiBaseUrl: location.origin, localServerProducts: true, enabledProductSurfaces: ['work', 'agentworks', 'relays', 'knowledgebase', 'mcp-gateway', 'bullpen'] } })
  await page.route(/^https?:\/\/[^/]+\/api\//, async route => {
    const url = new URL(route.request().url())
    let json = { items: [], sessions: [], projects: [], schedules: [], jobs: [], users: [], groups: [], providers: [], connections: [], total: 0 }
    if (url.pathname === '/api/llm-config/providers') json = { providers: [{ id: 'codex-cli', display_name: 'Codex', integration_kind: 'coding_agent', runtime_available: true }], provider_order: ['codex-cli'], integration_kinds: {} }
    if (url.pathname === '/api/provider-connections') json = { connections: ready ? [{ id: 'example-account', provider: 'codex-cli', configured: true, usable: true, relation: 'own', scope: 'user' }] : [] }
    if (url.pathname === '/api/workspaces') json = { workspaces: [{ workspace_id: 'work:notes', id: 'notes', profile_id: 'work', name: 'Research notes', workspace_path: 'Crew/notes' }] }
    if (url.pathname === '/api/chat-history/sessions') json = { sessions: [
      { session_id: 'notes-chat', title: 'Research notes', workspace_path: 'Crew/notes', updated_at: '2026-10-26T11:00:00Z' },
      { session_id: 'reading-chat', title: 'Reading target', workspace_path: 'Goals/reading', updated_at: '2026-10-26T12:00:00Z' },
      { session_id: 'weekly-chat', title: 'Weekly updates', workspace_path: 'Relays/weekly', updated_at: '2026-10-26T10:00:00Z' },
    ] }
    if (url.pathname === '/api/agentworks') { sent.push(route.request().postDataJSON()); json = { crew_id: 'my-agentworks', sent: true } }
    await route.fulfill({ json })
  })
  const url = server.resolvedUrls.local[0] + 'scripts/test-fixtures/central-home-qa.html'
  await page.goto(url)
  const home = page.getByRole('main', { name: 'Home', exact: true })
  const draft = home.getByRole('textbox', { name: 'Tell AgentWorks what you want to get done' })
  await draft.waitFor()
  await home.getByRole('link', { name: /Research notes/ }).waitFor()
  await home.getByRole('link', { name: /Reading target/ }).waitFor()
  await home.getByRole('link', { name: /Weekly updates/ }).waitFor()
  assert.equal(await home.getByRole('region', { name: 'AI account setup' }).count(), 0, 'ready shows one chat block')
  await home.getByLabel('Use cases').getByRole('button', { name: /Cowork/ }).click()
  assert.match(await draft.inputValue(), /research, prepare documents/)
  assert.equal(sent.length, 0, 'use case only fills a request')
  await page.screenshot({ path: path.join(output, 'central-home-desktop.png') })
  const footerGeometry = () => home.evaluate(element => {
    element.scrollTop = element.scrollHeight
    return element.getBoundingClientRect().bottom - element.querySelector('[aria-labelledby="welcome-tools"]').getBoundingClientRect().bottom
  })
  // Include the real global modal CSS: its child height cap used to swallow Home's padding.
  await home.locator(':scope > div').evaluate(element => element.classList.remove('max-h-none'))
  assert.ok(await footerGeometry() < 1, 'reproduce the former clipped footer with the global modal height cap')
  await home.locator(':scope > div').evaluate(element => element.classList.add('max-h-none'))
  assert.ok(await footerGeometry() >= 47, 'desktop retains 48px below the final card')
  await page.screenshot({ path: path.join(output, 'central-home-desktop-bottom.png') })
  await home.evaluate(element => { element.scrollTop = 0 })
  assert.equal(await home.getByLabel('Direct product creation').getByRole('button', { name: /^Open/ }).count(), 0)
  for (const [button, heading] of [['Create a Goal', 'Add Automation'], ['Create a Flow', 'Add Flow']]) {
    for (let attempt = 0; attempt < 2; attempt++) {
      await home.getByRole('button', { name: button, exact: true }).click()
      await page.getByRole('heading', { name: heading, exact: true }).waitFor()
      assert.equal(new URL(page.url()).pathname, '/home', 'creation must not open a remembered project')
      assert.equal(await page.locator('#preset-label').inputValue(), '', 'each setup starts blank')
      await page.locator('#preset-label').fill('Discarded draft')
      await page.getByRole('button', { name: 'Close', exact: true }).click()
      await home.waitFor()
      assert.equal(new URL(page.url()).pathname, '/home')
    }
  }
  await home.getByRole('button', { name: 'Create a Crew', exact: true }).click()
  await page.getByRole('heading', { name: 'Create a Crew', exact: true }).waitFor()
  assert.equal(new URL(page.url()).pathname, '/home')
  await page.getByRole('button', { name: 'Close', exact: true }).click()
  await home.getByRole('button', { name: 'Create Studio', exact: true }).click()
  const studio = page.getByRole('dialog')
  await studio.getByRole('heading', { name: 'Create a Studio' }).waitFor()
  assert.equal(await studio.locator('input[name="crew-type"]').count(), 2)
  await studio.getByRole('button', { name: 'Continue', exact: true }).click()
  await studio.getByText('Your private workspace', { exact: true }).waitFor()
  assert.equal(await studio.locator('input[type="checkbox"]').count(), 0)
  await page.screenshot({ path: path.join(output, 'central-home-studio.png') })
  await studio.getByRole('button', { name: 'Close', exact: true }).click()
  await page.getByRole('button', { name: 'New project', exact: true }).click()
  await draft.waitFor(); await page.waitForFunction(() => document.activeElement?.id === 'welcome-agentworks-message')
  assert.equal(await page.getByRole('menu', { name: 'New project', exact: true }).count(), 0)
  await draft.fill('Help me plan my week')
  await home.getByRole('button', { name: 'Send to AgentWorks' }).click()
  await page.waitForURL('**/crew/projects/my-agentworks')
  assert.deepEqual(sent, [{ message: 'Help me plan my week' }])
  await page.getByRole('button', { name: 'New project', exact: true }).click()
  await draft.waitFor()
  assert.equal(await draft.inputValue(), '', 'Home starts a fresh draft after a successful send')
  assert.equal(await draft.isEnabled(), true)
  for (const width of [390, 360]) {
    await page.setViewportSize({ width, height: 844 }); await page.goto(url); await draft.waitFor()
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true)
    await page.screenshot({ path: path.join(output, `central-home-mobile-${width}.png`) })
    assert.ok(await footerGeometry() >= 47, `${width}px phone retains 48px below the final card`)
    await page.screenshot({ path: path.join(output, `central-home-mobile-${width}-bottom.png`) })
  }
  ready = false
  await page.setViewportSize({ width: 1440, height: 1000 }); await page.goto(url)
  await home.getByRole('region', { name: 'AI account setup' }).waitFor()
  assert.equal(await draft.count(), 0, 'without an account show setup instead of chat')
  await page.screenshot({ path: path.join(output, 'central-home-connect-account.png') })
  assert.deepEqual(errors, [])
  console.log('Central Home browser passed: fresh Crew/Goal/Flow forms stay on Home, cancel/reopen names are blank; Studio, readiness, recents, draft-only cases, New focus, AgentWorks send and360/390px layout.')
} catch (cause) {
  await page?.screenshot({ path: path.join(output, 'central-home-failure.png') }).catch(() => {})
  throw cause
} finally { await browser?.close(); await server.close() }
