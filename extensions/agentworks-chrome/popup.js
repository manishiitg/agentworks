const $ = id => document.getElementById(id);
let busy = false;
let current = { connected: false, tabs: [] };
function updateButtons() {
  for (const button of document.querySelectorAll('button')) button.disabled = busy;
  $('connect').disabled = busy || !$('pairing').value.trim();
  $('group').disabled = busy || !current.tabs.length;
  $('connect').textContent = busy && !current.connected ? 'Connecting…' : 'Connect browser';
}
async function run(request) {
  const result = await chrome.runtime.sendMessage(request);
  if (!result?.ok) throw new Error(result?.error || 'Extension unavailable');
  render(result); return result;
}
function showError(message = '') { $('error').textContent = message; $('error').hidden = !message; }
function render(state) {
  current = state;
  $('status').textContent = state.connected ? 'Connected' : 'Not connected';
  $('status').classList.toggle('connected', state.connected);
  showError(state.error);
  $('setup').hidden = state.connected;
  $('controls').hidden = !state.connected;
  $('workspace').textContent = (state.workspace || '').split('/').filter(Boolean).pop() || '';
  $('server').textContent = state.server || '';
  $('project').replaceChildren(...(state.projects || []).map(p=>{const option=document.createElement('option');option.value=p.scope;option.textContent=`${p.profile_id === 'workflow' ? 'Workflow' : p.profile_id === 'work' ? 'Crew' : 'Code'} · ${p.workspace.split('/').pop()}${p.connected ? ' · connected' : ''}`;option.selected=p.scope === state.selectedScope;return option;}));
  $('tab-count').textContent = String(state.tabs.length);
  $('empty').hidden = !!state.tabs.length;
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
$('pairing').oninput = updateButtons;
$('connect').onclick = () => action(async () => {
  const raw = $('pairing').value.trim();
  let pairing, url;
  try {
    pairing = JSON.parse(raw); url = new URL(pairing.url);
    if (url.pathname !== '/api/browser/extension/connect' || url.search || url.hash || url.username || url.password || typeof pairing.token !== 'string' || pairing.token.length < 32 || (url.protocol !== 'wss:' && !(url.protocol === 'ws:' && ['localhost', '127.0.0.1'].includes(url.hostname)))) throw new Error();
  } catch { throw new Error('Paste the full connection code copied from AgentWorks Browser settings.'); }
  if (!await chrome.permissions.request({ origins: [`${url.protocol === 'wss:' ? 'https:' : 'http:'}//${url.host}/*`] })) throw new Error('Platform connection permission was declined.');
  await run({ action: 'connect', pairing: raw }); $('pairing').value = '';
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
void action(() => run({ action: 'state' }));
// Reflect Stop, closed tabs and connection loss while the popup remains open.
setInterval(() => { if (!busy) void run({ action: 'state' }).catch(error => showError(error.message)); }, 1500);
