// Real shared panel/styles in Chromium; run on GitHub, without a backend or account.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'mcp-offers-browser')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser
try {
  await mkdir(output, { recursive: true })
  await server.listen()
  browser = await chromium.launch({ headless: true })
  for (const [name, width, theme] of [['desktop', 1100, 'light'], ['mobile', 375, 'light'], ['dark', 1100, 'dark']]) {
    const page = await browser.newPage({ viewport: { width, height: 850 } })
    const errors = []
    page.on('pageerror', error => errors.push(error.message))
    await page.clock.install({ time: new Date('2026-10-11T12:00:00Z') })
    // An unavailable public feed must leave the bundled, dated evidence usable.
    await page.route('https://raw.githubusercontent.com/**', route => route.abort())
    await page.goto(server.resolvedUrls.local[0] + `scripts/test-fixtures/mcp-offers-qa.html?theme=${theme}`)
    await page.getByRole('tab', { name: /Available MCPs/ }).click()
    const exa = page.getByRole('button', { name: 'Exa Free tier details', exact: true })
    await exa.click()
    const dialog = page.getByRole('dialog', { name: 'Exa offer details' })
    await dialog.waitFor()
    assert.match(await dialog.textContent(), /No payment method required/)
    const bounds = await dialog.boundingBox()
    assert.ok(bounds.x >= 0 && bounds.y >= 0 && bounds.x + bounds.width <= width && bounds.y + bounds.height <= 850, `${name}: popover stays inside viewport`)
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= window.innerWidth), true, `${name}: no horizontal overflow`)
    await page.screenshot({ path: path.join(output, `mcp-offers-${name}.png`), fullPage: true })
    await page.keyboard.press('Escape')
    assert.equal(await dialog.count(), 0)
    assert.equal(await exa.evaluate(element => element === document.activeElement), true)
    await page.getByLabel('Filter by offer').selectOption('recurring')
    assert.equal(await page.getByRole('button', { name: 'Unstructured Trial credits details' }).count(), 0)
    assert.equal(await page.getByRole('button', { name: 'Browserbase Needs review details' }).count(), 0)
    await page.getByRole('button', { name: 'Add Exa connection', exact: true }).click()
    assert.equal(await page.getByLabel('Connection setup opened').textContent(), '1')
    await page.getByLabel('Filter by offer').selectOption('review')
    await page.getByRole('button', { name: 'Browserbase Needs review details' }).click()
    assert.match(await page.getByRole('dialog').textContent(), /This offer needs review/)
    await page.getByRole('button', { name: 'Close offer details' }).click()
    await page.getByRole('tab', { name: /Connected MCPs/ }).click()
    assert.equal(await page.getByLabel('Filter by offer').count(), 0)
    assert.equal(await page.getByRole('link', { name: 'Browse all free tools' }).count(), 0)
    assert.deepEqual(errors, [], `${name}: no component errors`)
    await page.close()
  }
  console.log('MCP offers Chromium checks passed: desktop, mobile and dark mode; screenshots saved.')
} finally {
  await browser?.close()
  await server.close()
}
