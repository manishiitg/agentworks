// The production Share popup must fit at normal zoom without losing its actions.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'share-popup')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser
try {
  await mkdir(output, { recursive: true })
  await server.listen()
  browser = await chromium.launch({ headless: true })
  for (const [name, width, height] of [['desktop', 1280, 650], ['phone', 375, 650], ['short', 1280, 400]]) {
    const page = await browser.newPage({ viewport: { width, height } })
    const errors = []
    const saved = []
    page.on('pageerror', error => errors.push(error.message))
    await page.route('**/api/dashboards', async route => {
      const args = route.request().postDataJSON()
      if (args.action === 'share') saved.push(args)
      await route.fulfill({ json: args.action === 'owners'
        ? { owner: { id: 'owner', email: 'owner@example.com' }, co_owners: [{ id: 'second', email: 'colleague@example.com' }], can_manage: true }
        : { mode: 'people', can_share: true, people: [{ id: 'colleague', email: 'colleague@example.com' }], accounts: [{ kind: 'Model', name: 'Shared account', use: 'This project uses its selected account.' }], agents: Array.from({ length: 5 }, (_, i) => ({ chat_id: 'team:chat:agent' + i, name: 'Agent ' + (i + 1), purpose: 'Help with the team review.', selected: i === 0, can_edit_purpose: true })) } })
    })
    await page.goto(server.resolvedUrls.local[0] + 'scripts/test-fixtures/crew-share-agents-qa.html?popup=true')
    const trigger = page.getByRole('button', { name: 'Share', exact: true })
    await trigger.click()
    const dialog = page.getByRole('dialog', { name: 'Share', exact: true })
    await dialog.locator('[aria-label="Current owners"]').getByText('colleague@example.com', { exact: true }).waitFor()
    const bounds = await dialog.boundingBox()
    assert.ok(bounds && bounds.x >= 0 && bounds.y >= 0 && bounds.x + bounds.width <= width && bounds.y + bounds.height <= height, name + ': popup stays within viewport ' + JSON.stringify(bounds))
    const save = dialog.getByRole('button', { name: 'Save', exact: true })
    const actionsReachable = async () => {
      const box = await save.boundingBox()
      assert.ok(box && box.y >= 0 && box.y + box.height <= height, name + ': Save stays visible')
      assert.ok(await save.evaluate(button => { const box = button.getBoundingClientRect(); return button.contains(document.elementFromPoint(box.x + box.width / 2, box.y + box.height / 2)) }), name + ': Save receives clicks')
    }
    await actionsReachable()
    await dialog.getByRole('textbox', { name: 'Add an owner by email' }).scrollIntoViewIfNeeded()
    assert.ok(await dialog.getByRole('textbox', { name: 'Add an owner by email' }).isVisible())
    await actionsReachable()
    await page.screenshot({ path: path.join(output, 'share-popup-' + name + '.png') })
    await dialog.locator('[data-share-dialog-body]').evaluate(node => { node.scrollTop = 0 })
    await actionsReachable()
    await save.click()
    await page.waitForTimeout(100)
    assert.equal(saved.length, 1, name + ': Save sends once')
    assert.equal(saved[0].mode, 'people')
    assert.deepEqual(saved[0].shared_agents, ['team:chat:agent0'])
    await page.keyboard.press('Escape')
    assert.equal(await dialog.count(), 0, name + ': Escape closes')
    assert.equal(await trigger.evaluate(node => node === document.activeElement), true, name + ': focus returns to Share')
    assert.deepEqual(errors, [], name + ': no component errors')
    console.log('Share popup passed at ' + width + 'x' + height + ': ' + JSON.stringify(bounds) + '; Owners reachable, Save/Close fixed, one Save request.')
    await page.close()
  }
} finally {
  await browser?.close()
  await server.close()
}
