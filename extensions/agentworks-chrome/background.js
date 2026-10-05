// Authority is held only by this live worker. Restart requires human pairing.
let socket = null;
let workspace = '';
let brand = 'AgentWorks';
let error = '';
let heartbeat;
let discover = false;
let autoAttach = false;
const shared = new Map();
const sessions = new Map();
const groups = new Map();
let queue = Promise.resolve();

function safeURL(raw) {
  const u = new URL(raw);
  if (u.protocol !== 'https:' && u.protocol !== 'http:' && raw !== 'about:blank') throw new Error('Only HTTP(S) tabs and about:blank can be shared');
  return u.href;
}
function send(message) { if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify(message)); }
function announceTabs() { send({ type: 'tabs', tabs: shared.size, tab_titles: [...shared.values()].map(tab => tab.title || 'Untitled tab') }); }
function event(method, params, sessionId) { send({ type: 'cdp', message: { method, params, ...(sessionId ? { sessionId } : {}) } }); }
function target(tab) { return { targetId: `tab-${tab.id}`, type: 'page', title: tab.title || '', url: tab.url || 'about:blank', attached: sessions.has(`session-${tab.id}`), browserContextId: 'agentworks' }; }
async function tabForTarget(id) {
  const tab = [...shared.values()].find(t => `tab-${t.id}` === id);
  if (!tab) throw new Error('Target is not shared with this workspace');
  return tab;
}
async function attach(tab) {
  const connection = socket;
  if (!connection || !workspace) throw new Error('Chrome connection stopped');
  const id = `session-${tab.id}`;
  if (!sessions.has(id)) {
    await chrome.debugger.attach({ tabId: tab.id }, '1.3');
    if (socket !== connection || !workspace) { try { await chrome.debugger.detach({ tabId: tab.id }); } catch {} throw new Error('Chrome connection stopped'); }
    sessions.set(id, { tabId: tab.id });
  }
  return id;
}
async function announce(tab) {
  if (discover) event('Target.targetCreated', { targetInfo: target(tab) });
  if (autoAttach) {
    const sessionId = await attach(tab);
    event('Target.attachedToTarget', { sessionId, targetInfo: target(tab), waitingForDebugger: false });
  }
}
// Groups organize only tabs already authorized for this live connection.
async function groupSharedTab(tabId, connection = socket) {
  if (!connection || socket !== connection || !workspace || !shared.has(tabId)) throw new Error('Connection stopped');
  const tab = await chrome.tabs.get(tabId);
  if (socket !== connection || !workspace || !shared.has(tabId)) throw new Error('Connection stopped');
  const title = `${brand} · ${workspace.split('/').pop()}`;
  let groupId;
  try { groupId = await chrome.tabs.group({ ...(groups.has(tab.windowId) ? { groupId: groups.get(tab.windowId) } : { createProperties: { windowId: tab.windowId } }), tabIds: [tabId] }); }
  catch { if (socket !== connection || !shared.has(tabId)) throw new Error('Connection stopped'); groupId = await chrome.tabs.group({ createProperties: { windowId: tab.windowId }, tabIds: [tabId] }); }
  if (socket !== connection || !workspace || !shared.has(tabId)) {
    try { if ((await chrome.tabs.get(tabId)).groupId === groupId) await chrome.tabs.ungroup(tabId); } catch {}
    throw new Error('Connection stopped');
  }
  groups.set(tab.windowId, groupId);
  await chrome.tabGroups.update(groupId, { title, color: 'blue' });
}
async function share(tabId) {
  if (socket?.readyState !== WebSocket.OPEN || !workspace) throw new Error('Connect to a workspace first');
  if (shared.size >= 32) throw new Error('At most 32 tabs may be shared');
  const connection = socket;
  const tab = await chrome.tabs.get(tabId); safeURL(tab.pendingUrl || tab.url);
  if (socket !== connection || !workspace) throw new Error('Connection stopped');
  if (!shared.has(tabId)) {
    // Attach immediately: if Chrome rejects access, do not advertise authority.
    await attach(tab);
    shared.set(tabId, tab);
    try { await groupSharedTab(tabId, connection); } catch (e) { if (socket !== connection || !workspace) throw e; error = 'Tab shared, but its group could not be created.'; }
    await announce(tab);
    announceTabs();
  }
  return state();
}
async function unshare(tabId) {
  if (!shared.has(tabId)) return;
  shared.delete(tabId);
  for (const [id, debuggee] of sessions) if (debuggee.tabId === tabId) {
    sessions.delete(id); event('Target.detachedFromTarget', { sessionId: id, targetId: `tab-${tabId}` });
  }
  try { const tab = await chrome.tabs.get(tabId); if (groups.get(tab.windowId) === tab.groupId) await chrome.tabs.ungroup(tabId); } catch {}
  try { await chrome.debugger.detach({ tabId }); } catch {}
  event('Target.targetDestroyed', { targetId: `tab-${tabId}` });
  announceTabs();
}
async function stop(reason = '') {
  const previous = socket; socket = null; workspace = ''; brand = 'AgentWorks'; error = reason;
  clearInterval(heartbeat); discover = false; autoAttach = false;
  if (previous) { if (previous.readyState === WebSocket.OPEN) previous.send(JSON.stringify({ type: 'stop' })); previous.close(); }
  const ids = [...shared.keys()], previousGroups = new Map(groups); shared.clear(); sessions.clear(); groups.clear();
  await Promise.allSettled(ids.map(async tabId => {
    try { await chrome.debugger.detach({ tabId }); } catch {}
    try { const tab = await chrome.tabs.get(tabId); if (previousGroups.get(tab.windowId) === tab.groupId) await chrome.tabs.ungroup(tabId); } catch {}
  }));
  await chrome.action.setBadgeText({ text: '' });
}
function state() { return { connected: !!workspace && socket?.readyState === WebSocket.OPEN, workspace, server: socket ? new URL(socket.url).host : '', error, tabs: [...shared.values()].map(t => ({ id: t.id, title: t.title || t.url })) }; }
async function connect(raw) {
  const pairing = JSON.parse(raw);
  // Display metadata follows the app's runtime branding; it grants no access.
  const displayBrand = typeof pairing.brand === 'string' && pairing.brand.trim().length > 0 && pairing.brand.trim().length <= 120 && !/[\x00-\x1f\x7f]/.test(pairing.brand) ? pairing.brand.trim() : 'AgentWorks';
  const endpoint = new URL(pairing.url);
  if (endpoint.pathname !== '/api/browser/extension/connect' || endpoint.search || endpoint.hash || endpoint.username || endpoint.password) throw new Error('Invalid pairing connection');
  if (endpoint.protocol !== 'wss:' && !(endpoint.protocol === 'ws:' && ['localhost', '127.0.0.1'].includes(endpoint.hostname))) throw new Error('A secure platform connection is required');
  if (typeof pairing.token !== 'string' || pairing.token.length < 32) throw new Error('Invalid pairing credential');
  await stop();
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(endpoint.href); socket = ws;
    const timer = setTimeout(() => { if (socket === ws) void stop('Connection timed out'); reject(new Error('Connection timed out')); }, 10000);
    ws.onopen = () => ws.send(JSON.stringify({ type: 'pair', token: pairing.token }));
    ws.onmessage = ({ data }) => {
      if (socket !== ws) return;
      let e; try { e = JSON.parse(data); } catch { void stop('Invalid server response'); return; }
      if (e.type === 'paired') {
        clearTimeout(timer); workspace = e.workspace; brand = displayBrand; error = '';
        heartbeat = setInterval(() => send({ type: 'ping' }), 25000);
        void chrome.action.setBadgeText({ text: 'ON' }); resolve(state());
      } else if (e.type === 'error') {
        clearTimeout(timer); reject(new Error(e.workspace)); void stop(e.workspace);
      } else if (e.type === 'cdp') {
        // CDP events are asynchronous; requests execute in arrival order.
        queue = queue.then(() => handleCDP(e.message, ws, e.active === true)).catch(() => {});
      } else if (e.type === 'client-disconnected') {
        discover = false; autoAttach = false;
      }
    };
    ws.onerror = () => { clearTimeout(timer); reject(new Error('Cannot connect to the platform')); };
    ws.onclose = () => { clearTimeout(timer); reject(new Error('Connection closed')); if (socket === ws) void stop('Browser disconnected. Paste your saved connection code to reconnect.'); };
  });
}

