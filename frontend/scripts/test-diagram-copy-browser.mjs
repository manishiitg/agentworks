// Exercise real DOM -> PNG rendering; only clipboard delivery is intercepted.
import assert from 'node:assert/strict'
import { mkdir, writeFile } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'diagram-copy-browser')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser
try {
  await mkdir(output, { recursive: true })
  await server.listen()
  browser = await chromium.launch({ headless: true })
  const page = await browser.newPage({ acceptDownloads: true })
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.addInitScript(() => {
    Object.defineProperty(navigator, 'clipboard', { configurable: true, value: { write: async items => {
      if (window.failClipboard) throw new Error('Clipboard denied')
      window.clipboardGesture = navigator.userActivation.isActive
      window.copiedPng = await items[0].getType('image/png')
    } } })
  })
  const results = []
  for (const [theme, width] of [['dark', 1000], ['light', 360]]) {
    await page.setViewportSize({ width, height: 900 })
    await page.goto(server.resolvedUrls.local[0] + `scripts/test-fixtures/diagram-copy-qa.html?${theme}`)
    const copy = page.getByRole('button', { name: 'Copy diagram as image', exact: true })
    await copy.click()
    await page.getByRole('status').filter({ hasText: 'Copied image. Paste it into Slack.' }).waitFor()
    const result = await page.evaluate(async () => {
      const blob = window.copiedPng
      const image = await createImageBitmap(blob)
      const canvas = document.createElement('canvas')
      canvas.width = image.width; canvas.height = image.height
      const ctx = canvas.getContext('2d')
      ctx.drawImage(image, 0, 0)
      const viewport = document.querySelector('[data-diagram-viewport]')
      const figure = viewport.closest('figure')
      const body = viewport.firstElementChild
      const last = document.querySelector('[data-diagram-node="step-7"]')
      const scale = image.height / (body.offsetHeight + figure.querySelector('figcaption').offsetHeight + 2)
      const x = (last.getBoundingClientRect().left - figure.getBoundingClientRect().left + last.offsetWidth / 2) * scale
      const y = (last.getBoundingClientRect().top - figure.getBoundingClientRect().top + last.offsetHeight / 2) * scale
      const card = Array.from(ctx.getImageData(x, y, 1, 1).data)
      const background = Array.from(ctx.getImageData(8, image.height - 8, 1, 1).data)
      const pixels = ctx.getImageData(0, 0, image.width, image.height).data
      const colors = new Set()
      for (let i = 0; i < pixels.length; i += 256) colors.add(`${pixels[i]},${pixels[i + 1]},${pixels[i + 2]}`)
      const data = await new Promise(resolve => { const reader = new FileReader(); reader.onload = () => resolve(reader.result); reader.readAsDataURL(blob) })
      return { type: blob.type, width: image.width, height: image.height, bodyHeight: body.offsetHeight, viewportHeight: viewport.clientHeight, card, background, colors: colors.size, gesture: window.clipboardGesture, data }
    })
    assert.equal(result.type, 'image/png')
    assert.equal(result.gesture, true, 'clipboard write starts in the click gesture')
    assert.ok(result.bodyHeight > result.viewportHeight, 'fixture really scrolls')
    assert.ok(result.height > result.bodyHeight, 'PNG includes the full body and title')
    assert.notDeepEqual(result.card, result.background, 'the offscreen final node is rendered')
    assert.equal(result.card[3], 255, 'export has an opaque theme background')
    assert.ok(result.colors > 12, 'PNG includes styled text, boxes and connections')
    await writeFile(path.join(output, `diagram-copy-${theme}.png`), Buffer.from(result.data.split(',')[1], 'base64'))
    delete result.data
    results.push({ theme, ...result })
  }
  await page.evaluate(() => { window.failClipboard = true })
  await page.getByRole('button', { name: 'Copy diagram as image', exact: true }).click()
  const fallback = page.getByRole('button', { name: 'Download diagram image', exact: true })
  await fallback.waitFor()
  const [download] = await Promise.all([page.waitForEvent('download'), fallback.click()])
  assert.ok(download.suggestedFilename().endsWith('.png'))
  await download.saveAs(path.join(output, 'diagram-download-fallback.png'))
  assert.deepEqual(errors, [])
  await writeFile(path.join(output, 'diagram-copy-results.json'), JSON.stringify(results, null, 2))
  console.log('Diagram PNG: dark/light, desktop/phone, full offscreen content, clipboard gesture and download fallback passed.')
} finally {
  await browser?.close()
  await server.close()
}
