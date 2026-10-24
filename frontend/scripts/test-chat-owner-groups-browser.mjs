// The production grouping and tab pills in Chromium; no backend or account changes.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'chat-owner-groups')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser
try {
  await mkdir(output, { recursive: true })
  await server.listen()
  browser = await chromium.launch({ headless: true })
  for (const [name, width, theme] of [['desktop', 1086, 'dark'], ['narrow', 375, 'dark'], ['light', 1086, 'light']]) {
    const page = await browser.newPage({ viewport: { width, height: 430 }, deviceScaleFactor: 2 })
    const errors = []
    page.on('pageerror', error => errors.push(error.message))
    await page.goto(server.resolvedUrls.local[0] + 'scripts/test-fixtures/chat-owner-groups-qa.html?theme=' + theme)
    await page.locator('[data-chat-owner-label]').first().waitFor()
    for (const surface of ['work', 'agentworks', 'relays']) {
      const section = page.locator('[data-surface="' + surface + '"]')
      const group = section.locator('[data-chat-owner-group="other"]')
      assert.equal(await section.locator('[data-chat-owner-label]').count(), 1, surface + ': one owner heading')
      assert.equal(await group.locator('[data-chat-owner-label]').textContent(), 'Alex T.')
      assert.equal(await group.locator('[data-chat-owner-label]').getAttribute('title'), 'alex.taylor@example.com')
      assert.equal(await section.locator('[data-chat-owner-group="mine"]').count(), 0, 'own tabs unmarked')
      assert.equal(await section.locator('[aria-label^="Tab owned by"]').count(), 0, 'no repeated avatars')
      assert.equal(await group.locator('[role="button"]').count(), 2, 'both foreign tabs share one owner')
    }
    await page.screenshot({ path: path.join(output, 'chat-owner-groups-' + name + '.png'), fullPage: true })
    const measuredWidth = await page.evaluate(() => ({ document: document.documentElement.scrollWidth, viewport: window.innerWidth }))
    assert.ok(measuredWidth.document <= measuredWidth.viewport, name + ': tab strip contains overflow ' + JSON.stringify(measuredWidth))
    for (const surface of ['work', 'agentworks', 'relays']) {
      const group = page.locator('[data-surface="' + surface + '"] [data-chat-owner-group="other"]')
      await group.getByText('Testing', { exact: true }).click()
      assert.equal(await group.locator('[role="button"]').first().getAttribute('data-tab-active'), 'true', surface + ': mouse switching retained')
      const tab = group.locator('[role="button"]').last()
      await tab.focus()
      await tab.press('Enter')
      assert.equal(await tab.getAttribute('data-tab-active'), 'true', surface + ': keyboard switching retained')
      assert.equal(await group.locator('[data-chat-owner-label]').count(), 1)
    }
    assert.deepEqual(errors, [], name + ': no component errors')
    await page.close()
  }
  console.log('Chat owner groups Chromium passed: Crew, Goal and Relay; one owner label, unmarked own tabs, keyboard switching, dark/light and narrow scrolling.')
} finally {
  await browser?.close()
  await server.close()
}
