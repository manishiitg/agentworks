// Exercise the production Share and shared-agent components in Chromium.
import assert from 'node:assert/strict'
import { mkdir } from 'node:fs/promises'
import path from 'node:path'
import os from 'node:os'
import { createServer } from 'vite'
import { chromium } from 'playwright'

const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || path.join(os.tmpdir(), 'crew-share-agents')
const server = await createServer({ server: { host: '127.0.0.1', port: 0 } })
let browser
const agents = [{ chat_id: 'team:chat:coach', name: 'Coach', purpose: 'Review feedback and suggest practical next steps.', shared: true }]
try {
  await mkdir(output, { recursive: true })
  await server.listen()
  browser = await chromium.launch({ headless: true })
  for (const [name, width, theme] of [['desktop', 1100, 'dark'], ['narrow', 375, 'dark'], ['light', 1100, 'light']]) {
    const page = await browser.newPage({ viewport: { width, height: 780 }, deviceScaleFactor: 2 })
    const errors = []
    const sent = []
    const saved = []
    let readCount = 0
    page.on('pageerror', error => errors.push(error.message))
    await page.route('**/api/crew-agents', async route => {
      const args = route.request().postDataJSON()
      let body
      if (args.action === 'chats') body = { chats: agents }
      else if (args.action === 'ask') { sent.push(args); body = { status: 'accepted' } }
      else if (args.action === 'read_chat') {
        if (args.after) { readCount++; await new Promise(resolve => setTimeout(resolve, 250)); body = { messages: [], next_after: 2, working: false } }
        else body = { messages: [{ seq: 1, role: 'user', sender: 'Alex Taylor', text: 'Please review this feedback.' }, { seq: 2, role: 'assistant', text: 'Here are two practical next steps.' }], next_after: 2, working: false }
      }
      await route.fulfill({ json: body })
    })
    await page.route('**/api/dashboards', async route => {
      const args = route.request().postDataJSON()
      if (args.action === 'share') saved.push(args)
      await route.fulfill({ json: { mode: args.mode || 'people', can_share: true, people: [{ id: 'colleague', email: 'colleague@example.com' }], agents: [{ ...agents[0], selected: true, can_edit_purpose: true }] } })
    })
    const fixture = server.resolvedUrls.local[0] + 'scripts/test-fixtures/crew-share-agents-qa.html?theme=' + theme + (name === 'light' ? '&standalone=true' : '')
    await page.goto(fixture)
    await page.getByRole('button', { name: /^Agents/ }).click()
    await page.getByRole('button', { name: /Coach Review feedback/ }).click()
    await page.getByRole('log').getByText('Alex Taylor', { exact: true }).waitFor()
    assert.ok(await page.getByRole('log').getByText('Here are two practical next steps.').isVisible())
    await page.getByRole('textbox', { name: 'Message Coach' }).fill('Help us prepare the next review.')
    await page.getByRole('button', { name: 'Send message', exact: true }).click()
    await page.waitForFunction(() => document.querySelector('textarea')?.value === '')
    assert.equal(sent.length, 1)
    assert.equal(sent[0].chat_id, 'team:chat:coach')
    if (name === 'light') assert.equal(sent[0].workspace, 'Crew/example-team')
    else assert.equal(sent[0].workspace_id, 'work:example-team')
    assert.ok(sent[0].submission_id)
    assert.equal(await page.getByRole('button', { name: /Stop|Reset|Change model/ }).count(), 0)
    await page.screenshot({ path: path.join(output, 'crew-shared-agent-' + name + '.png'), fullPage: true })
    assert.ok(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), name + ': no page overflow')
    await page.getByRole('button', { name: 'Back to shared agents' }).click()
    const stoppedAt = readCount
    await page.waitForTimeout(400)
    assert.equal(readCount, stoppedAt, 'reading stops when the conversation closes')
    await page.goto(fixture + '&owner=true')
    await page.getByRole('region', { name: 'Shared agents' }).getByText('Coach', { exact: true }).waitFor()
    assert.ok(await page.getByText(/messages from sharing onward/).isVisible())
    assert.equal(await page.getByRole('textbox').count(), 0)
    await page.getByRole('button', { name: 'Change access in chat' }).click()
    await page.waitForFunction(() => document.querySelector('[data-testid="chat-request"]').textContent.includes('access'))
    assert.equal(saved.length, 0, 'overview sends no direct share writes')
    await page.screenshot({ path: path.join(output, 'crew-share-setting-' + name + '.png'), fullPage: true })
    assert.deepEqual(errors, [], name + ': no component errors')
    await page.close()
  }
  console.log('Crew sharing Chromium passed: same Share audience and selected agent, attributed conversation, send, controls, stopped background reads, dark/light and narrow layouts.')
} finally {
  await browser?.close()
  await server.close()
}
