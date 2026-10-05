import { chromium } from '../packages/playwright/node_modules/playwright/index.mjs';
import { execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { mkdtemp, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import assert from 'node:assert/strict';
import { fileURLToPath } from 'node:url';
const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const base = process.env.CHROME_EXTENSION_E2E_URL;
const profile = await mkdtemp(path.join(tmpdir(), 'agentworks-extension-e2e-'));
const executablePath = process.env.CHROME_EXTENSION_E2E_CHROME || chromium.executablePath();
const extension = path.join(root, 'extensions/agentworks-chrome');
const run = promisify(execFile);
let browser, connection;
async function cli(command, ...args) {
  const response = await fetch(`${base}/fixture/tool`, { method: 'POST', headers: { 'Content-Type': 'application/json' }, body: JSON.stringify({ command, args }) });
  const stdout = await response.text(); assert.equal(response.status, 200, stdout);
  const data = JSON.parse(stdout);
  assert.equal(data.success, true, stdout); return data.data;
}
try {
  browser = await chromium.launchPersistentContext(profile, { executablePath, headless: false, args: [`--disable-extensions-except=${extension}`, `--load-extension=${extension}`] });
  console.log(`Chrome ${browser.browser().version()}`);
  let [worker] = browser.serviceWorkers(); if (!worker) worker = await browser.waitForEvent('serviceworker');
  const id = worker.url().split('/')[2];
  const page = await browser.newPage(); await page.goto(`${base}/fixture`);
  const privatePage = await browser.newPage(); await privatePage.goto(`${base}/private`);
  const popup = await browser.newPage(); await popup.goto(`chrome-extension://${id}/popup.html`);
  const message = async payload => popup.evaluate(payload => chrome.runtime.sendMessage(payload), payload);
  const paired = await message({ action: 'connect', pairing: JSON.stringify({ url: base.replace('http', 'ws') + '/api/browser/extension/connect', token: process.env.CHROME_EXTENSION_E2E_TOKEN }) });
  assert.equal(paired.ok, true, JSON.stringify(paired));
  const tabId = await popup.evaluate(async base => (await chrome.tabs.query({})).find(t => t.url === `${base}/fixture`).id, base);
  const shared = await message({ action: 'share', tabId }); assert.equal(shared.ok, true, JSON.stringify(shared));
  const response = await fetch(`${base}/fixture/cdp`); assert.equal(response.status, 200); connection = await response.json();
  const snapshot = await cli('snapshot', '-i'); console.log('PASS shared-page snapshot');
  assert.match(JSON.stringify(snapshot), /Name/); assert.match(JSON.stringify(snapshot), /Save customer/);
  const refs = snapshot.refs || {};
  const name = Object.entries(refs).find(([,value]) => value.name?.trim() === 'Name')?.[0];
  const save = Object.entries(refs).find(([,value]) => value.name === 'Save customer')?.[0];
  assert.ok(name && save, 'snapshot has actionable refs');
  await cli('fill', `@${name}`, 'Ada'); await cli('click', `@${save}`);
  await page.locator('#result').filter({ hasText: 'Saved Ada' }).waitFor(); console.log('PASS real snapshot → fill → click');
  const tabs = await cli('tab'); assert.doesNotMatch(JSON.stringify(tabs), /Unshared private/); console.log('PASS unshared-tab exclusion');
  await cli('screenshot', 'Workflow/extension-e2e/evidence/screenshot.png'); const image = await fetch(`${base}/fixture/image`); assert.equal(image.status, 200); assert.ok((await image.arrayBuffer()).byteLength > 1000); console.log('PASS screenshot through workspace artifact broker');
  await cli('open', `${base}/fixture?navigation=1`); assert.equal(await page.evaluate(() => document.cookie.includes('fixture_login=retained')), true); console.log('PASS navigation and retained login cookie');
  await cli('tab', 'new', `${base}/fixture?new=1`); const after = await cli('tab'); assert.ok(JSON.stringify(after).includes('new=1')); console.log('PASS scoped tab creation');
  const disallowed = await message({ action: 'share', tabId: await popup.evaluate(async () => (await chrome.tabs.query({})).find(t => t.url.startsWith('chrome-extension:')).id) }); assert.equal(disallowed.ok, false); console.log('PASS protected-page refusal');
  const override = await fetch(`${base}/fixture/tool`, { method: 'POST', body: JSON.stringify({ command: 'snapshot', args: ['--cdp', '9222'] }) }); assert.equal(override.status, 409); console.log('PASS backend-owned endpoint');
  const created = browser.pages().find(p => p.url().includes('new=1')); assert.ok(created); await created.close();
  const closed = await fetch(`${base}/fixture/tool`, { method: 'POST', body: JSON.stringify({ command: 'get', args: ['title'] }) }); assert.equal(closed.status, 409); console.log('PASS closed tab fails without switching to another shared tab');
  await message({ action: 'stop' }); const stopped = await fetch(`${base}/fixture/cdp`); assert.equal(stopped.status, 409); console.log('PASS immediate stop');
  const blocked = await fetch(`${base}/fixture/tool`, { method: 'POST', body: JSON.stringify({ command: 'snapshot', args: [] }) }); assert.equal(blocked.status, 409); assert.match(await blocked.text(), /CHROME_EXTENSION_DISCONNECTED/); console.log('PASS tool fails closed after stop with host CDP disabled');
} finally {
  if (connection) await run('agent-browser', ['--session', connection.session, 'close'], { timeout: 10000 }).catch(() => {});
  await browser?.close(); await rm(profile, { recursive: true, force: true });
}
