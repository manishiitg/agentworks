const $ = id => document.getElementById(id);
async function run(request) {
  const result = await chrome.runtime.sendMessage(request);
  if (!result?.ok) throw new Error(result?.error || 'Extension unavailable');
  render(result); return result;
}
function render(s) {
  $('status').textContent = s.connected ? `Connected to ${s.workspace}` : 'Not connected';
  $('error').textContent = s.error || '';
  $('controls').hidden = !s.connected;
  $('tabs').replaceChildren(...s.tabs.map(tab => {
    const row = document.createElement('li'), title = document.createElement('span'), remove = document.createElement('button');
    title.textContent = tab.title; remove.textContent = 'Unshare'; remove.onclick = () => action(() => run({ action: 'unshare', tabId: tab.id })); row.append(title, remove); return row;
  }));
}
async function action(fn) {
  $('error').textContent = '';
  for (const button of document.querySelectorAll('button')) button.disabled = true;
  try { await fn(); } catch (e) { $('error').textContent = e.message; }
  finally { for (const button of document.querySelectorAll('button')) button.disabled = false; }
}
$('connect').onclick = () => action(async () => {
  const raw = $('pairing').value.trim();
  const pairing = JSON.parse(raw), url = new URL(pairing.url);
  // Host permission is requested only for the server the user chose.
  if (!await chrome.permissions.request({ origins: [`${url.protocol === 'wss:' ? 'https:' : 'http:'}//${url.host}/*`] })) throw new Error('Platform connection permission was declined');
  await run({ action: 'connect', pairing: raw }); $('pairing').value = '';
});
$('share').onclick = () => action(async () => { const [tab] = await chrome.tabs.query({ active: true, currentWindow: true }); await run({ action: 'share', tabId: tab.id }); });
$('stop').onclick = () => action(() => run({ action: 'stop' }));
void action(() => run({ action: 'state' }));
