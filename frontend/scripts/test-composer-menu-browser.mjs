import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'composer-menu-browser')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser, page
try {
  await mkdir(output, { recursive: true }); await server.listen()
  browser = await chromium.launch({ headless: true })
  page = await browser.newPage(); page.setDefaultTimeout(15000)
  const errors = []; page.on('pageerror', error => errors.push(error.message))
  const geometry = []
  // Browser/Electron page zoom reduces the CSS viewport; the app does not use CSS zoom.
  for (const [name, width, height] of [['desktop', 1280, 720], ['short-phone', 390, 360], ['zoom-equivalent', 853, 480]]) {
    await page.setViewportSize({ width, height })
    await page.goto(server.resolvedUrls.local[0] + 'scripts/test-fixtures/composer-menu-qa.html')
    await page.getByRole('button', { name: 'Add files and actions', exact: true }).click()
    const menu = page.getByRole('menu', { name: 'Add files and actions' })
    await menu.waitFor()
    await menu.evaluate(() => new Promise(resolve => requestAnimationFrame(() => requestAnimationFrame(resolve))))
    const metrics = await menu.evaluate(element => {
      const rect = element.getBoundingClientRect()
      return { top: rect.top, bottom: rect.bottom, left: rect.left, right: rect.right, height: rect.height, scroll: element.scrollHeight, client: element.clientHeight, viewport: innerHeight }
    })
    geometry.push({ name, ...metrics })
    assert.ok(metrics.top >= 11 && metrics.bottom <= height - 11, `${name}: menu stays inside the viewport: ${JSON.stringify(metrics)}`)
    assert.ok(metrics.left >= 11 && metrics.right <= width - 11, `${name}: menu fits horizontally`)
    if (name === 'desktop') assert.ok(metrics.scroll <= metrics.client + 1, 'use available desktop height instead of cutting off the last actions at 22rem')
    if (name === 'short-phone') assert.ok(metrics.scroll > metrics.client, 'short screens scroll the full menu')
    // Keyboard navigation must bring the final action into view without dismissing the menu.
    await menu.press('End')
    const stop = page.getByRole('menuitem', { name: 'Stop current turn', exact: true })
    await stop.scrollIntoViewIfNeeded()
    const visible = await stop.evaluate(element => {
      const rect = element.getBoundingClientRect(), menu = element.closest('[role="menu"]').getBoundingClientRect()
      return rect.top >= menu.top && rect.bottom <= menu.bottom
    })
    assert.ok(visible, `${name}: final action is fully reachable`)
    await menu.evaluate(element => { element.scrollTop = element.scrollHeight })
    await page.screenshot({ path: path.join(output, `composer-menu-${name}.png`) })
    await page.keyboard.press('Escape')
  }
  assert.deepEqual(errors, [])
  await writeFile(path.join(output, 'composer-menu-geometry.json'), JSON.stringify(geometry, null, 2))
  console.log('Composer menu browser passed: desktop uses available height; short/zoomed screens fit and every final action remains reachable.')
} catch (error) {
  await page?.screenshot({ path: path.join(output, 'composer-menu-failure.png') }).catch(() => {})
  throw error
} finally { await browser?.close(); await server.close() }
