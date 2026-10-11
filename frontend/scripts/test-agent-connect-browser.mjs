// Real connect-page components/styles. Browser checks run on GitHub, not the owner's laptop.
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
    const errors = []; page.on('pageerror', error => errors.push(error.message))
    await page.route('**/api/**', route => route.fulfill({ contentType: 'application/json', body: JSON.stringify({ tokens: [], connections: [] }) }))
    await page.goto(server.resolvedUrls.local[0] + `scripts/test-fixtures/agent-connect-qa.html?theme=${theme}`)
    await page.getByRole('heading', { name: 'Connect your AI agent' }).waitFor()
    assert.equal(await page.getByLabel('Selected AgentWorks server').textContent(), 'http://127.0.0.1:18743')
    assert.match(await page.getByLabel('Sign in to this server', { exact: true }).textContent(), /agentworks --server 'http:\/\/127.0.0.1:18743' login/)
    await page.getByRole('button', { name: 'Copy Attach this chat', exact: true }).click()
    assert.equal(await page.evaluate(() => navigator.clipboard.readText()), '/agentworks attach codex-review')
    assert.equal(await page.getByLabel('Attach this chat', { exact: true }).textContent(), '/agentworks attach codex-review')
    await page.getByRole('button', { name: 'Windows', exact: true }).click()
    assert.match(await page.getByLabel('Install AgentWorks', { exact: true }).textContent(), /install-agentworks.ps1/)
    await page.getByRole('button', { name: 'macOS / Linux', exact: true }).click()
    await page.screenshot({ path: path.join(output, `agent-connect-${name}.png`), fullPage: true })
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, `${name}: no horizontal overflow`)
    await page.getByRole('button', { name: /Claude Code Live messaging plugin/ }).click()
    assert.equal(await page.getByLabel('Install coding agent plugin', { exact: true }).textContent(), 'agentworks attach')
    assert.equal(await page.getByLabel('Attach this chat', { exact: true }).textContent(), '/agentworks attach claude-review')
    assert.doesNotMatch(await page.getByLabel('Coding agent setup').textContent(), /--confirm|\/agentworks resume|\/agentworks status/)
    for (const provider of ['Cursor', 'Muse', 'Pi', 'agy']) {
      await page.getByRole('button', { name: `${provider} Other connection methods`, exact: true }).click()
      assert.match(await page.getByRole('heading', { name: `${provider} connection options` }).textContent(), /connection options/)
      assert.match(await page.getByLabel('Coding agent setup').textContent(), /installer is not available in this release/)
      assert.equal(await page.getByLabel('Attach this chat', { exact: true }).count(), 0)
    }
    await page.getByRole('button', { name: 'Open MCP setup', exact: true }).click()
    await page.getByRole('button', { name: 'Create access token', exact: true }).waitFor()
    assert.equal(await page.getByLabel('Local token access').inputValue(), 'full')
    await page.getByText('What your connections can access', { exact: true }).click()
    assert.equal(await page.getByText('Crew authoring access', { exact: true }).isVisible(), true)
    await page.getByRole('button', { name: 'Agent messaging', exact: true }).click()
    assert.equal(await page.getByRole('heading', { name: 'Connect your AI agent' }).isVisible(), true)
    assert.deepEqual(errors, [], `${name}: no component errors`)
    await page.close()
  }
  console.log('Agent connection Chromium checks passed: server-specific login, both plugin paths, copy, OS switching, adapter limits, MCP/token flow, responsive layout.')
} finally { await browser?.close(); await server.close() }
