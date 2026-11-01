import { setupConnection } from './pairing.js';
const $ = id => document.getElementById(id);
let busy = false;
let setup = null, preview = null;
let current = { connected: false, tabs: [] };
document.querySelector('.subtitle').textContent = `Browser connection · v${chrome.runtime.getManifest().version}`;
function updateButtons() {
  for (const button of document.querySelectorAll('button')) button.disabled = busy;
  for(const id of ['share','newtab','group'])$(id).disabled=busy || !current.projectConnected;
  $('connect').disabled = busy || !setup;
  $('group').disabled = busy || !current.projectConnected || !current.tabs.length;
  $('connect').textContent = busy ? 'Connecting…' : preview ? 'Connect browser' : 'Continue';
}
async function run(request) {
  const result = await chrome.runtime.sendMessage(request);
  if (!result?.ok) throw new Error(result?.error || 'Extension unavailable');
  render(result); return result;
}
function showError(message = '') { $('error').textContent = message; $('error').hidden = !message; }
function render(state) {
  current = state;
  $('status').textContent = state.connected ? 'Connected' : state.reconnecting ? 'Reconnecting…' : 'Not connected';
  $('status').classList.toggle('connected', state.connected);
  showError(state.error);
  $('setup').hidden = state.connected || state.remembered;
  $('controls').hidden = !state.connected && !state.remembered;
  $('workspace').textContent = state.accountName || 'AgentWorks';
  $('server').textContent = state.server || '';
  $('project').replaceChildren(...(state.projects?.length ? state.projects : [{scope:'',name:'Choose a project in AgentWorks',profile_id:''}]).map(p=>{const option=document.createElement('option');option.value=p.scope;option.textContent=p.scope ? `${p.profile_id === 'workflow' ? 'Workflow' : p.profile_id === 'work' ? 'Crew' : 'Code'} · ${p.name || p.workspace.split('/').pop()}${p.connected ? ' · in use here' : ''}` : p.name;option.selected=p.scope === state.selectedScope;return option;}));
  $('tab-count').textContent = String(state.tabs.length);
  $('empty').hidden = !!state.tabs.length;
  $('empty').querySelector('strong').textContent = state.connected ? 'Ready for your agent' : 'Restoring your connection';
  $('empty').querySelector('p').textContent = state.connected ? 'Your agent can create its own tabs. Sharing an already-open page below is optional.' : 'This browser will reconnect automatically when the platform is available.';
  document.querySelector('.workspace .eyebrow').textContent = state.connected ? 'CONNECTED TO' : 'RECONNECTING TO';
  $('stop').hidden = !state.selectedScope;
  $('project').disabled = busy || !state.connected || !state.projects?.length;
  $('tabs').replaceChildren(...state.tabs.map(tab => {
    const row = document.createElement('li'), title = document.createElement('span'), remove = document.createElement('button');
    title.textContent = tab.title; title.title = tab.title; remove.textContent = 'Unshare'; remove.setAttribute('aria-label', `Unshare ${tab.title}`);
    remove.onclick = () => action(() => run({ action: 'unshare', tabId: tab.id }));
    row.append(title, remove); return row;
  }));
  updateButtons();
}
async function action(fn) {
  showError(); busy = true; updateButtons();
  try { await fn(); } catch (error) { showError(error.message); }
  finally { busy = false; updateButtons(); }
}
async function previewConnection() {
 const result = await chrome.runtime.sendMessage({ action:'pairing-preview', tabId:setup.tabId, setupURL:setup.url });
 if (!result?.ok) throw new Error(result?.error || 'Could not check this connection.');
 preview = result.setup;
 $('pairing-account').textContent = `Connect this browser to ${preview.account} on ${preview.origin}`;
 $('pairing-help').textContent = 'Check that this is your account and server.';
 updateButtons();
}
async function inspectSetupTab() {
 const [tab] = await chrome.tabs.query({active:true,currentWindow:true});
 try { setup = {...setupConnection(tab?.url || ''),tabId:tab.id}; } catch { return; }
 $('pairing-account').textContent = `Server: ${setup.origin}`;
 if (await chrome.permissions.contains({origins:[setup.permission]})) await previewConnection();
}
$('connect').onclick = () => action(async () => {
 if (!setup) throw new Error('Open the connection tab from AgentWorks first.');
 if (!preview) {
  if (!await chrome.permissions.request({origins:[setup.permission]})) throw new Error('Browser connection permission was declined.');
  await previewConnection();
  return;
 }
 await run({action:'pairing-connect',tabId:setup.tabId,setupURL:setup.url,accountId:preview.account_id});
});
$('share').onclick = () => action(async () => {
  const [tab] = await chrome.tabs.query({ active: true, currentWindow: true });
  if (!tab) throw new Error('Open a website tab first.');
  await run({ action: 'share', tabId: tab.id });
});
$('newtab').onclick = () => action(() => run({ action: 'newtab' }));
$('group').onclick = () => action(() => run({ action: 'group' }));
$('project').onchange = () => action(() => run({action:'select-project',scope:$('project').value}));
$('stop-all').onclick = () => action(() => run({action:'stop-all'}));
$('stop').onclick = () => action(() => run({ action: 'stop' }));
void action(async () => { await run({action:'state'}); if (!current.remembered) await inspectSetupTab(); });
// Reflect Stop, closed tabs and connection loss while the popup remains open.
setInterval(() => { if (!busy) void run({ action: 'state' }).catch(error => showError(error.message)); }, 1500);
