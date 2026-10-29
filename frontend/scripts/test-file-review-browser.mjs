import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'file-review-browser')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser, page
try {
  await mkdir(output, { recursive: true }); await server.listen()
  browser = await chromium.launch({ headless: true })
  page = await browser.newPage(); page.setDefaultTimeout(20000)
  const errors = []; page.on('pageerror', error => errors.push(error.message))
  const measurements = []
  for (const [name, width, height] of [['desktop', 1280, 720], ['phone', 390, 800]]) {
    await page.setViewportSize({ width, height })
    await page.goto(server.resolvedUrls.local[0] + 'scripts/test-fixtures/file-review-qa.html?count=50')
    const files = page.locator('[data-review-file]')
    await files.nth(49).getByText('new file-49.txt', { exact: true }).waitFor()
    assert.equal(await files.count(), 50)
    const metrics = await files.evaluateAll(elements => elements.map(element => ({
      top: element.offsetTop, height: element.offsetHeight,
      nestedScroll: [...element.querySelectorAll('*')].some(child => /auto|scroll/.test(getComputedStyle(child).overflowY) && child.scrollHeight > child.clientHeight + 1),
    })))
    assert.ok(metrics.every((item, index) => item.height > 50 && (!index || item.top >= metrics[index - 1].top + metrics[index - 1].height)), `${name}: file sections flow sequentially`)
    assert.ok(metrics.every(item => !item.nestedScroll), `${name}: diffs do not require separate vertical scrolling`)
    await page.getByRole('button', { name: 'file-49.txt', exact: false }).click()
    assert.ok(await files.nth(49).evaluate(element => { const rect = element.getBoundingClientRect(); return rect.top >= 0 && rect.top < innerHeight }), `${name}: list jumps to the final diff`)
    const pageScroll = await page.evaluate(() => document.documentElement.scrollHeight <= innerHeight + 1)
    assert.ok(pageScroll, `${name}: scrolling stays inside the review pane`)
    measurements.push({ name, files: metrics.length, last: metrics.at(-1) })
    await page.screenshot({ path: path.join(output, `file-review-${name}.png`) })
  }
  await page.goto(server.resolvedUrls.local[0] + 'scripts/test-fixtures/file-review-qa.html?count=51')
  await page.locator('[data-review-file="file-0.txt"]').waitFor()
  assert.equal(await page.locator('[data-review-file]').count(), 1)
  await page.getByRole('button', { name: 'file-50.txt', exact: false }).click()
  await page.locator('[data-review-file="file-50.txt"]').waitFor()
  assert.equal(await page.locator('[data-review-file]').count(), 1)
  await page.goto(server.resolvedUrls.local[0] + 'scripts/test-fixtures/file-review-qa.html?count=2&git=1')
  await page.getByRole('button', { name: 'Uncommitted', exact: true }).click()
  await page.locator('[data-review-file="app/file-1.txt"]').getByText('new file-1.txt', { exact: true }).waitFor()
  assert.equal(await page.locator('[data-review-file]').count(), 2)
  await page.goto(server.resolvedUrls.local[0] + 'scripts/test-fixtures/file-review-qa.html?count=2&worktree=1')
  await page.locator('[data-review-file="file-1.txt"]').waitFor()
  await page.getByRole('button', { name: 'Branch / PR', exact: true }).click()
  const worktree = page.getByRole('combobox', { name: 'Worktree', exact: true })
  assert.equal(await worktree.inputValue(), 'app-wt')
  await page.getByText('feature → main', { exact: true }).waitFor()
  await worktree.selectOption('app')
  await page.getByRole('button', { name: 'This chat', exact: true }).click()
  await page.getByRole('button', { name: 'Branch / PR', exact: true }).click()
  assert.equal(await worktree.inputValue(), 'app')
  await page.screenshot({ path: path.join(output, 'chat-worktree-default.png') })
  assert.deepEqual(errors, [])
  await writeFile(path.join(output, 'file-review-geometry.json'), JSON.stringify(measurements, null, 2))
  console.log('File review browser passed: 50 sequential diffs on desktop/mobile, file jumps, 51-file picker and Git diff stack.')
} catch (error) {
  await page?.screenshot({ path: path.join(output, 'file-review-failure.png') }).catch(() => {})
  throw error
} finally { await browser?.close(); await server.close() }
