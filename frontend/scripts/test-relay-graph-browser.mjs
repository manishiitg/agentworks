// Exercise the real shared canvas in Chromium on GitHub, without a platform run.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'relay-graph-browser')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser
try {
  await mkdir(output, { recursive: true })
  await server.listen()
  browser = await chromium.launch({ headless: true })
  for (const theme of ['light', 'dark']) {
    const page = await browser.newPage({ viewport: { width: 1100, height: 850 } })
    const errors = []
    page.on('pageerror', error => errors.push(error.message))
    await page.goto(server.resolvedUrls.local[0] + `scripts/test-fixtures/relay-graph-qa.html?theme=${theme}`)
    const classifier = page.getByRole('group', { name: 'Classify request', exact: true })
    await classifier.waitFor()
    await page.waitForFunction(() => {
      const scale = document.querySelector('.react-flow__viewport')?.style.transform.match(/scale\(([\d.]+)/)
      return scale && Number(scale[1]) < 0.6 && Number(scale[1]) > 0.1
    })
    const small = await classifier.boundingBox()
    await page.getByRole('button', { name: 'Resize panel', exact: true }).click()
    await page.waitForFunction(width => document.querySelector('[aria-label="Classify request"]').getBoundingClientRect().width > width * 1.3, small.width)
    assert.equal(await page.getByRole('group', { name: 'Category is billing?', exact: true }).count(), 1)
    await page.screenshot({ path: path.join(output, `relay-overview-${theme}.png`), fullPage: true })
    await page.getByRole('button', { name: 'Zoom In', exact: true }).click()
    // Let the control's animated viewport settle before changing the panel.
    await page.waitForTimeout(300)
    const manualViewport = await page.locator('.react-flow__viewport').getAttribute('style')
    await page.getByRole('button', { name: 'Resize panel', exact: true }).click()
    await page.waitForTimeout(300)
    assert.equal(await page.locator('.react-flow__viewport').getAttribute('style'), manualViewport, 'resize preserves a manually adjusted viewport')
    await page.getByRole('button', { name: 'Resize panel', exact: true }).click()
    await page.getByRole('button', { name: 'Fit View', exact: true }).click()
    await classifier.click()
    await page.getByText('Categories', { exact: true }).click()
    assert.match(await page.getByRole('region', { name: 'Relay node details' }).textContent(), /Payments and refunds/)
    await page.getByRole('button', { name: 'Close node details' }).click()
    await page.getByRole('button', { name: 'completed', exact: true }).click()
    const graph = page.getByLabel('Relay graph', { exact: true })
    await page.getByRole('group', { name: 'Billing agent', exact: true }).waitFor()
    assert.equal(await graph.getByRole('group', { name: 'Support agent', exact: true }).count(), 0)
    assert.equal(await graph.getByLabel('Step 1', { exact: true }).count(), 1)
    assert.equal(await graph.getByLabel('Step 2', { exact: true }).count(), 1)
    assert.equal(await graph.getByRole('group', { name: 'Final result', exact: true }).count(), 1)
    assert.match(await graph.textContent(), /Chosen category: billing/)
    await page.screenshot({ path: path.join(output, `relay-execution-${theme}.png`), fullPage: true })
    await page.getByRole('button', { name: 'failed', exact: true }).click()
    await page.getByRole('group', { name: 'Run stopped', exact: true }).waitFor()
    assert.equal(await graph.getByRole('group', { name: 'Final result', exact: true }).count(), 0)
    await page.screenshot({ path: path.join(output, `relay-failed-${theme}.png`), fullPage: true })
    assert.deepEqual(errors, [], `${theme}: no graph errors`)
    await page.close()
  }
  console.log('Relay canvas checks passed: resize fit, category details, actual execution order and failed result; light/dark screenshots saved.')
} finally {
  await browser?.close()
  await server.close()
}
