import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
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
    page.on('console', message => { if (message.type() === 'error') console.error('Browser console:', message.text()) })
    const errors = []; page.on('pageerror', error => { errors.push(error.message); console.error('Browser page error:', error.message) })
    await page.route('**/api/**', route => new URL(route.request().url()).pathname.startsWith('/api/') ? route.fulfill({ json: {} }) : route.continue())
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
    await menu.waitFor()
    assert.equal(await menu.evaluate(node => { const box = node.getBoundingClientRect(); return node.contains(document.elementFromPoint(box.x + box.width / 2, box.y + Math.min(20, box.height / 2))) }), true, surface + ': menu receives clicks outside clipping header')
    await page.screenshot({ path: path.join(output, 'project-menu-' + surface + '-' + width + '.png') })
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
  // PLAT-746-230: real sidebar and header components; only server/network boundaries are fixtures.
  for (const active of ['local', 'a'.repeat(32)]) {
    const page = await browser.newPage({ viewport: { width: 1280, height: 800 } })
    page.on('console', message => { if (message.type() === 'error') console.error('Browser console:', message.text()) })
    const errors = []; page.on('pageerror', error => { errors.push(error.message); console.error('Browser page error:', error.message) })
    await page.addInitScript(active => {
      window.__APP_RUNTIME_CONFIG__ = { deploymentMode: 'local', enabledProductSurfaces: ['work', 'agentworks'] }
      window.qaConnections = []
      window.electronAPI = {
        getDesktopConnection: () => active,
        getApiBaseUrl: () => location.origin,
        getDesktopApiBaseUrl: () => location.origin,
        openDesktopConnection: async (id, path) => { window.qaConnections.push({ id, path }) },
      }
    }, active)
    await page.route('**/api/**', route => {
      const url = new URL(route.request().url())
      if (!url.pathname.startsWith('/api/')) return route.continue()
      if (url.pathname === '/api/desktop-connections') return route.fulfill({ json: { connections: [
        { id: 'a'.repeat(32), name: 'Team', server: 'https://team.example.com' },
        { id: 'b'.repeat(32), name: 'Sandbox', server: 'https://sandbox.example.com' },
      ] } })
      if (url.pathname.endsWith('/inventory')) return route.fulfill({ json: { status: 'connected', projects: [
        { id: 'example', name: 'Example Crew', kind: 'crew' }, { id: 'launch', name: 'Launch plan', kind: 'goal' },
      ] } })
      if (url.pathname.endsWith('/api/workspaces')) return route.fulfill({ json: { workspaces: [
        { id: 'example', workspace_id: 'work:example', profile_id: 'work', name: 'Example Crew', icon: '🧩', workspace_path: 'Crew/example' },
      ] } })
      return route.fulfill({ json: {} })
    })
    await page.goto(server.resolvedUrls.local[0] + 'scripts/test-fixtures/project-agent-tabs.html?surface=work&sidebar=1')
    const sidebar = page.getByRole('complementary', { name: 'Projects', exact: true })
    for (const name of ['Local', 'Team', 'Sandbox']) {
      const group = sidebar.getByRole('region', { name: name + ' projects', exact: true })
      await group.getByRole('button', { name: 'Example Crew · Crew', exact: true }).waitFor()
      assert.equal(await group.getByRole('button', { name: 'Example Crew · Crew', exact: true }).count(), 1)
      assert.equal(await group.getByRole('button', { name: 'Launch plan · Goal', exact: true }).count(), 1)
    }
    assert.equal(await sidebar.getByText('Research', { exact: true }).count(), 0)
    await page.getByTestId('work-chat-tab-strip').waitFor()
    assert.equal(await page.locator('[data-chat-tab-id="chat-1"]').isVisible(), true, 'open sidebar keeps header chat tabs')
    await page.getByRole('button', { name: /Active work:/ }).waitFor()
    await page.screenshot({ path: path.join(output, `simple-sidebar-${active === 'local' ? 'local' : 'remote'}.png`) })
    // Selecting the same project closes global pages even though its URL did not change.
    await page.evaluate(async () => {
      history.replaceState({}, '', '/crew/projects/example')
      const { useAppStore } = await import('/src/stores/useAppStore.ts')
      useAppStore.getState().setShowSchedulesOverview(true)
    })
    const currentGroup = sidebar.getByRole('region', { name: (active === 'local' ? 'Local' : 'Team') + ' projects', exact: true })
    await currentGroup.getByRole('button', { name: 'Example Crew · Crew', exact: true }).click()
    assert.equal(await page.evaluate(async () => (await import('/src/stores/useAppStore.ts')).useAppStore.getState().showSchedulesOverview), false)

    const remote = sidebar.getByRole('region', { name: 'Sandbox projects', exact: true })
    await remote.getByRole('button', { name: 'Example Crew · Crew', exact: true }).click()
    assert.deepEqual(await page.evaluate(() => window.qaConnections.at(-1)), { id: 'b'.repeat(32), path: '/crew/projects/example' })
    await remote.getByRole('button', { name: 'Sandbox', exact: true }).click()
    assert.equal(await remote.getByRole('button', { name: 'Example Crew · Crew', exact: true }).isVisible(), false)
    assert.equal(await page.getByTestId('work-chat-tab-strip').isVisible(), true)
    assert.deepEqual(errors, [])
    console.log(`Simple sidebar (${active}): project-only server groups, header chats, monitor and remote routing passed.`)
    await page.close()
  }

} catch (error) {
  for (const [index, page] of (browser?.contexts().flatMap(context => context.pages()) || []).entries()) {
    await page.screenshot({ path: path.join(output, `failure-${index}.png`) }).catch(() => {})
    await writeFile(path.join(output, `failure-${index}.html`), await page.content()).catch(() => {})
  }
  throw error
} finally { await browser?.close(); await server.close() }
