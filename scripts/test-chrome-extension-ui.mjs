import { chromium } from '../packages/playwright/node_modules/playwright/index.mjs';
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
const browser = await chromium.launch({ executablePath: process.env.CHROME_EXTENSION_E2E_CHROME || chromium.executablePath(), headless: true });
let selected = false;
try {
  const page = await browser.newPage();
  await page.route('**/api/browser/extension*', async route => {
    const request = route.request();
    const action = request.method() === 'POST' ? request.postDataJSON().action : '';
    if (action === 'disconnect') selected = false;
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(action === 'pair' ? { token: 'test-pairing-credential-that-is-not-a-real-secret' } : { selected, connected: selected, tabs: selected ? 2 : 0, workspace: 'Workflow/CRM' }) });
  });
  await page.goto(`${process.env.CHROME_EXTENSION_UI_URL || 'http://127.0.0.1:5417'}/scripts/test-fixtures/chrome-extension-qa.html`);
  await page.getByRole('button', { name: 'Connect Chrome', exact: true }).click();
  const pairing = await page.getByRole('textbox', { name: 'Chrome pairing connection' }).inputValue();
  assert.equal(JSON.parse(pairing).token, 'test-pairing-credential-that-is-not-a-real-secret');
  assert.match(JSON.parse(pairing).url, /\/api\/browser\/extension\/connect$/);
  const evidence = process.env.CHROME_EXTENSION_UI_EVIDENCE || '/tmp/agentworks-chrome-extension-qa';
  await mkdir(evidence, { recursive: true });
  for (const width of [1000, 420]) {
    await page.setViewportSize({ width, height: 720 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `overflow at ${width}`);
    await page.screenshot({ path: `${evidence}/pair-${width}.png`, fullPage: true });
  }
  selected = true;
  await page.getByRole('button', { name: 'Use workspace browser' }).waitFor({ timeout: 10000 });
  await page.screenshot({ path: `${evidence}/connected-420.png`, fullPage: true });
  assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, 'connected controls overflow');
  await page.getByRole('button', { name: 'Use workspace browser' }).click();
  await page.getByText('Use your signed-in Chrome').waitFor();
  console.log('PASS browser-rendered pairing, download link, responsive connection controls and explicit disconnect');
} finally { await browser.close(); }
