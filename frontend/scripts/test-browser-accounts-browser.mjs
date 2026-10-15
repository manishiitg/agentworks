import assert from 'node:assert/strict'
import { chromium } from 'playwright'
import { spawn } from 'node:child_process'
import { mkdtemp, mkdir, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'

const script = fileURLToPath(import.meta.url)
const repo = path.resolve(path.dirname(script), '../..')
if (!process.env.BROWSER_ACCOUNT_FIXTURE_URL) {
  const child = spawn('go', ['test', './pkg/browserrelay', '-run', '^TestAccountBrowsersChromium$', '-count=1', '-v', '-timeout=180s'], {
    cwd: path.join(repo, 'agent_go'), stdio: 'inherit',
    env: { ...process.env, GOWORK: 'off', BROWSER_ACCOUNT_NODE: process.execPath, BROWSER_ACCOUNT_CHECK_SCRIPT: script, BROWSER_ACCOUNT_CHROME: chromium.executablePath() },
  })
  process.exitCode = await new Promise(resolve => child.on('exit', code => resolve(code ?? 1)))
} else {
  const base = process.env.BROWSER_ACCOUNT_FIXTURE_URL
  const pairing = JSON.stringify({ url: base.replace('http:', 'ws:') + '/api/browser/extension/connect', token: process.env.BROWSER_ACCOUNT_FIXTURE_TOKEN })
  const root = await mkdtemp(path.join(tmpdir(), 'browser-accounts-'))
  const extension = path.join(repo, 'extensions/agentworks-chrome')
  const contexts = new Set()
  const state = async () => (await fetch(base + '/fixture/state')).json()
  const until = async condition => {
    for (let i = 0; i < 200; i++) { const value = await condition(); if (value) return value; await new Promise(resolve => setTimeout(resolve, 100)) }
    throw new Error('Browser connection state did not converge')
  }
  const launch = async name => {
    const context = await chromium.launchPersistentContext(path.join(root, name), {
      executablePath: process.env.BROWSER_ACCOUNT_CHROME, headless: true,
      args: [`--disable-extensions-except=${extension}`, `--load-extension=${extension}`],
    })
    contexts.add(context)
    let [worker] = context.serviceWorkers(); if (!worker) worker = await context.waitForEvent('serviceworker')
    const id = worker.url().split('/')[2]
    const popup = await context.newPage(); await popup.goto(`chrome-extension://${id}/popup.html`)
    return { context, popup, message: value => popup.evaluate(value => chrome.runtime.sendMessage(value), value) }
  }
  try {
    let first = await launch('first')
    const second = await launch('second')
    for (const peer of [first, second]) {
      const reply = await peer.message({ action: 'connect', pairing })
      assert.equal(reply.ok, true, reply.error)
      assert.equal(reply.accountConnected, true)
      assert.equal(reply.selectedScope, '', 'account connection must not need a project')
      await peer.popup.getByText('Connected', { exact: true }).waitFor()
    }
    const browsers = await until(async () => { const s = await state(); return s.browsers.filter(b => b.connected).length === 2 && s.browsers })
    const firstID = await first.popup.evaluate(async () => (await chrome.storage.local.get('browserInstallationID')).browserInstallationID)
    const secondID = await second.popup.evaluate(async () => (await chrome.storage.local.get('browserInstallationID')).browserInstallationID)
    assert.notEqual(firstID, secondID)
    assert.equal(browsers.some(b => b.id === firstID), true)
    for (const [scope, id] of [['alpha', firstID], ['beta', secondID]]) {
      const response = await fetch(base + '/fixture/select', { method: 'POST', body: JSON.stringify({ scope, id }) })
      assert.equal(response.status, 200, await response.text())
    }
    await until(async () => { const s = await state(); return s.alpha.connected && s.beta.connected })
    let s = await state()
    assert.equal(s.alpha.browser_id, firstID)
    assert.equal(s.beta.browser_id, secondID)
    assert.equal(s.alpha.tabs, 0, 'general pairing must not adopt the current tab')
    assert.equal(s.beta.tabs, 0)
    await fetch(base + '/fixture/restart', { method: 'POST' })
    await until(async () => { const s = await state(); return s.alpha.connected && s.beta.connected && s.browsers.filter(b => b.connected).length === 2 })
    await first.context.close(); contexts.delete(first.context)
    await until(async () => !(await state()).alpha.connected)
    assert.equal((await state()).beta.connected, true, 'other browser survives full browser shutdown')
    first = await launch('first')
    await until(async () => (await state()).alpha.connected)
    assert.equal((await first.message({ action: 'state' })).accountConnected, true, 'startup reconnect needs no project or new code')
    assert.equal(await first.popup.evaluate(async () => (await chrome.storage.local.get('browserInstallationID')).browserInstallationID), firstID)
    await fetch(base + '/fixture/disconnect?id=' + firstID, { method: 'POST' })
    await until(async () => !(await state()).alpha.connected)
    s = await state()
    assert.equal(s.alpha.browser_id, firstID, 'offline chosen browser is retained')
    assert.equal(s.beta.connected, true)
    assert.match(s.alpha.message, /offline/)
    const output = process.env.BROWSER_CHECK_ARTIFACT_DIR
    if (output) { await mkdir(output, { recursive: true }); await second.popup.screenshot({ path: path.join(output, 'browser-account-connected.png') }) }
    console.log('PASS: two real Chromium profiles connect without projects; independent routing, server restart, browser startup, stable installation IDs and individual disconnect.')
  } finally {
    for (const context of contexts) await context.close()
    await rm(root, { recursive: true, force: true })
  }
}
