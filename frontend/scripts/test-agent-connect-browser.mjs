// Real Bullpen and MCP components/styles; browser checks run on GitHub.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createServer } from 'vite'
import { chromium } from 'playwright'
const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'agent-connect-browser')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser
try {
  await mkdir(output, { recursive: true }); await server.listen()
  browser = await chromium.launch({ headless: true })
  for (const [name, width, theme] of [['desktop', 1200, 'light'], ['mobile', 375, 'light'], ['dark', 1200, 'dark']]) {
    const page = await browser.newPage({ viewport: { width, height: 900 }, permissions: ['clipboard-read', 'clipboard-write'] })
    const errors = []; page.on('pageerror', error => { errors.push(error.message); console.error('Browser component error:', error.stack) })
    page.on('console', msg => { if (['error', 'warning'].includes(msg.type()) || msg.text().includes('QA fixture')) console.error('Browser console:', msg.type(), msg.text()) })
    page.on('requestfailed', request => console.error('Browser failed request:', request.url(), request.failure()?.errorText))
    page.on('response', response => { if (response.status() >= 400 && !response.url().includes('/api/bullpen/agents')) console.error('Browser HTTP error:', response.status(), response.url()) })
    const purpose = 'Reviews workflow communication, runtime delivery and migration behavior. Coordinates exact-session native receipt checks and reports concrete follow-ups to the platform epic.'
    let directoryStatus = 200
    let agents = [{ agent_id: 'reviewer', agent_name: 'reviewer', target: 'agent:reviewer', provider: 'codex', online: true, purpose }, { agent_id: 'writer', agent_name: 'writer', target: 'agent:writer', provider: 'claude', online: false, purpose: 'Writes documentation.' }]
    await page.route('**/api/**', route => {
      if (!new URL(route.request().url()).pathname.startsWith('/api/')) return route.continue()
      const headers = { 'access-control-allow-origin': '*', 'access-control-allow-headers': '*', 'access-control-allow-methods': 'GET, POST, DELETE, OPTIONS' }
      if (route.request().method() === 'OPTIONS') return route.fulfill({ status: 204, headers })
      if (route.request().url().includes('/api/bullpen/agents')) return route.fulfill({ headers, status: directoryStatus, contentType: 'application/json', body: JSON.stringify({ agents }) })
      return route.fulfill({ headers, contentType: 'application/json', body: JSON.stringify({ tokens: [], connections: [], profiles: [], providers: [], presets: [], projects: [], sessions: [] }) })
    })
    const url = server.resolvedUrls.local[0] + 'scripts/test-fixtures/agent-connect-qa.html'
    await page.goto(url + '?theme=' + theme)
    try { await page.getByRole('heading', { name: 'Your agents', exact: true }).waitFor() }
    catch (error) {
      await page.screenshot({ path: path.join(output, 'bullpen-failure-' + name + '.png'), fullPage: true })
      console.error('Fixture body:', await page.locator('body').innerText(), 'component errors:', errors, 'scripts:', await page.locator('script').evaluateAll(nodes => nodes.map(node => node.src)))
      throw error
    }
    await page.getByRole('heading', { name: 'reviewer', exact: true }).waitFor()
    await page.getByText('Full purpose', { exact: true }).click()
    assert.equal(await page.getByText(purpose, { exact: true }).last().isVisible(), true)
    await page.getByRole('button', { name: 'Copy address agent:reviewer', exact: true }).click()
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), 'agent:reviewer')
    await page.getByLabel('Find an agent').fill('documentation')
    assert.equal(await page.getByRole('heading', { name: 'reviewer', exact: true }).count(), 0)
    assert.equal(await page.getByRole('heading', { name: 'writer', exact: true }).isVisible(), true)
    await page.getByLabel('Find an agent').fill('')
    directoryStatus = 403
    await page.getByRole('button', { name: 'Refresh agents', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: 'not enabled' }).waitFor()
    assert.equal(await page.getByRole('heading', { name: 'reviewer', exact: true }).count(), 0)
    directoryStatus = 200; agents = []
    await page.getByRole('button', { name: 'Refresh agents', exact: true }).click()
    await page.getByRole('heading', { name: 'Bring your first agent into Bullpen' }).waitFor()
    await page.getByRole('button', { name: 'Set up messaging', exact: true }).click()
    await page.getByRole('heading', { name: 'Connect your AI agent' }).waitFor()
    assert.equal(await page.getByLabel('Selected AgentWorks server').textContent(), 'http://127.0.0.1:18743')
    assert.equal(await page.getByLabel('Connect this chat', { exact: true }).textContent(), 'Attach this chat to AgentWorks as codex-review.')
    await page.getByRole('button', { name: 'Copy Connect this chat', exact: true }).click()
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), 'Attach this chat to AgentWorks as codex-review.')
    await page.getByRole('button', { name: 'Windows', exact: true }).click()
    assert.match(await page.getByLabel('Install AgentWorks', { exact: true }).textContent(), /install-agentworks.ps1/)
    await page.getByRole('button', { name: 'Claude Code Live messaging plugin', exact: true }).click()
    assert.equal(await page.getByLabel('Attach this chat', { exact: true }).textContent(), 'agentworks attach claude-review')
    await page.getByRole('button', { name: 'Muse Idle Monitor and hooks', exact: true }).click()
    assert.equal(await page.getByLabel('Add Muse marketplace', { exact: true }).textContent(), 'muse plugins marketplace add agentworks https://github.com/manishiitg/agentworks.git')
    assert.equal(await page.getByLabel('Attach this chat', { exact: true }).textContent(), '/agentworks-muse:agentworks attach muse-review')
    const museSetup = await page.getByLabel('Coding agent setup').textContent()
    assert.match(museSetup, /native Monitor wakes this attached chat/)
    assert.match(museSetup, /without polling/)
    assert.match(museSetup, /leave the chat idle/)
    assert.match(museSetup, /review any native tool approval/)
    assert.doesNotMatch(museSetup, /Native push into an idle Muse chat is not available/)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, name + ': no overflow')
    await page.screenshot({ path: path.join(output, 'bullpen-setup-' + name + '.png'), fullPage: true })
    await page.goto(url + '?mcp&theme=' + theme)
    await page.getByRole('button', { name: 'Create access token', exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: 'Open Bullpen', exact: true }).isVisible(), true)
    assert.equal(await page.getByLabel('Coding agent setup').count(), 0)
    await page.getByText('What your connections can access', { exact: true }).click()
    assert.equal(await page.getByText('Crew authoring access', { exact: true }).isVisible(), true)
    await page.screenshot({ path: path.join(output, 'mcp-tools-' + name + '.png'), fullPage: true })
    await page.goto(url + '?mcp&restricted')
    await page.getByRole('button', { name: 'Create access token', exact: true }).waitFor()
    assert.equal(await page.getByRole('button', { name: 'Open Bullpen', exact: true }).count(), 0)
    assert.deepEqual(errors, [], name + ': no component errors')
    await page.close()
  }
  console.log('Bullpen Chromium checks passed: account directory, full purpose, search/copy, failure/empty recovery, own-attach setup, MCP separation, product allowlist and responsive layout.')
} finally { await browser?.close(); await server.close() }
