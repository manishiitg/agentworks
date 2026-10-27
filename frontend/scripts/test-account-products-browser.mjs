import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'account-products-browser')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser, page
try {
  await mkdir(output, { recursive: true }); await server.listen()
  browser = await chromium.launch({ headless: true })
  page = await browser.newPage(); page.setDefaultTimeout(15000)
  const errors = []; page.on('pageerror', error => errors.push(error.message))
  const url = server.resolvedUrls.local[0] + 'scripts/test-fixtures/account-products-qa.html'
  for (const [name, width, height] of [['desktop', 1280, 720], ['short', 853, 480], ['phone', 390, 700]]) {
    await page.setViewportSize({ width, height }); await page.goto(url)
    const trigger = page.getByRole('button', { name: 'Account & settings', exact: true })
    await trigger.click()
    const menu = page.getByRole('menu', { name: 'Account & settings', exact: true })
    const options = menu.locator('[data-product-option]')
    assert.equal(await options.count(), 8)
    const boxes = await options.evaluateAll(items => items.map(item => {
      const r = item.getBoundingClientRect(), icon = item.querySelector('svg').getBoundingClientRect()
      const label = item.lastElementChild
      return { x: r.x, y: r.y, right: r.right, width: r.width, iconX: icon.x, iconWidth: icon.width, overflow: item.scrollWidth > item.clientWidth, truncated: label.scrollWidth > label.clientWidth }
    }))
    assert.equal(boxes[0].y, boxes[2].y, `${name}: three columns`)
    for (const box of boxes) {
      assert.ok(Math.abs(box.iconX + box.iconWidth / 2 - (box.x + box.width / 2)) <= 1, `${name}: centered icon`)
      assert.equal(box.overflow, false, `${name}: tile text fits`)
      assert.equal(box.truncated, false, `${name}: product names remain readable`)
    }
    const bounds = await menu.boundingBox()
    assert.ok(bounds.x >= 0 && bounds.x + bounds.width <= width && bounds.y >= 0 && bounds.y + bounds.height <= height, `${name}: popup stays in viewport`)
    await menu.screenshot({ path: path.join(output, `account-products-${name}.png`) })
    await page.keyboard.press('Escape'); await menu.waitFor({ state: 'hidden' })
    assert.equal(await trigger.evaluate(element => document.activeElement === element), true)
    await trigger.press('ArrowDown')
    await page.getByRole('menuitem', { name: 'Dashboards', exact: true }).click()
    assert.equal(new URL(page.url()).pathname, '/dashboards')
    assert.equal(await page.locator('[data-account-products-fixture]').count(), 1, 'same document stays mounted')
    await trigger.click()
    await page.getByRole('menuitem', { name: 'CLI / MCP', exact: true }).click()
    assert.equal(new URL(page.url()).pathname, '/settings/ai-tools')
    await menu.waitFor({ state: 'hidden' })
  }
  assert.deepEqual(errors, [])
  console.log('Account products passed: eight existing destinations, three centered columns, viewport bounds, Escape/focus and SPA navigation at desktop/short/phone sizes.')
} catch (error) {
  await page?.screenshot({ path: path.join(output, 'account-products-failure.png') }).catch(() => {})
  throw error
} finally { await browser?.close(); await server.close() }
