// Authority lives only in this worker; restart requires human pairing.
// Each project has its own socket, target/session maps, controller and tab groups.
const connections = new Map();
const tabOwners = new Map();
let accountPairing = null;
let availableProjects = [];
let selectedScope = '';
let popupQueue = Promise.resolve();
function updateBadge() { void chrome.action.setBadgeText({text:[...connections.values()].some(c=>c.state().connected) ? 'ON' : ''}); }
function state() {
 const c = connections.get(selectedScope);
 return {...(c?.state() || {connected:false,workspace:'',server:'',error:'',tabs:[]}), selectedScope,
  projects:availableProjects.map(p=>({...p,connected:connections.get(p.scope)?.state().connected || false}))};
}
async function connectProject(raw) {
 const pairing = JSON.parse(raw);
 if (![...connections.values()].some(c=>c.state().connected)) {accountPairing=null;availableProjects=[];connections.clear();}
 if (accountPairing && (accountPairing.url !== pairing.url || accountPairing.token !== pairing.token)) throw new Error('Disconnect all projects before connecting another account or server');
 const previous = connections.get(pairing.scope);
 if (previous) await previous.stop();
 const c = createConnection();
 await c.connect(raw);
 if (!c.scope) {await c.stop(); throw new Error('Update the platform before connecting');}
 const old = connections.get(c.scope);
 if (old && old !== previous) await old.stop();
 accountPairing = pairing; connections.set(c.scope,c); selectedScope = c.scope; updateBadge();
 return state();
}
function createConnection() {
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
  if (tabOwners.has(tab.id) && tabOwners.get(tab.id) !== api) throw new Error('This tab is already shared with another project');
  const connection = socket;
  if (!connection || !workspace) throw new Error('Chrome connection stopped');
  const id = `session-${tab.id}`;
  if (!sessions.has(id)) {
    tabOwners.set(tab.id, api);
    try { await chrome.debugger.attach({ tabId: tab.id }, '1.3'); } catch (e) { if (tabOwners.get(tab.id) === api) tabOwners.delete(tab.id); throw e; }
    if (socket !== connection || !workspace) { try { await chrome.debugger.detach({ tabId: tab.id }); } catch {} if (tabOwners.get(tab.id) === api) tabOwners.delete(tab.id); throw new Error('Chrome connection stopped'); }
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
  if (tabOwners.get(tabId) === api) tabOwners.delete(tabId);
  event('Target.targetDestroyed', { targetId: `tab-${tabId}` });
  announceTabs();
}
async function stop(reason = '') {
  const previous = socket; socket = null; workspace = ''; brand = 'AgentWorks'; error = reason;
  clearInterval(heartbeat); discover = false; autoAttach = false;
  if (previous) { if (previous.readyState === WebSocket.OPEN) previous.send(JSON.stringify({ type: 'stop' })); previous.close(); }
  const ids = [...tabOwners].filter(([, owner]) => owner === api).map(([id]) => id), previousGroups = new Map(groups); shared.clear(); sessions.clear(); groups.clear();
  await Promise.allSettled(ids.map(async tabId => {
    try { await chrome.debugger.detach({ tabId }); } catch {}
    if (tabOwners.get(tabId) === api) tabOwners.delete(tabId);
    try { const tab = await chrome.tabs.get(tabId); if (previousGroups.get(tab.windowId) === tab.groupId) await chrome.tabs.ungroup(tabId); } catch {}
  }));
  updateBadge();
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
    ws.onopen = () => ws.send(JSON.stringify({ type: 'pair', token: pairing.token, scope: pairing.scope }));
    ws.onmessage = ({ data }) => {
      if (socket !== ws) return;
      let e; try { e = JSON.parse(data); } catch { void stop('Invalid server response'); return; }
      if (e.type === 'paired') {
        clearTimeout(timer); workspace = e.workspace; api.scope = e.scope; api.profile = e.profile_id; if(Array.isArray(e.projects))availableProjects=e.projects; brand = displayBrand; error = '';
        heartbeat = setInterval(() => send({ type: 'ping' }), 25000);
        updateBadge(); resolve(state());
      } else if (e.type === 'connect-project') {
        const previousScope=selectedScope;
        const attachProject=async()=>{if(socket!==ws || !accountPairing)return;try{await connectProject(JSON.stringify({...accountPairing,scope:e.scope}));}finally{if(connections.get(previousScope)?.state().connected)selectedScope=previousScope;}};
        popupQueue=popupQueue.then(attachProject,attachProject);
      } else if (e.type === 'target-command') {
        queue=queue.then(async()=>{if(socket!==ws || !workspace)return;try{const result=await command(e.target_id ? {method:'Target.closeTarget',params:{targetId:e.target_id}} : {method:'Target.createTarget',params:{url:e.url}},e.active===true);send({type:'target-result',request_id:e.request_id,target_id:e.target_id || result.targetId});}catch(error){send({type:'target-result',request_id:e.request_id,error:error.message});}}).catch(()=>{});
      } else if (e.type === 'projects' || e.type === 'pong') {
        if (Array.isArray(e.projects)) availableProjects = e.projects;
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

  const api = {
    scope: '', profile: '', connect, state, share, unshare, stop,
    async newtab() { const result = await command({ method:'Target.createTarget', params:{url:'about:blank'} }); const tab = await tabForTarget(result.targetId); await chrome.tabs.update(tab.id,{active:true}); },
    async group() { for (const id of [...shared.keys()]) await groupSharedTab(id); },
    onEvent(source,method,params) {
      if (!shared.has(source.tabId)) return;
      const id = source.sessionId || `session-${source.tabId}`;
      if (method === 'Target.attachedToTarget') sessions.set(params.sessionId,{tabId:source.tabId,sessionId:params.sessionId});
      if (method === 'Target.detachedFromTarget') sessions.delete(params.sessionId);
      event(method,params || {},id);
    },
    onUpdated(tabId,change,tab) {
      if (!shared.has(tabId)) return;
      try { safeURL(tab.pendingUrl || tab.url || 'about:blank'); shared.set(tabId,tab); if(change.url || change.title) {event('Target.targetInfoChanged',{targetInfo:target(tab)});announceTabs();} }
      catch { void unshare(tabId); }
    }
  };
  return api;
}

chrome.debugger.onEvent.addListener((source,method,params)=>tabOwners.get(source.tabId)?.onEvent(source,method,params));
chrome.debugger.onDetach.addListener(source=>{const c=tabOwners.get(source.tabId);if(c)void c.unshare(source.tabId);});
chrome.tabs.onRemoved.addListener(tabId=>{const c=tabOwners.get(tabId);if(c)void c.unshare(tabId);});
chrome.tabs.onUpdated.addListener((tabId,change,tab)=>tabOwners.get(tabId)?.onUpdated(tabId,change,tab));
chrome.runtime.onMessage.addListener((request,sender,respond)=>{
 if(sender.id !== chrome.runtime.id) return;
 const run=async()=>{
  if(request.action==='state')return state();
  if(request.action==='connect') {
   await connectProject(request.pairing);
   const c=connections.get(selectedScope);
   const [tab]=await chrome.tabs.query({active:true,currentWindow:true});
   // Connect is a human action granting this workspace the current website.
   // Automatic project connections above never adopt the user's active tab.
   const url=tab?.pendingUrl || tab?.url || '';
   if(c && tab && (/^https?:\/\//.test(url) || url==='about:blank') && (!tabOwners.has(tab.id) || tabOwners.get(tab.id)===c)) {
    try {await c.share(tab.id);} catch { /* Pairing remains usable if Chrome refuses this page. */ }
   }
   return state();
  }
  if(request.action==='select-project') {
   if(!availableProjects.some(p=>p.scope===request.scope) || !accountPairing)throw new Error('Project is not available to this account');
   if(!connections.get(request.scope)?.state().connected)await connectProject(JSON.stringify({...accountPairing,scope:request.scope}));
   selectedScope=request.scope;return state();
  }
  if(request.action==='stop-all') {
   await Promise.allSettled([...connections.values()].map(c=>c.stop()));connections.clear();accountPairing=null;availableProjects=[];selectedScope='';return state();
  }
  const c=connections.get(selectedScope);if(!c)throw new Error('Connect to a project first');
  if(request.action==='share')await c.share(request.tabId);
  else if(request.action==='newtab')await c.newtab();
  else if(request.action==='group')await c.group();
  else if(request.action==='unshare')await c.unshare(request.tabId);
  else if(request.action==='stop') {
   await c.stop();connections.delete(selectedScope);selectedScope=[...connections.keys()].find(k=>connections.get(k).state().connected)||'';
   if(!selectedScope){accountPairing=null;availableProjects=[];connections.clear();}
  }
  else throw new Error('Unknown action');
  return state();
 };
 // Popup mutations serialize so two Share clicks cannot claim one tab twice.
 if(request.action==='state'){respond({ok:true,...state()});return;}
 popupQueue=popupQueue.then(run,run);
 popupQueue.then(value=>respond({ok:true,...value}),e=>respond({ok:false,error:e.message}));return true;
});
