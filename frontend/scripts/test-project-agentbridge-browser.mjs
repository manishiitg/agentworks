import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { createServer } from 'vite'
import { chromium } from 'playwright'
const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'project-agentbridge')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser
try {
  await mkdir(output, { recursive: true }); await server.listen(); browser = await chromium.launch({ headless: true })
  for (const [product, target, width, theme] of [['crew', 'workspace:work:example-team', 1280, 'dark'], ['goal', 'workflow:example-goal', 390, 'dark'], ['relay', 'workflow:example-relay', 1280, 'light']]) {
    const page = await browser.newPage({ viewport: { width, height: 760 } })
    const errors = []; page.on('pageerror', error => errors.push(error.message))
    let reads = 0
    await page.route('**/api/**', async route => {
      if (!new URL(route.request().url()).pathname.startsWith('/api/')) { await route.continue(); return }
      if (route.request().url().includes('/api/agent-directory')) { reads++; await route.fulfill({ json: { agents: [
        { id: target, kind: product, name: 'Example project', chats: [{ chat_id: 'review', name: 'Review', purpose: 'Review work', owner_name: 'Taylor' }] },
        { id: 'agent:computer', target: 'agent:computer', kind: 'connected_agent', name: 'My computer agent', provider: 'Codex', online: true, presence: 'Connected', purpose: 'Help with code' },
        { id: 'workflow:other', kind: 'goal', name: 'Another project', description: 'Shared work' },
      ] } }); return }
      await route.fulfill({ json: {} })
    })
    await page.goto(server.resolvedUrls.local[0] + 'scripts/test-fixtures/crew-share-agents-qa.html?bridge=true&target=' + encodeURIComponent(target) + '&theme=' + theme)
    await page.getByRole('region', { name: "This project's agents" }).getByText('Review', { exact: true }).waitFor({ timeout: 10_000 }).catch(async error => {
      await page.screenshot({ path: path.join(output, 'agentbridge-failed-' + product + '.png'), fullPage: true })
      console.error(JSON.stringify({ product, reads, errors, page: await page.locator('body').innerText() }))
      throw error
    })
    await page.getByRole('button', { name: /Review Review work/ }).click()
    await page.waitForFunction(() => document.querySelector('[data-testid="chat-request"]').textContent.includes('chat_id'))
    assert.ok((await page.getByTestId('chat-request').textContent()).includes(target), 'current project target is retained')
    await page.getByRole('button', { name: /My computer agent/ }).click()
    await page.waitForFunction(() => document.querySelector('[data-testid="chat-request"]').textContent.includes('agent:computer'))
    assert.equal(reads, 1, 'directory loads on opening, without fixed polling or message reads')
    assert.ok(page.url().includes('bridge=true'), 'agent actions stay in the project panel')
    assert.equal(await page.getByRole('button', { name: 'Open full AgentBridge directory' }).count(), 1)
    await page.screenshot({ path: path.join(output, 'agentbridge-' + product + '.png'), fullPage: true })
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), product + ': no horizontal overflow')
    assert.deepEqual(errors, [])
    await page.close()
  }
  console.log('Project AgentBridge: Crew/Goal/Relay contextual tabs, reachable computer agent, exact target in visible chat callback, no full-page navigation, desktop/narrow/light layouts PASS')
} finally { await browser?.close(); await server.close() }
