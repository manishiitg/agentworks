import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { createServer } from 'vite'
import { chromium } from 'playwright'
const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'project-agent-tabs')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser
try {
  await mkdir(output, { recursive: true }); await server.listen(); browser = await chromium.launch({ headless: true })
  for (const [surface, width, theme] of [['work', 1280, 'dark'], ['agentworks', 1280, 'dark'], ['relays', 1280, 'light'], ['agentworks', 390, 'dark']]) {
    const page = await browser.newPage({ viewport: { width, height: 760 } })
    const errors = []; page.on('pageerror', error => errors.push(error.message))
    await page.route('**/api/**', route => route.fulfill({ json: {} }))
    await page.goto(server.resolvedUrls.local[0] + 'scripts/test-fixtures/project-agent-tabs.html?surface=' + surface + '&theme=' + theme)
    const calls = () => page.getByTestId('calls').textContent().then(value => JSON.parse(value))
    await page.getByRole('button', { name: 'New agent', exact: true }).click()
    const input = page.getByRole('textbox', { name: 'Agent name', exact: true })
    await input.waitFor(); assert.equal((await calls()).length, 0)
    await input.press('Enter'); assert.equal((await calls()).length, 0)
    await input.fill('Cost analysis'); await input.press('Enter')
    await page.waitForFunction(() => JSON.parse(document.querySelector('[data-testid="calls"]').textContent).length === 1)
    assert.deepEqual((await calls())[0], { action: 'open', data: 'Cost analysis' })
    await page.getByRole('button', { name: /^Agents: switch/ }).click()
    const menu = page.getByRole('dialog')
    assert.equal(await menu.getByRole('button', { name: 'Rename Other Builder', exact: true }).count(), 0)
    assert.equal(await menu.getByRole('button', { name: 'Close Builder', exact: true }).count(), 0)
    await menu.getByRole('button', { name: 'Rename Cost analysis', exact: true }).click()
    await menu.getByRole('textbox', { name: 'New name for Cost analysis', exact: true }).fill('Cost review')
    await menu.getByRole('textbox', { name: 'New name for Cost analysis', exact: true }).press('Enter')
    await menu.getByRole('button', { name: 'Unpin Cost review', exact: true }).click()
    await menu.getByRole('button', { name: 'Research', exact: true }).click()
    assert.equal(await page.locator('[data-chat-tab-id="created"]').count(), 0)
    await page.getByRole('button', { name: /^Agents: switch/ }).click()
    await menu.getByRole('button', { name: 'Pin Cost review to the tab bar', exact: true }).click()
    await menu.getByRole('button', { name: 'Reorder', exact: true }).click()
    await menu.getByRole('button', { name: 'Move Review earlier', exact: true }).click()
    assert.ok((await calls()).some(call => call.action === 'order' && call.data[0] === 'chat-2'))
    await menu.getByRole('button', { name: 'Done', exact: true }).click()
    await menu.getByRole('button', { name: 'Close Cost review', exact: true }).click()
    assert.ok((await calls()).some(call => call.action === 'close' && call.data === 'created'))
    await page.getByRole('button', { name: 'New agent', exact: true }).click(); await input.press('Escape')
    await page.screenshot({ path: path.join(output, 'project-tabs-' + surface + '-' + width + '.png') })
    assert.deepEqual(errors, [])
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), surface + ': no page overflow')
    console.log(surface + ' at ' + width + ': names before open; shared menu rename/pin/order/close; own tabs unmarked and other-owner controls absent.')
    await page.close()
  }
} finally { await browser?.close(); await server.close() }
