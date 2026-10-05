// Browser-rendered integration check. HTTP is stubbed; real transport/action
// qualification is scripts/test-chrome-extension-e2e.mjs.
import { chromium } from '../packages/playwright/node_modules/playwright/index.mjs';
import assert from 'node:assert/strict';
import { mkdir } from 'node:fs/promises';
const browser = await chromium.launch({ executablePath: process.env.CHROME_EXTENSION_E2E_CHROME || chromium.executablePath(), headless: true });
let selected = false, connected = false, tabs = [], liveSessions = [], connectionID = '', copyCount = 0, disconnectCount = 0, calls = 0;
const token = 'test-stable-connection-code-that-is-not-a-real-secret';
let currentToken = token;
const fixture = `${process.env.CHROME_EXTENSION_UI_URL || 'http://127.0.0.1:5419'}/scripts/test-fixtures/chrome-extension-qa.html`;
const evidence = process.env.CHROME_EXTENSION_UI_EVIDENCE || '/tmp/agentworks-chrome-extension-ui';
try {
  await mkdir(evidence, { recursive: true });
  const context = await browser.newContext({ permissions: ['clipboard-read', 'clipboard-write'] });
  const page = await context.newPage();
  page.on("pageerror", e => console.error("UI page error:", e.message));
  await page.route(url => url.pathname.startsWith('/api/'), async route => {
    if (route.request().url().includes('/api/browser/live/sessions')) return route.fulfill({contentType:'application/json',body:JSON.stringify({sessions:liveSessions})});
    if (!route.request().url().includes('/api/browser/extension')) return route.fulfill({ contentType: 'application/json', body: '{"sessions":[],"success":true}' });
    calls++;
    const action = route.request().method() === 'POST' ? route.request().postDataJSON().action : '';
    if (action === 'disconnect') { selected = connected = false; tabs = []; disconnectCount++; }
    if (action === 'pair') copyCount++;
    if (action === 'reset') { currentToken = token + '-rotated'; connected = false; tabs = []; }
    await route.fulfill({ contentType: 'application/json', body: JSON.stringify(['pair','reset'].includes(action) ? { token:currentToken, scope:'qa-project-scope' } : { selected, connected, tabs: tabs.length, workspace: 'customer-portal', connection_id: connectionID, tab_titles: tabs }) });
  });
  const openSettings = async () => page.getByRole('button', { name: 'Browser settings', exact: true }).click();
  await page.goto(fixture);
  await page.getByRole('heading', {name:'Choose a browser',exact:true}).waitFor({timeout:10000});
  await page.screenshot({path:`${evidence}/empty-browser.png`,fullPage:true});
  liveSessions = [{browser_session:'qa-running', workflow_session:'fixture', label:'QA existing browser'}];
  await page.getByText('QA existing browser', {exact:false}).first().waitFor({timeout:10000});
  assert.equal(await page.getByRole('heading', {name:'Choose a browser',exact:true}).count(), 0, 'existing browser discovery hides empty choices');
  liveSessions = [];
  await page.getByRole('heading', {name:'Choose a browser',exact:true}).waitFor({timeout:10000});
  await page.getByRole('button', {name:/My Chrome or Edge Use your signed-in/}).click();
  assert.equal(await page.getByRole('dialog', {name:'Browser settings'}).isVisible(), true, 'empty pane browser option opens its existing setup');
  await page.getByRole('button', {name:'Close browser settings'}).click();
  await openSettings();
  const choice = page.getByRole('combobox', { name: 'Browser choice' });
  assert.equal(await page.locator('option[value="auto"]').count(), 0, 'Code offers explicit browser choices');
  await choice.selectOption('extension');
  await page.getByRole('button', { name: 'Copy connection', exact: true }).click();
  assert.equal(JSON.parse(await page.evaluate(() => navigator.clipboard.readText())).token, token);
  assert.equal(JSON.parse(await page.evaluate(() => navigator.clipboard.readText())).brand, 'AgentWorks');
  assert.equal(await page.getByRole('textbox', { name: 'Browser connection code' }).isVisible(), false, 'raw code hidden by default');
  await page.getByRole('button', { name: 'Copied connection', exact: true }).click();
  assert.equal(copyCount, 2);
  assert.equal(JSON.parse(await page.evaluate(() => navigator.clipboard.readText())).token, token);
  for (const width of [1000, 420]) {
    await page.setViewportSize({ width, height: 820 });
    assert.equal(await page.evaluate(() => document.documentElement.scrollWidth <= innerWidth), true, `overflow at ${width}`);
    await page.screenshot({ path: `${evidence}/setup-${width}.png`, fullPage: true });
  }
  selected = connected = true; connectionID = 'new-connection-one';
  await page.getByText('Ready for your agent', { exact: true }).waitFor({ timeout: 10000 });
  assert.equal(await page.getByRole('button', { name: 'Start browser', exact: true }).count(), 0, 'extension tab does not expose workspace launch');
  assert.equal(await page.getByRole('button', { name: 'Copy connection', exact: true }).count(), 0, 'setup disappears after connection');
  await page.getByRole('button', { name: 'Reconnect', exact: true }).click();
  await page.getByRole('button', { name: 'Copy connection', exact: true }).click();
  await page.getByText('Waiting for your browser…', { exact: true }).waitFor();
  await page.waitForTimeout(3000);
  assert.equal(await page.getByText('Waiting for your browser…', { exact: true }).isVisible(), true, 'old connection must not complete new setup');
  connectionID = 'new-connection-two'; tabs = ['Customer portal', 'Project dashboard'];
  await page.getByText('Your browser is ready', { exact: true }).waitFor({ timeout: 10000 });
  await page.getByRole('button', { name: 'Close browser settings' }).click();
  assert.equal(await page.getByRole('heading', {name:'Choose a browser',exact:true}).count(), 0, 'connected browser keeps choices in the header');
  const closeGuide = page.getByRole('button', {name:'Close panel walkthrough',exact:true});
  if (await closeGuide.isVisible()) await closeGuide.click();
  await page.setViewportSize({ width: 1000, height: 720 });
  await page.screenshot({ path: `${evidence}/connected-1000.png`, fullPage: true });
  await openSettings();
  await page.getByText('Connection code options', {exact:true}).click();
  await page.getByRole('button', {name:'Reset connection code',exact:true}).click();
  await page.getByRole('button', {name:'Reset all connections',exact:true}).click();
  await page.getByText('Browser disconnected', {exact:true}).waitFor();
  assert.equal(JSON.parse(await page.evaluate(() => navigator.clipboard.readText())).token, token + '-rotated');
  await choice.selectOption('headless');
  assert.equal(disconnectCount, 1, 'switching browser explicitly disconnects extension');
  await page.getByRole('button', { name: 'Start browser', exact: true }).waitFor();
  for (const profile of ['work','workflow']) {
    await page.goto(`${fixture}?profile=${profile}`); await openSettings();
    assert.equal(await page.locator('option[value="extension"]').count(),1,`${profile} offers extension`);
    await choice.selectOption('extension');
    await page.getByRole('button',{name:'Copy connection',exact:true}).click();
    assert.equal(JSON.parse(await page.evaluate(()=>navigator.clipboard.readText())).token,currentToken,'same account token in Code, Crew and workflow');
    if(profile==='workflow') {
      selected=connected=true;connectionID='workflow-ui-connection';tabs=[];
      await page.getByText('Ready for your agent',{exact:true}).waitFor({timeout:10000});
      await page.screenshot({path:`${evidence}/workflow-connected.png`,fullPage:true});
      selected=connected=false;tabs=[];
    }
  }
  const previousCalls=calls;
  await page.goto(`${fixture}?profile=relay`);await openSettings();
  assert.equal(await page.locator('option[value="extension"]').count(),0,'relay rollout remains deferred');
  assert.equal(calls,previousCalls,'relay does not query extension API');
  await page.goto(`${fixture}?theme=light`); await openSettings();
  await choice.selectOption('extension');
  await page.screenshot({ path: `${evidence}/setup-light.png`, fullPage: true });
  await page.goto(`${fixture}?brand=Acme`); await openSettings(); await choice.selectOption('extension');
  await page.getByRole('button', {name:'Copy connection',exact:true}).click();
  assert.equal(JSON.parse(await page.evaluate(() => navigator.clipboard.readText())).brand, 'Acme', 'connection carries deployment runtime brand');
  await page.goto(`${fixture}?notifications=1`);
  const notices = page.getByRole('list', {name:'Automatic chat notifications'}).locator('li');
  assert.equal(await notices.count(), 0, 'initial disconnected state is quiet');
  selected = connected = true; connectionID = 'notification-connection-one'; tabs = [];
  await notices.filter({hasText:'No tabs exist for this project yet'}).waitFor({timeout:10000});
  tabs = ['Shared website'];
  await notices.filter({hasText:'tab is now shared'}).waitFor({timeout:10000});
  connected = false; tabs = [];
  await notices.filter({hasText:'Do not fall back'}).waitFor({timeout:10000});
  const count = await notices.count(); await page.waitForTimeout(3000);
  assert.equal(await notices.count(), count, 'repeat polls do not duplicate auto messages');
  selected = false;
  await notices.filter({hasText:'extension is no longer selected'}).waitFor({timeout:10000});
  selected = connected = true; connectionID = 'notification-connection-two'; tabs = ['Shared website'];
  await notices.filter({hasText:'Shared tabs are available'}).waitFor({timeout:10000});
  selected=connected=false;tabs=[];connectionID='';
  await page.goto(`${fixture}?profile=workflow&notifications=1`);
  const workflowNotices=page.getByRole('list',{name:'Automatic chat notifications'}).locator('li');
  selected=connected=true;connectionID='workflow-notification-one';
  await workflowNotices.filter({hasText:'connected to this workflow'}).waitFor({timeout:10000});
  connected=false;
  await workflowNotices.filter({hasText:'disconnected from this workflow'}).waitFor({timeout:10000});
  console.log('PASS workflow connect/disconnect notices through the real preset binding and global queue');
  console.log('PASS browser-rendered connect, share-ready, disconnect, browser change and reconnect messages through the real global queue');
  console.log('PASS Code browser choice, hidden raw code, stable copy, connected states, reconnect identity, explicit browser switch, Code/Crew/workflow rollout, dark/light layouts at 1000/420px');
} finally { await browser.close(); }