async function command(message, active = false) {
  const { method, sessionId, params = {} } = message;
  if (!sessionId) {
    switch (method) {
      case 'Browser.getVersion': return { protocolVersion: '1.3', product: navigator.userAgent.match(/Chrome\/[^ ]+/)?.[0] || 'Chrome/125.0.0.0', revision: '', userAgent: navigator.userAgent, jsVersion: '' };
      case 'Target.getBrowserContexts': return { browserContextIds: [] };
      case 'Target.getTargets': return { targetInfos: [...shared.values()].map(target) };
      case 'Target.getTargetInfo': return { targetInfo: target(await tabForTarget(params.targetId)) };
      case 'Target.setDiscoverTargets': discover = !!params.discover; if (discover) for (const tab of shared.values()) event('Target.targetCreated', { targetInfo: target(tab) }); return {};
      case 'Target.setAutoAttach': autoAttach = !!params.autoAttach; if (autoAttach) for (const tab of shared.values()) { const id = await attach(tab); event('Target.attachedToTarget', { sessionId: id, targetInfo: target(tab), waitingForDebugger: false }); } return {};
      case 'Target.attachToTarget': { const tab = await tabForTarget(params.targetId); return { sessionId: await attach(tab) }; }
      case 'Target.detachFromTarget': { const src = sessions.get(params.sessionId); if (!src) throw new Error('Session is not shared'); sessions.delete(params.sessionId); await chrome.debugger.detach(src); return {}; }
      case 'Target.createTarget': {
        if (!workspace || socket?.readyState !== WebSocket.OPEN) throw new Error('Connect first');
        if (shared.size >= 32) throw new Error('Shared tab limit reached');
        const tab = await chrome.tabs.create({ url: safeURL(params.url || 'about:blank'), active });
        try { await attach(tab); shared.set(tab.id, tab); try { await groupSharedTab(tab.id); } catch (e) { if (!workspace || !shared.has(tab.id)) throw e; error = 'Tab shared, but its group could not be created.'; } await announce(tab); announceTabs(); return { targetId: `tab-${tab.id}` }; }
        catch (e) { await chrome.tabs.remove(tab.id); throw e; }
      }
      case 'Target.closeTarget': { const tab = await tabForTarget(params.targetId); await unshare(tab.id); await chrome.tabs.remove(tab.id); return { success: true }; }
      case 'Target.activateTarget': { const tab = await tabForTarget(params.targetId); if (active) await chrome.tabs.update(tab.id, { active: true }); return {}; }
      default: throw new Error(`Unsupported browser operation: ${method}`);
    }
  }
  const source = sessions.get(sessionId);
  if (!source || !shared.has(source.tabId)) throw new Error('Session is not shared with this workspace');
  // agent-browser also sends Page.bringToFront during logical tab selection.
  if (method === 'Page.bringToFront' && !active) return {};
  const domain = method.split('.')[0];
  const allowed = new Set(['Accessibility', 'DOM', 'DOMSnapshot', 'Runtime', 'Page', 'Input', 'CSS', 'Log', 'Console', 'Performance']);
  if (!allowed.has(domain) && !['Network.enable', 'Network.disable', 'Network.getResponseBody', 'Network.setCacheDisabled', 'Network.emulateNetworkConditions', 'Network.setUserAgentOverride', 'Target.setAutoAttach', 'Target.detachFromTarget'].includes(method)) throw new Error(`Unsupported extension operation: ${method}`);
  if (['DOM.setFileInputFiles', 'Page.setDownloadBehavior', 'Page.addScriptToEvaluateOnNewDocument', 'Runtime.addBinding'].includes(method)) throw new Error(`Unsupported extension operation: ${method}`);
  if (method === 'Page.navigate') safeURL(params.url);
  if (method === 'Page.captureScreenshot' && params.clip?.height > 16000) throw new Error('SCREENSHOT_TOO_TALL: full-page screenshots are limited to 16000 px; capture the viewport after scrolling');
  return await chrome.debugger.sendCommand(source, method, params) || {};
}
async function handleCDP(message, connection, active) {
  if (socket !== connection || !workspace) return;
  if (!message || typeof message.id !== 'number' || typeof message.method !== 'string') return;
  try { const result = await command(message, active); if (socket === connection && workspace) send({ type: 'cdp', message: { id: message.id, ...(message.sessionId ? { sessionId: message.sessionId } : {}), result } }); }
  catch (e) { if (socket === connection && workspace) send({ type: 'cdp', message: { id: message.id, ...(message.sessionId ? { sessionId: message.sessionId } : {}), error: { code: -32000, message: e.message } } }); }
}
chrome.debugger.onEvent.addListener((source, method, params) => {
  if (!shared.has(source.tabId)) return;
  const id = source.sessionId || `session-${source.tabId}`;
  if (method === 'Target.attachedToTarget') sessions.set(params.sessionId, { tabId: source.tabId, sessionId: params.sessionId });
  if (method === 'Target.detachedFromTarget') sessions.delete(params.sessionId);
  event(method, params || {}, id);
});
chrome.debugger.onDetach.addListener(source => { if (shared.has(source.tabId)) void unshare(source.tabId); });
chrome.tabs.onRemoved.addListener(tabId => { void unshare(tabId); });
chrome.tabs.onUpdated.addListener((tabId, change, tab) => {
  if (!shared.has(tabId)) return;
  try { safeURL(tab.pendingUrl || tab.url || 'about:blank'); shared.set(tabId, tab); if (change.url || change.title) { event('Target.targetInfoChanged', { targetInfo: target(tab) }); announceTabs(); } }
  catch { void unshare(tabId); }
});
chrome.runtime.onMessage.addListener((request, sender, respond) => {
  // No externally_connectable or content scripts: only our own popup can pair.
  if (sender.id !== chrome.runtime.id) return;
  const run = async () => {
    if (request.action === 'state') return state();
    if (request.action === 'connect') return connect(request.pairing);
    if (request.action === 'share') return share(request.tabId);
    if (request.action === 'newtab') {
      const result = await command({ method: 'Target.createTarget', params: { url: 'about:blank' } });
      const tab = await tabForTarget(result.targetId); await chrome.tabs.update(tab.id, { active: true }); return state();
    }
    if (request.action === 'group') {
      if (!workspace || socket?.readyState !== WebSocket.OPEN) throw new Error('Connect first');
      const connection = socket;
      for (const id of [...shared.keys()]) await groupSharedTab(id, connection);
      return state();
    }
    if (request.action === 'unshare') { await unshare(request.tabId); return state(); }
    if (request.action === 'stop') { await stop(); return state(); }
    throw new Error('Unknown action');
  };
  run().then(value => respond({ ok: true, ...value }), e => respond({ ok: false, error: e.message }));
  return true;
});
