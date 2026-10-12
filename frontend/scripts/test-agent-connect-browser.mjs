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
    const page = await browser.newPage({ viewport: { width, height: 900 }, permissions: ['clipboard-read', 'clipboard-write'], timezoneId: 'Asia/Kolkata' })
    const errors = []; page.on('pageerror', error => { errors.push(error.message); console.error('Browser component error:', error.stack) })
    page.on('console', msg => { if (['error', 'warning'].includes(msg.type()) || msg.text().includes('QA fixture')) console.error('Browser console:', msg.type(), msg.text()) })
    page.on('requestfailed', request => console.error('Browser failed request:', request.url(), request.failure()?.errorText))
    page.on('response', response => { if (response.status() >= 400 && !response.url().includes('/api/bullpen/agents')) console.error('Browser HTTP error:', response.status(), response.url()) })
    const purpose = 'Reviews workflow communication, runtime delivery and migration behavior. Coordinates exact-session native receipt checks and reports concrete follow-ups to the platform epic.'
    let directoryStatus = 200
    const sample = { reading_id: 'sample', agent_id: 'reviewer', observed_at: '2026-10-11T19:15:00Z', window_start: '2026-10-11T18:15:00Z', session: { input_tokens: 120, output_tokens: 9, cached_tokens: 90, model: 'fixture-model', source: 'Codex session total' }, delta: { input_tokens: 20, output_tokens: 5, cached_tokens: 10 }, baseline: false }
    let readings = [sample], deleted = false
    let agents = [{ agent_id: 'reviewer', agent_name: 'reviewer', target: 'agent:reviewer', provider: 'codex', online: true, purpose, usage: { ...sample, reporting: true, available: true, today: sample.delta, today_timezone: 'Asia/Kolkata' } }, { agent_id: 'writer', agent_name: 'writer', target: 'agent:writer', provider: 'claude', online: false, purpose: 'Writes documentation.' }]
    await page.route('**/api/**', route => {
      if (!new URL(route.request().url()).pathname.startsWith('/api/')) return route.continue()
      const headers = { 'access-control-allow-origin': '*', 'access-control-allow-headers': '*', 'access-control-allow-methods': 'GET, POST, DELETE, OPTIONS' }
      if (route.request().method() === 'OPTIONS') return route.fulfill({ status: 204, headers })
      if (route.request().url().includes('/api/bullpen/notices')) return route.fulfill({ headers, contentType: 'application/json', body: '{"notices":[]}' })
      if (route.request().url().includes('/api/bullpen/usage')) {
        if (route.request().method() === 'DELETE') { readings = []; agents = agents.map(({ usage: _usage, ...agent }) => agent); deleted = true; return route.fulfill({ headers, contentType: 'application/json', body: '{"deleted":true}' }) }
        return route.fulfill({ headers, contentType: 'application/json', body: JSON.stringify({ readings, before: 1, has_more: false, retention_days: 90, row_cap: 250000 }) })
      }
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
    await page.getByRole('heading', { name: purpose, exact: true }).waitFor()
    await page.getByText('Full purpose', { exact: true }).click()
    assert.equal(await page.getByText(purpose, { exact: true }).last().isVisible(), true)
    await page.getByRole('button', { name: 'Copy address agent:reviewer', exact: true }).click()
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), 'agent:reviewer')
    await page.getByText('Hourly usage history', { exact: true }).click()
    await page.getByRole('button', { name: 'Load history', exact: true }).click()
    const localDate = await page.evaluate(value => new Date(value).toLocaleDateString(), sample.observed_at)
    await page.getByLabel('Usage over time').getByText(localDate, { exact: true }).waitFor()
    assert.equal(await page.getByLabel('Usage over time').getByText(localDate, { exact: true }).count(), 1, 'UTC reading grouped by viewer local date')
    await page.screenshot({ path: path.join(output, 'bullpen-usage-' + name + '.png') })
    await page.getByRole('button', { name: 'Copy off command', exact: true }).click()
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), 'agentworks usage off')
    await page.getByRole('button', { name: 'Delete saved history', exact: true }).click()
    await page.getByRole('button', { name: 'Delete history', exact: true }).click()
    await page.getByLabel('Usage over time').getByText(localDate, { exact: true }).waitFor({ state: 'hidden' })
    await page.getByText('This session:', { exact: false }).waitFor({ state: 'hidden' })
    assert.equal(deleted, true)
    assert.equal(await page.getByLabel('Usage over time').locator('div').count(), 0)
    await page.getByLabel('Find an agent').fill('documentation')
    assert.equal(await page.getByRole('heading', { name: purpose, exact: true }).count(), 0)
    assert.equal(await page.getByRole('heading', { name: 'Writes documentation.', exact: true }).isVisible(), true)
    await page.getByLabel('Find an agent').fill('')
    directoryStatus = 403
    await page.getByRole('button', { name: 'Refresh agents', exact: true }).click()
    await page.getByRole('alert').filter({ hasText: 'not enabled' }).waitFor()
    assert.equal(await page.getByRole('heading', { name: purpose, exact: true }).count(), 0)
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
    assert.equal(await page.getByRole('link', { name: /Bullpen help/ }).getAttribute('href'), 'https://github.com/manishiitg/agentworks/blob/main/docs/bullpen.md')
    const headings = ['Requirements and tested versions', 'Install or update the plugin', 'Sign in to this server', 'Connect this chat', 'Commands and menus', 'Try a message', 'Troubleshooting']
    for (const client of ['Codex', 'Claude Code', 'Pi', 'Cursor', 'agy', 'Muse']) {
      await page.getByRole('button', { name: client, exact: true }).click()
      for (const heading of headings) assert.equal(await page.getByRole('heading', { name: heading, exact: true }).count(), 1, client + ': consistent section ' + heading)
      if (client === 'agy') {
        assert.equal(await page.getByLabel('Attach this chat', { exact: true }).count(), 0)
        assert.match(await page.getByLabel('Coding agent setup').textContent(), /does not create a coding-chat messaging attachment/)
      } else assert.equal(await page.getByLabel('Attach this chat', { exact: true }).count(), 1)
      if (client === 'Codex') {
        assert.equal(await page.getByRole('button', { name: 'Codex app', exact: true }).getAttribute('aria-pressed'), 'true')
        assert.equal(await page.getByLabel('Install in the Codex app').isVisible(), true)
        assert.equal(await page.getByLabel('Install Codex plugin', { exact: true }).count(), 0)
        assert.match(await page.getByLabel('Install in the Codex app').textContent(), /Add → Add a marketplace/)
        assert.match(await page.getByLabel('Install in the Codex app').textContent(), /Settings → Hooks/)
        await page.screenshot({ path: path.join(output, 'bullpen-codex-app-' + name + '.png'), fullPage: true })
        await page.getByRole('heading', { name: 'Install or update the plugin', exact: true }).scrollIntoViewIfNeeded()
        await page.getByText('Terminal alternative after sign-in', { exact: true }).click()
        await page.getByRole('button', { name: 'Copy Set up Codex app from Terminal', exact: true }).click()
        assert.equal(await page.evaluate(() => navigator.clipboard.readText()), 'agentworks attach codex-review')
        await page.screenshot({ path: path.join(output, 'bullpen-codex-app-install-' + name + '.png'), fullPage: true })
        await page.getByRole('button', { name: 'Codex CLI', exact: true }).click()
        assert.equal(await page.getByLabel('Install in the Codex app').count(), 0)
        assert.equal(await page.getByLabel('Install Codex plugin', { exact: true }).isVisible(), true)
        await page.getByRole('button', { name: 'Copy Install Codex plugin', exact: true }).click()
        assert.equal(await page.evaluate(() => navigator.clipboard.readText()), 'codex plugin marketplace add manishiitg/agentworks-plugins\ncodex plugin add agentworks-codex@agentworks')
        await page.screenshot({ path: path.join(output, 'bullpen-codex-cli-' + name + '.png'), fullPage: true })
        await page.getByRole('button', { name: 'Codex app', exact: true }).click()
      }
      if (client === 'Cursor') {
        assert.equal(await page.getByLabel('Configure Cursor project hooks', { exact: true }).isVisible(), true)
        await page.getByRole('button', { name: 'Cursor desktop', exact: true }).click()
        assert.equal(await page.getByLabel('Attach this chat', { exact: true }).count(), 0)
        assert.match(await page.getByLabel('Coding agent setup').textContent(), /not claim end-to-end desktop messaging/)
        await page.screenshot({ path: path.join(output, 'bullpen-cursor-desktop-limit-' + name + '.png'), fullPage: true })
        await page.getByRole('button', { name: 'Cursor CLI', exact: true }).click()
        assert.equal(await page.getByLabel('Attach this chat', { exact: true }).isVisible(), true)
        await page.screenshot({ path: path.join(output, 'bullpen-cursor-cli-' + name + '.png'), fullPage: true })
      }
      assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, name + ': no overflow for ' + client)
    }
    assert.equal(await page.getByLabel('Add Muse marketplace', { exact: true }).textContent(), 'muse plugins marketplace add agentworks https://github.com/manishiitg/agentworks-plugins.git')
    assert.equal(await page.getByLabel('Attach this chat', { exact: true }).textContent(), '/agentworks-muse:agentworks attach muse-review')
    const museSetup = await page.getByLabel('Coding agent setup').textContent()
    assert.match(museSetup, /native Monitor wakes this attached chat/)
    assert.match(museSetup, /without polling/)
    assert.match(museSetup, /leave the chat idle/)
    assert.match(museSetup, /native tool approval/)
    assert.match(museSetup, /agentworks-muse:messaging/)
    assert.match(museSetup, /agentworks-muse:monitor/)
    await page.getByText('Updating an older installation', { exact: true }).click()
    assert.match(await page.getByLabel('Update Muse plugin', { exact: true }).textContent(), /marketplace update.*\n.*plugins remove.*\n.*plugins install/)
    await page.getByRole('heading', { name: 'Bullpen', exact: true }).scrollIntoViewIfNeeded()
    await page.screenshot({ path: path.join(output, 'bullpen-setup-' + name + '.png'), fullPage: true })
    await page.getByRole('heading', { name: 'Commands and menus', exact: true }).scrollIntoViewIfNeeded()
    await page.screenshot({ path: path.join(output, 'bullpen-muse-menu-' + name + '.png'), fullPage: true })
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
