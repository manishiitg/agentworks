// Remember explicitly enabled connections locally; tab grants survive only the browser session.
// Each project has its own socket, target/session maps, controller and tab groups.
const connections = new Map();
const tabOwners = new Map();
// Events that happen while no server socket is open (a refused handshake, a closed socket, a scheduled retry) are kept
// briefly and sent after the next pairing, with their age, so the server log shows them (PLAT-788).
const offlineEvents = [];
function offlineDiagnostic(event, scope, reason = '', extra = {}) {
  const record = { event, tab_id: 0, reason, method: '', request_id: '', duration_ms: 0, version: chrome.runtime.getManifest().version, at: Date.now(), ...extra };
  console.info('[CHROME_EXTENSION]', JSON.stringify({ scope, ...record }));
  offlineEvents.push(record); if (offlineEvents.length > 48) offlineEvents.shift();
}
function chromeMajor() { return navigator.userAgent.match(/Chrome\/(\d{1,4})/)?.[1] || ''; }
// Chrome's error text mapped to a fixed code. The raw text is never logged: it can carry page details.
function errorCode(message) {
  const text = String(message || '');
  if (text === 'Cannot access a chrome-extension:// URL of different extension') return 'foreign_frame';
  if (/Another debugger is already attached/i.test(text)) return 'another_debugger';
  if (/Cannot attach to this target/i.test(text)) return 'cannot_attach';
  if (/No tab with (given )?id|tab was closed/i.test(text)) return 'no_tab';
  if (/Cannot access (a )?(chrome|devtools|about|file|edge)/i.test(text)) return 'not_allowed_url';
  if (/Connection stopped|not shared/i.test(text)) return 'not_shared';
  if (/time(d)? ?out/i.test(text)) return 'timeout';
  return 'other';
}
// What a frame's address says about it, without the address: its scheme and, for an extension page, the extension ID.
function frameFacts(rawUrl) {
  let u; try { u = new URL(rawUrl); } catch { return { scheme: 'other', ext_ids: [] }; }
  const scheme = u.protocol.replace(':', '');
  const known = ['https', 'http', 'chrome-extension', 'about', 'blob', 'data', 'chrome', 'chrome-untrusted', 'devtools', 'file'];
  const ext = scheme === 'chrome-extension' && /^[a-p]{32}$/.test(u.hostname) && u.hostname !== chrome.runtime.id ? [u.hostname] : [];
  return { scheme: known.includes(scheme) ? scheme : 'other', ext_ids: ext, own_ext: scheme === 'chrome-extension' && u.hostname === chrome.runtime.id };
}
function tabFacts(tab) {
  const flags = [tab.active && 'active', tab.discarded && 'discarded', tab.incognito && 'incognito', tab.groupId >= 0 && 'grouped', tab.audible && 'audible', tab.pinned && 'pinned', tab.frozen && 'frozen'].filter(Boolean).join(',');
  return { tab_status: ['loading', 'complete', 'unloaded'].includes(tab.status) ? tab.status : '', tab_flags: flags };
}
let accountPairing = null;
let availableProjects = [];
let selectedScope = '';
let popupQueue = Promise.resolve();
const savedPairings = new Map();
const savedTabs = new Map();
const savedChatTabs = new Map();
const retries = new Map();
const storageKey = 'browserConnections';
const retryAlarm = 'browser-reconnect';
let storageWrites = Promise.resolve();
let retryTimer;
let storageError = '';
let resumeQueued = false;
function saveRemembered() {
 const record = {pairings:[...savedPairings.values()],selectedScope,projects:availableProjects};
 const grants = Object.fromEntries(savedTabs);
 const hasPairings = record.pairings.length > 0;
 const write = () => Promise.all([
  hasPairings ? chrome.storage.local.set({[storageKey]:record}) : chrome.storage.local.remove(storageKey),
  chrome.storage.session.set({browserSharedTabs:grants,browserChatTabs:Object.fromEntries(savedChatTabs)})
 ]);
 storageWrites = storageWrites.then(write,write).catch(()=>{storageError='Connection is live, but could not be remembered. Reconnect after checking extension storage.';});
 return storageWrites;
}
async function initialize() {
 await Promise.all([
  chrome.storage.local.setAccessLevel({accessLevel:'TRUSTED_CONTEXTS'}),
  chrome.storage.session.setAccessLevel({accessLevel:'TRUSTED_CONTEXTS'})
 ]);
 const [{browserConnections:record},{browserSharedTabs:grants,browserChatTabs:chatGrants}] = await Promise.all([
  chrome.storage.local.get(storageKey),chrome.storage.session.get(['browserSharedTabs','browserChatTabs'])
 ]);
 for(const pairing of record?.pairings || []) {
  if(typeof pairing.scope==='string' && pairing.scope && typeof pairing.token==='string' && typeof pairing.url==='string') savedPairings.set(pairing.scope,pairing);
 }
 accountPairing = savedPairings.values().next().value || null;
 if(accountPairing)accountPairing={...accountPairing,resume:false};
 selectedScope = savedPairings.has(record?.selectedScope) ? record.selectedScope : savedPairings.keys().next().value || '';
 availableProjects = Array.isArray(record?.projects) ? record.projects : [];
 for(const [scope,ids] of Object.entries(grants || {})) if(savedPairings.has(scope) && Array.isArray(ids))savedTabs.set(scope,ids.filter(Number.isInteger).slice(0,32));
 for(const [scope,metadata] of Object.entries(chatGrants || {})) if(savedPairings.has(scope) && metadata && typeof metadata==='object')savedChatTabs.set(scope,metadata);
 await chrome.alarms.create(retryAlarm,{periodInMinutes:0.5});
}
async function forgetProject(scope,reason='') {
 savedPairings.delete(scope);savedTabs.delete(scope);savedChatTabs.delete(scope);retries.delete(scope);
 const c=connections.get(scope);
 connections.delete(scope);
 if(selectedScope===scope)selectedScope=savedPairings.keys().next().value || '';
 if(!savedPairings.size){accountPairing=null;availableProjects=[];}
 await saveRemembered();
 await c?.stop(reason);updateBadge();
}
function retryLater(scope) {
 if(!savedPairings.has(scope))return;
 const delay=Math.min((retries.get(scope)?.delay || 500)*2,30000);
 retries.set(scope,{delay,after:Date.now()+delay,attempt:(retries.get(scope)?.attempt || 0)+1});
 offlineDiagnostic('reconnect_scheduled',scope,'',{delay_ms:delay,attempt:retries.get(scope).attempt,chrome:chromeMajor()});
 clearTimeout(retryTimer);
 retryTimer=setTimeout(queueResume,Math.max(100,[...retries.values()].reduce((n,r)=>Math.min(n,r.after-Date.now()),30000)));
}
function queueResume() {
 if(resumeQueued || ![...savedPairings.keys()].some(scope=>!connections.get(scope)?.state().connected))return;
 resumeQueued=true;
 const resume=async()=>{
  try {
   await ready;
   const previousScope=selectedScope;
   for(const [scope,pairing] of [...savedPairings]) {
    if(connections.get(scope)?.state().connected || Date.now()<(retries.get(scope)?.after || 0))continue;
    const ids=[...(savedTabs.get(scope) || [])], metadata=savedChatTabs.get(scope) || {};
    try {
     await connectProject(JSON.stringify(pairing));
     const c=connections.get(scope);
     // IDs belong only to this browser session. Never restore tabs by URL/title,
     // group membership, or whichever page is now in the foreground.
     for(const id of ids) {
      if(tabOwners.has(id) && tabOwners.get(id)!==c)continue;
      try {await chrome.debugger.detach({tabId:id});} catch {}
      try {if(await c.restoreGrant(id,metadata[id]))await c.share(id);} catch {}
     }
     savedTabs.set(scope,c.state().tabs.map(t=>t.id));
     retries.delete(scope);
    } catch {retryLater(scope);}
   }
   if(savedPairings.has(previousScope))selectedScope=previousScope;
   await saveRemembered();updateBadge();
  } finally {resumeQueued=false;}
 };
 popupQueue=popupQueue.then(resume,resume);
}
function updateBadge() { void chrome.action.setBadgeText({text:[...connections.values()].some(c=>c.state().connected) ? 'ON' : ''}); }
function state() {
 const c = connections.get(selectedScope);
 const remembered=savedPairings.has(selectedScope);
 const live=c?.state() || {connected:false,workspace:'',server:'',error:'',tabs:[]};
 return {...live,
  workspace:live.workspace || availableProjects.find(p=>p.scope===selectedScope)?.workspace || '',
  server:live.server || (remembered ? new URL(savedPairings.get(selectedScope).url).host : ''),
  error:live.error || storageError,
  selectedScope, remembered, reconnecting:remembered && !live.connected,
  projects:availableProjects.map(p=>({...p,connected:connections.get(p.scope)?.state().connected || false}))};
}
async function connectProject(raw) {
 const pairing = JSON.parse(raw);
 const changedAccount=accountPairing && (accountPairing.url!==pairing.url || accountPairing.token!==pairing.token);
 if(changedAccount && [...connections.values()].some(c=>c.state().connected))throw new Error('Disconnect all projects before connecting another account or server');
 const previous = changedAccount ? null : connections.get(pairing.scope);
 if (previous) await previous.stop();
 const c = createConnection();
 await c.connect(raw);
 if (!c.scope) {await c.stop(); throw new Error('Update the platform before connecting');}
 if(changedAccount){connections.clear();savedPairings.clear();savedTabs.clear();savedChatTabs.clear();retries.clear();}
 const old = connections.get(c.scope);
 if (old && old !== previous) await old.stop();
 accountPairing = {...pairing,resume:false}; connections.set(c.scope,c); selectedScope = c.scope;
 savedPairings.set(c.scope,{...pairing,scope:c.scope,resume:true});await saveRemembered();updateBadge();
 return state();
}
function createConnection() {
let socket = null;
let workspace = '';
let projectName = '';
let error = '';
let heartbeat;
const clients = new Map([['', {epoch:0, connected:false, discover:false, autoAttach:false}]]);
const clientTabs = new Map();
const releasedClients = new Set();
const createdTabs = new Set();
let serverDiagnostics = false;
const shared = new Map();
const sessions = new Map();
let attachmentSequence = 0;
const groups = new Map();
const recoveries = new Map();
const sessionSettings = new Map();
const setupMethods = new Set(['Page.enable', 'Runtime.enable', 'Network.enable', 'DOM.enable', 'Accessibility.enable', 'Log.enable', 'Console.enable', 'CSS.enable', 'Performance.enable', 'Target.setAutoAttach']);
const diagnostics = [];
const attachedAt = new Map();
const navigatedAt = new Map();
const childFrameLog = new Map();
let lastMethod = '';
let queue = Promise.resolve();


function safeURL(raw) {
  const u = new URL(raw);
  if (u.protocol !== 'https:' && u.protocol !== 'http:' && raw !== 'about:blank') throw new Error('Only HTTP(S) tabs and about:blank can be shared');
  return u.href;
}
function send(message) { if (socket?.readyState === WebSocket.OPEN) socket.send(JSON.stringify(message)); }
function diagnostic(event, tabId = 0, reason = '', requestId = '', method = lastMethod, durationMs = 0, extra = {}) {
  const record = { event, tab_id: tabId, reason, method, request_id: requestId, duration_ms:durationMs, version:chrome.runtime.getManifest().version, at: Date.now(), ...extra };
  diagnostics.push(record); if (diagnostics.length > 256) diagnostics.shift();
  // Fixed lifecycle metadata only: never send CDP arguments, page URLs or tokens.
  console.info('[CHROME_EXTENSION]', JSON.stringify(record));
  // Older platforms reject unknown envelope types, so negotiate this channel.
  if (serverDiagnostics) send({ type: 'diagnostic', ...record });
}
function visibleTabs(clientId = '') { return [...shared.values()].filter(tab => !clientTabs.has(tab.id) || clientTabs.get(tab.id) === clientId); }
function allowedTab(tabId, clientId) { return !clientTabs.has(tabId) || clientTabs.get(tabId) === clientId; }
function claimTab(tab, clientId) {
  if (!clients.has(clientId) || !allowedTab(tab.id, clientId)) throw new Error('Target belongs to another chat');
  if (clientId && !clientTabs.has(tab.id)) {
    clientTabs.set(tab.id, clientId);
    for (const source of sessions.values()) if (source.tabId === tab.id) source.clientId = clientId;
    void announceTabs();
  }
}
function announceTabs() {
  send({type:'tabs', tabs:shared.size, tab_titles:[...shared.values()].map(tab=>tab.title || 'Untitled tab')});
  for (const id of clients.keys()) if (id) send({type:'tabs',client_id:id,tabs:visibleTabs(id).length});
  if(savedPairings.has(api.scope)){savedTabs.set(api.scope,[...shared.keys()]);savedChatTabs.set(api.scope,Object.fromEntries([...shared.keys()].filter(id=>clientTabs.has(id)).map(id=>[id,{clientId:clientTabs.get(id),created:createdTabs.has(id)}])));return saveRemembered();}
  return Promise.resolve();
}
function event(method, params, sessionId, clientId) {
  const source = sessions.get(sessionId || params?.sessionId);
  const targetId = params?.targetId || params?.targetInfo?.targetId;
  const tabId = targetId?.startsWith('tab-') ? Number(targetId.slice(4)) : 0;
  const emit = id => send({type:'cdp', ...(id ? {client_id:id}:{}), message:{method,params,...(sessionId?{sessionId}:{})}});
  if (clientId !== undefined) emit(clientId);
  else if (source) emit(source.clientId || '');
  else if (clientTabs.has(tabId)) emit(clientTabs.get(tabId));
  else for (const [id, client] of clients) if (client.connected || !id) emit(id);
}
function target(tab) { return { targetId: `tab-${tab.id}`, type: 'page', title: tab.title || '', url: tab.url || 'about:blank', attached: sessions.has(`session-${tab.id}`), browserContextId: 'agentworks' }; }
async function tabForTarget(id, clientId) {
  const tab = [...shared.values()].find(t => `tab-${t.id}` === id);
  if (!tab) throw new Error('Target is not shared with this workspace');
  if (clientId !== undefined && (!clients.has(clientId) || !allowedTab(tab.id, clientId))) throw new Error('Target belongs to another chat');
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
    sessions.set(id, { tabId: tab.id, clientId:clientTabs.get(tab.id) || '' });
    attachedAt.set(tab.id, Date.now());
    diagnostic('debugger_attached', tab.id, '', '', lastMethod, 0, { ...tabFacts(tab), chrome: chromeMajor() });
  }
  return id;
}
async function announce(tab) {
  for (const [clientId, client] of clients) {
    if (!client.connected || !allowedTab(tab.id,clientId)) continue;
    if (client.discover) event('Target.targetCreated', {targetInfo:target(tab)},undefined,clientId);
    if (client.autoAttach) {
      claimTab(tab,clientId); const sessionId=await attach(tab);
      event('Target.attachedToTarget',{sessionId,targetInfo:target(tab),waitingForDebugger:false},undefined,clientId);
    }
  }
}
// Groups organize only tabs already authorized for this live connection.
async function groupSharedTab(tabId, connection = socket) {
  if (!connection || socket !== connection || !workspace || !shared.has(tabId)) throw new Error('Connection stopped');
  const tab = await chrome.tabs.get(tabId);
  if (socket !== connection || !workspace || !shared.has(tabId)) throw new Error('Connection stopped');
  const title = projectName || workspace.split('/').pop();
  diagnostic('tab_grouping_started', tabId);
  let groupId;
  try { groupId = await chrome.tabs.group({ ...(groups.has(tab.windowId) ? { groupId: groups.get(tab.windowId) } : { createProperties: { windowId: tab.windowId } }), tabIds: [tabId] }); }
  catch { if (socket !== connection || !shared.has(tabId)) throw new Error('Connection stopped'); groupId = await chrome.tabs.group({ createProperties: { windowId: tab.windowId }, tabIds: [tabId] }); }
  if (socket !== connection || !workspace || !shared.has(tabId)) {
    try { if ((await chrome.tabs.get(tabId)).groupId === groupId) await chrome.tabs.ungroup(tabId); } catch {}
    throw new Error('Connection stopped');
  }
  groups.set(tab.windowId, groupId);
  await chrome.tabGroups.update(groupId, { title, color: 'blue' });
  diagnostic('tab_grouped', tabId);
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
    try { await groupSharedTab(tabId, connection); } catch (e) { diagnostic('tab_grouping_failed', tabId); if (socket !== connection || !workspace) throw e; error = 'Tab shared, but its group could not be created.'; }
    await announce(tab);
    await announceTabs();
  }
  return state();
}
function displayName(value) { return typeof value === 'string' && value.trim() && value.length <= 240 && !/[\x00-\x1f\x7f]/.test(value) ? value.trim() : ''; }
async function updateGroupNames(name, connection = socket) {
  name = displayName(name);
  if (name === projectName) return;
  projectName = name;
  for (const groupId of groups.values()) {
    try {
      const tabs = await chrome.tabs.query({groupId});
      if (socket !== connection || !workspace) return;
      if (tabs.some(tab => shared.has(tab.id) && tabOwners.get(tab.id) === api)) await chrome.tabGroups.update(groupId, {title:projectName || workspace.split('/').pop()});
    } catch { /* A deleted group is recreated only when a tab is shared. */ }
  }
}
async function unshare(tabId, reason = 'requested_unshare') {
  if (!shared.has(tabId)) return;
  const recovery = recoveries.get(tabId); if (recovery) recovery.cancelled = true;
  recoveries.delete(tabId); sessionSettings.delete(tabId);
  diagnostic('tab_unshared', tabId, reason);
  shared.delete(tabId);
  for (const [id, debuggee] of sessions) if (debuggee.tabId === tabId) {
    if (debuggee.capturing) event('Inspector.detached',{reason:'recording_target_unshared'},id,debuggee.clientId || '');
    sessions.delete(id); event('Target.detachedFromTarget', { sessionId: id, targetId: `tab-${tabId}` },undefined,debuggee.clientId || '');
  }
  try { const tab = await chrome.tabs.get(tabId); if (groups.get(tab.windowId) === tab.groupId) await chrome.tabs.ungroup(tabId); } catch {}
  try { await chrome.debugger.detach({ tabId }); } catch {}
  if (tabOwners.get(tabId) === api) tabOwners.delete(tabId);
  event('Target.targetDestroyed', { targetId: `tab-${tabId}` });
  clientTabs.delete(tabId); createdTabs.delete(tabId);
  await announceTabs();
}
async function stop(reason = '') {
  if (socket) diagnostic('connection_stopped');
  const previous = socket; socket = null; workspace = ''; projectName = ''; error = reason;
  clearInterval(heartbeat); clients.clear(); clients.set('',{epoch:0,connected:false,discover:false,autoAttach:false}); clientTabs.clear(); createdTabs.clear();
  serverDiagnostics = false;
  for (const recovery of recoveries.values()) recovery.cancelled = true;
  recoveries.clear(); sessionSettings.clear();
  if (previous) { if (previous.readyState === WebSocket.OPEN) previous.send(JSON.stringify({ type: 'stop' })); previous.close(); }
  const ids = [...tabOwners].filter(([, owner]) => owner === api).map(([id]) => id), previousGroups = new Map(groups); shared.clear(); sessions.clear(); groups.clear();
  await Promise.allSettled(ids.map(async tabId => {
    try { await chrome.debugger.detach({ tabId }); } catch {}
    if (tabOwners.get(tabId) === api) tabOwners.delete(tabId);
    try { const tab = await chrome.tabs.get(tabId); if (previousGroups.get(tab.windowId) === tab.groupId) await chrome.tabs.ungroup(tabId); } catch {}
  }));
  updateBadge();
}
// Chrome can close a renderer's debugging target while its physical tab stays
// open. Keep the explicit grant and logical root session only for that same tab.
// Cancellation, closed tabs and stopped/project-replaced connections revoke it.
async function restoreTarget(tabId, recovery) {
  const allowed = () => !recovery.cancelled && socket === recovery.connection && workspace && shared.has(tabId) && tabOwners.get(tabId) === api;
  const started = Date.now();
  let lastCode = '', attempt = 0;
  diagnostic('target_recovery_started', tabId, '', '', lastMethod, 0, { since_attach_ms: attachedAt.has(tabId) ? started - attachedAt.get(tabId) : 0, since_navigate_ms: navigatedAt.has(tabId) ? started - navigatedAt.get(tabId) : 0 });
  for (const delay of [0, 250, 500, 1000]) {
    if (delay) await new Promise(resolve => setTimeout(resolve, delay));
    if (!allowed()) return;
    let tab;
    attempt++;
    try { tab = await chrome.tabs.get(tabId); safeURL(tab.pendingUrl || tab.url || 'about:blank'); }
    catch (e) { diagnostic('recovery_attempt', tabId, errorCode(e.message) === 'other' ? 'no_tab' : errorCode(e.message), '', lastMethod, 0, { attempt, delay_ms: delay, elapsed_ms: Date.now() - started }); break; }
    diagnostic('recovery_attempt', tabId, '', '', lastMethod, 0, { attempt, delay_ms: delay, elapsed_ms: Date.now() - started, ...tabFacts(tab) });
    if (!allowed()) return;
    // Only child sessions are tied to the dead renderer. The platform's root
    // session/target IDs describe the authorized physical tab and remain stable.
    for (const [id, source] of sessions) if (source.tabId === tabId && source.sessionId) {
      sessions.delete(id); event('Target.detachedFromTarget', {sessionId:id},undefined,source.clientId || '');
    }
    let attached = false;
    try {
      await chrome.debugger.attach({tabId}, '1.3'); attached = true;
      if (!allowed()) throw new Error('Connection stopped');
      for (const [method, params] of sessionSettings.get(tabId) || []) {
        await sendSetupCommand({tabId}, method, params);
        if (!allowed()) throw new Error('Connection stopped');
      }
      if (!allowed()) throw new Error('Connection stopped');
      shared.set(tabId, tab);
      event('Target.targetInfoChanged', {targetInfo:target(tab)});
      diagnostic('target_recovered', tabId, '', '', lastMethod, 0, { attempt, elapsed_ms: Date.now() - started });
      await announceTabs();
      return;
    } catch (e) {
      lastCode = errorCode(e.message);
      diagnostic(attached ? 'target_setup_failed' : 'target_attach_failed', tabId, lastCode, '', lastMethod, 0, { attempt, elapsed_ms: Date.now() - started, ...tabFacts(tab) });
      // Never detach a tab already claimed by a different project.
      if (attached && (!tabOwners.has(tabId) || tabOwners.get(tabId) === api)) {
        try { await chrome.debugger.detach({tabId}); } catch {}
      }
    }
  }
  if (allowed()) { diagnostic('target_recovery_failed', tabId, lastCode, '', lastMethod, 0, { attempt, elapsed_ms: Date.now() - started }); await unshare(tabId, 'debugger_detached'); }
}
// A newly navigating tab can briefly fail Chrome's frame permission check even
// though its main URL is HTTP(S). Retry subscriptions only, on the same grant;
// never suppress the check or retry input, evaluation or navigation actions.
async function sendSetupCommand(source, method, params) {
  const connection = socket;
  const delays = [0, 100, 250, 500, 1000, 2000, 2000, 2000, 2000];
  for (const [index, delay] of delays.entries()) {
    if (delay) await new Promise(resolve => setTimeout(resolve, delay));
    if (!connection || socket !== connection || !workspace || !shared.has(source.tabId) || tabOwners.get(source.tabId) !== api) throw new Error('Session is not shared with this workspace');
    const tab = await chrome.tabs.get(source.tabId); safeURL(tab.pendingUrl || tab.url || 'about:blank');
    try { return await chrome.debugger.sendCommand(source, method, params); }
    catch (e) {
      if (e.message !== 'Cannot access a chrome-extension:// URL of different extension' || index === delays.length - 1) throw e;
      diagnostic('setup_waiting_for_page', source.tabId, 'foreign_frame', '', method, 0, { attempt: index + 1, delay_ms: delay, elapsed_ms: delays.slice(0, index + 1).reduce((a, b) => a + b, 0) });
    }
  }
}
function state() { return { connected: !!workspace && socket?.readyState === WebSocket.OPEN, workspace, name:projectName, server: socket ? new URL(socket.url).host : '', error, diagnostics: [...diagnostics], tabs: [...shared.values()].map(t => ({ id: t.id, title: t.title || t.url })) }; }
async function connect(raw) {
  const pairing = JSON.parse(raw);
  const endpoint = new URL(pairing.url);
  if (endpoint.pathname !== '/api/browser/extension/connect' || endpoint.search || endpoint.hash || endpoint.username || endpoint.password) throw new Error('Invalid pairing connection');
  if (endpoint.protocol !== 'wss:' && !(endpoint.protocol === 'ws:' && ['localhost', '127.0.0.1'].includes(endpoint.hostname))) throw new Error('A secure platform connection is required');
  if (typeof pairing.token !== 'string' || pairing.token.length < 32) throw new Error('Invalid pairing credential');
  await stop();
  return new Promise((resolve, reject) => {
    const ws = new WebSocket(endpoint.href); socket = ws;
    const transientLoss=async(reason)=>{if(socket!==ws)return;await stop(reason);retryLater(pairing.scope);};
    const revoke=()=>{const forget=()=>{const scope=api.scope || pairing.scope, saved=savedPairings.get(scope);if(saved?.token===pairing.token && saved.url===pairing.url)return forgetProject(scope,'Connection stopped. Connect again to enable access.');};popupQueue=popupQueue.then(forget,forget);};
    const timer = setTimeout(() => {void transientLoss('Reconnecting to the platform…');reject(new Error('Connection timed out'));},10000);
    ws.onopen = () => ws.send(JSON.stringify({ type: 'pair', features:['chat-clients'], token: pairing.token, scope: pairing.scope, resume:pairing.resume===true }));
    ws.onmessage = ({ data }) => {
      if (socket !== ws) return;
      let e; try { e = JSON.parse(data); } catch { void stop('Invalid server response'); return; }
      if (e.type === 'paired') {
        clearTimeout(timer); workspace = e.workspace; api.scope = e.scope; api.profile = e.profile_id; if(Array.isArray(e.projects))availableProjects=e.projects; projectName = displayName(e.name); error = '';
        serverDiagnostics = e.diagnostics === true;
        heartbeat = setInterval(() => send({ type: 'ping' }), 25000);
        diagnostic('connection_paired', 0, '', '', '', 0, { chrome: chromeMajor() });
        // Send what happened while the socket was down, oldest first, each with its age.
        if (serverDiagnostics) for (const old of offlineEvents.splice(0)) send({ type: 'diagnostic', ...old, age_ms: Math.max(0, Date.now() - old.at) });
        updateBadge(); resolve(state());
      } else if (e.type === 'connect-project') {
        const previousScope=selectedScope;
        const attachProject=async()=>{if(socket!==ws || !accountPairing)return;try{await connectProject(JSON.stringify({...accountPairing,scope:e.scope}));}finally{if(connections.get(previousScope)?.state().connected)selectedScope=previousScope;await saveRemembered();}};
        popupQueue=popupQueue.then(attachProject,attachProject);
      } else if (e.type === 'target-command') {
        queue=queue.then(async()=>{if(socket!==ws || !workspace)return;try{const result=await command(e.target_id ? {method:'Target.closeTarget',params:{targetId:e.target_id}} : {method:'Target.createTarget',params:{url:e.url}},e.active===true,e.client_id || '');send({type:'target-result',request_id:e.request_id,target_id:e.target_id || result.targetId});}catch(error){send({type:'target-result',request_id:e.request_id,error:error.message});}}).catch(()=>{});
      } else if (e.type === 'projects' || e.type === 'pong') {
        if (Array.isArray(e.projects)) availableProjects = e.projects;
        const project = availableProjects.find(p => p.scope === api.scope);
        if (project) void updateGroupNames(project.name, ws);
      } else if (e.type === 'error') {
        clearTimeout(timer); reject(new Error(e.workspace)); revoke();void stop(e.workspace);
      } else if (e.type === 'cdp') {
        // CDP permits concurrent requests. A stalled renderer evaluation must
        // not block browser-level controls, other tabs or screencast acks.
        // The caller awaits actions that depend on a previous response.
        void handleCDP(e.message, ws, e.active === true, e.client_id || '');
      } else if (e.type === 'client-register') {
        if (typeof e.client_id !== 'string' || !/^[a-f0-9]{16}$/.test(e.client_id)) return;
        releasedClients.delete(e.client_id);
        if (!clients.has(e.client_id)) clients.set(e.client_id,{epoch:0,connected:false,discover:false,autoAttach:false});
        send({type:'client-ready',client_id:e.client_id,tabs:visibleTabs(e.client_id).length});
      } else if (e.type === 'client-release') {
        void releaseClient(e.client_id);
      } else if (e.type === 'client-connected' || e.type === 'client-disconnected') {
        const clientId=e.client_id || '', client=clients.get(clientId);
        if (!client) return;
        client.epoch++; client.connected=e.type==='client-connected';
        if (!client.connected) {
          client.discover=false; client.autoAttach=false;
          for (const [id, source] of sessions) if (source.logical && (source.clientId || '')===clientId) {
            sessions.delete(id);
            if (source.capturing) void chrome.debugger.sendCommand({tabId:source.tabId},'Page.stopScreencast').catch(()=>{});
          }
        }

      }
    };
    ws.onerror = () => { offlineDiagnostic('connect_failed', pairing.scope, 'ws_error', {chrome: chromeMajor(), online: navigator.onLine}); clearTimeout(timer); reject(new Error('Cannot connect to the platform')); };
    ws.onclose = ({code}) => {offlineDiagnostic('connection_closed', pairing.scope, 'ws_closed', {ws_code: Number.isInteger(code) ? code : 0, online: navigator.onLine});clearTimeout(timer);reject(new Error('Connection closed'));if(socket!==ws)return;if(code===4001){revoke();void stop('Connection stopped. Connect again to enable access.');}else void transientLoss('Reconnecting to the platform…');};
  });
}

async function command(message, active = false, clientId = '') {
  const client = clients.get(clientId);
  if (!client) throw new Error('Chat browser client is no longer connected');
  const { method, sessionId, params = {} } = message;
  lastMethod = /^[A-Za-z]+\.[A-Za-z]+$/.test(method) && method.length <= 80 ? method : '';
  if (!sessionId) {
    switch (method) {
      case 'Browser.getVersion': return { protocolVersion: '1.3', product: navigator.userAgent.match(/Chrome\/[^ ]+/)?.[0] || 'Chrome/125.0.0.0', revision: '', userAgent: navigator.userAgent, jsVersion: '' };
      case 'Target.getBrowserContexts': return { browserContextIds: [] };
      case 'Target.getTargets': return { targetInfos: visibleTabs(clientId).map(target) };
      case 'Target.getTargetInfo': return { targetInfo: target(await tabForTarget(params.targetId,clientId)) };
      case 'Target.setDiscoverTargets': client.discover=!!params.discover; if(client.discover) for(const tab of visibleTabs(clientId)) event('Target.targetCreated',{targetInfo:target(tab)},undefined,clientId); return {};
      case 'Target.setAutoAttach': client.autoAttach=!!params.autoAttach; if(client.autoAttach) for(const tab of visibleTabs(clientId)) {claimTab(tab,clientId); const id=await attach(tab); event('Target.attachedToTarget',{sessionId:id,targetInfo:target(tab),waitingForDebugger:false},undefined,clientId);} return {};
      case 'Target.attachToTarget': {
        const tab = await tabForTarget(params.targetId,clientId); claimTab(tab,clientId); await attach(tab);
        if ([...sessions.values()].filter(s => s.tabId === tab.id && s.logical).length >= 16) throw new Error('Too many sessions on this shared tab');
        // A recorder needs a distinct flattened session: sharing the main ID
        // lets its private event receiver consume navigation events instead of
        // the automation client. Both logical sessions retain the same grant.
        const id = `attached-${tab.id}-${++attachmentSequence}`;
        sessions.set(id,{tabId:tab.id,logical:true,clientId});
        return {sessionId:id};
      }
      case 'Target.detachFromTarget': {
        const src = sessions.get(params.sessionId); if (!src || (src.clientId || '')!==clientId || !allowedTab(src.tabId,clientId)) throw new Error('Session is not shared');
        diagnostic('session_detach_requested', src.tabId); sessions.delete(params.sessionId);
        if (src.logical) {
          if (src.capturing) await chrome.debugger.sendCommand({tabId:src.tabId},'Page.stopScreencast');
        } else await chrome.debugger.detach({tabId:src.tabId,...(src.sessionId ? {sessionId:src.sessionId} : {})});
        return {};
      }
      case 'Target.createTarget': {
        if (!workspace || socket?.readyState !== WebSocket.OPEN) throw new Error('Connect first');
        if (shared.size >= 32) throw new Error('Shared tab limit reached');
        const tab = await chrome.tabs.create({ url: safeURL(params.url || 'about:blank'), active });
        claimTab(tab,clientId); createdTabs.add(tab.id);
        diagnostic('tab_created', tab.id);
        try { await attach(tab); shared.set(tab.id, tab); try { await groupSharedTab(tab.id); } catch (e) { diagnostic('tab_grouping_failed', tab.id); if (!workspace || !shared.has(tab.id)) throw e; error = 'Tab shared, but its group could not be created.'; } await announce(tab); await announceTabs(); return { targetId: `tab-${tab.id}` }; }
        catch (e) { await unshare(tab.id); clientTabs.delete(tab.id); createdTabs.delete(tab.id); await chrome.tabs.remove(tab.id); throw e; }
      }
      case 'Target.closeTarget': { const tab = await tabForTarget(params.targetId,clientId); await unshare(tab.id, 'target_close'); await chrome.tabs.remove(tab.id); return { success: true }; }
      case 'Target.activateTarget': { const tab = await tabForTarget(params.targetId,clientId); if (active) await chrome.tabs.update(tab.id, { active: true }); return {}; }
      default: throw new Error(`Unsupported browser operation: ${method}`);
    }
  }
  const source = sessions.get(sessionId);
  if (!source || !shared.has(source.tabId) || (source.clientId || '')!==clientId || !allowedTab(source.tabId,clientId)) throw new Error('Session is not shared with this workspace');
  const recovery = recoveries.get(source.tabId);
  if (recovery) {
    await recovery.promise;
    if (socket !== recovery.connection || !shared.has(source.tabId) || sessions.get(sessionId) !== source) throw new Error('Session is not shared with this workspace');
  }
  // agent-browser also sends Page.bringToFront during logical tab selection.
  if (method === 'Page.bringToFront' && !active) return {};
  // The recorder requests target metadata through its page session. Resolve
  // only that session's already-granted root; never reveal another target.
  if (method === 'Target.getTargetInfo' && !source.sessionId) {
    const tab = shared.get(source.tabId);
    if (params.targetId && params.targetId !== target(tab).targetId) throw new Error('Target is not shared with this session');
    return {targetInfo: target(tab)};
  }
  const domain = method.split('.')[0];
  const allowed = new Set(['Accessibility', 'DOM', 'DOMSnapshot', 'Runtime', 'Page', 'Input', 'CSS', 'Log', 'Console', 'Performance']);
  if (!allowed.has(domain) && !['Network.enable', 'Network.disable', 'Network.getResponseBody', 'Network.setCacheDisabled', 'Network.emulateNetworkConditions', 'Network.setUserAgentOverride', 'Target.setAutoAttach', 'Target.detachFromTarget'].includes(method)) throw new Error(`Unsupported extension operation: ${method}`);
  if (['DOM.setFileInputFiles', 'Page.setDownloadBehavior', 'Page.addScriptToEvaluateOnNewDocument', 'Runtime.addBinding'].includes(method)) throw new Error(`Unsupported extension operation: ${method}`);
  if (method === 'Page.navigate') { safeURL(params.url); navigatedAt.set(source.tabId, Date.now()); childFrameLog.delete(source.tabId); }
  if (method === 'Page.captureScreenshot' && params.clip?.height > 16000) throw new Error('SCREENSHOT_TOO_TALL: full-page screenshots are limited to 16000 px; capture the viewport after scrolling');
  if (method === 'Page.startScreencast' && [...sessions.values()].some(s => s.tabId === source.tabId && s.capturing && s !== source)) throw new Error('This shared tab is already recording');
  const debuggee = {tabId:source.tabId,...(source.sessionId ? {sessionId:source.sessionId} : {})};
  // Chrome does not process input in a tab hidden behind another: the first
  // mouse event waits ~5 s and then reports success, and the click is lost
  // (Upwork, 2026-10-06). The agent cannot see that, so before a click or key
  // the shared tab is made the visible tab of its window. Reads, snapshots and
  // navigation stay in the background (PLAT-516).
  if (domain === 'Input') await showTabForInput(source.tabId);
  const result = await (setupMethods.has(method) && !source.sessionId ? sendSetupCommand(debuggee, method, params) : chrome.debugger.sendCommand(debuggee, method, params)) || {};
  if (method === 'Page.startScreencast') source.capturing = true;
  if (method === 'Page.stopScreencast') source.capturing = false;
  if (!source.sessionId && setupMethods.has(method)) {
    if (!sessionSettings.has(source.tabId)) sessionSettings.set(source.tabId, new Map());
    sessionSettings.get(source.tabId).set(method, params);
  } else if (!source.sessionId && method.endsWith('.disable')) sessionSettings.get(source.tabId)?.delete(method.replace(/\.disable$/, '.enable'));
  return result;
}
async function showTabForInput(tabId) {
  const tab = await chrome.tabs.get(tabId);
  // A minimized window hides every tab in it, so restore it first. A window
  // that is open but behind other apps is left where it is: raising Chrome
  // over the user's work on every click is what PLAT-636 avoids.
  const window = await chrome.windows.get(tab.windowId);
  const restore = window.state === 'minimized';
  if (tab.active && !restore) return;
  if (restore) { await chrome.windows.update(tab.windowId, { state: 'normal' }); diagnostic('window_restored_for_input', tabId); }
  if (!tab.active) { await chrome.tabs.update(tabId, { active: true }); diagnostic('tab_shown_for_input', tabId); }
  // Give the renderer a frame to become visible before the event arrives.
  await new Promise(resolve => setTimeout(resolve, restore ? 300 : 150));
}
async function releaseClient(clientId) {
  if (typeof clientId !== 'string' || !/^[a-f0-9]{16}$/.test(clientId)) return;
  releasedClients.add(clientId); clients.delete(clientId);
  for (const [tabId, owner] of [...clientTabs]) if (owner===clientId) {
    const created=createdTabs.has(tabId);
    await unshare(tabId);
    if(created) {try {await chrome.tabs.remove(tabId);} catch {}}
  }
}

async function handleCDP(message, connection, active, clientId) {
  if (socket !== connection || !workspace) return;
  if (!message || typeof message.id !== 'number' || typeof message.method !== 'string') return;
  lastMethod = /^[A-Za-z]+\.[A-Za-z]+$/.test(message.method) && message.method.length <= 80 ? message.method : '';
  const tabId = sessions.get(message.sessionId)?.tabId || 0, requestId = Number.isSafeInteger(message.id) && message.id >= 0 ? String(message.id) : '';
  const client=clients.get(clientId); if(!client || !client.connected)return;
  const started = Date.now(), method = lastMethod, epoch = client.epoch;
  diagnostic('command_started', tabId, '', requestId, method);
  try { const result = await command(message, active, clientId); diagnostic('command_succeeded', tabId, '', requestId, method, Date.now()-started); if (socket === connection && workspace && clients.get(clientId)===client && client.epoch === epoch && client.connected) send({ type: 'cdp', ...(clientId ? {client_id:clientId}:{}), message: { id: message.id, ...(message.sessionId ? { sessionId: message.sessionId } : {}), result } }); }
  catch (e) { diagnostic('command_failed', tabId, /detached/i.test(e.message) ? 'detached' : errorCode(e.message), requestId, method, Date.now()-started); if (socket === connection && workspace && clients.get(clientId)===client && client.epoch === epoch && client.connected) send({ type: 'cdp', ...(clientId ? {client_id:clientId}:{}), message: { id: message.id, ...(message.sessionId ? { sessionId: message.sessionId } : {}), error: { code: -32000, message: e.message } } }); }
}

  const api = {
    scope: '', profile: '', connect, state, share, unshare, stop,
    async restoreGrant(tabId, metadata) {
      if(metadata && /^[a-f0-9]{16}$/.test(metadata.clientId)) {
        if(releasedClients.has(metadata.clientId)) {if(metadata.created===true)try {await chrome.tabs.remove(tabId);} catch {} return false;}
        clientTabs.set(tabId,metadata.clientId);if(metadata.created===true)createdTabs.add(tabId);
      }
      return true;
    },
    async newtab() { const result = await command({ method:'Target.createTarget', params:{url:'about:blank'} }); const tab = await tabForTarget(result.targetId); await chrome.tabs.update(tab.id,{active:true}); },
    async group() { for (const id of [...shared.keys()]) await groupSharedTab(id); },
    onDetach(tabId, reason) {
      const now = Date.now();
      diagnostic('debugger_detached', tabId, ['target_closed','canceled_by_user','replaced_with_devtools'].includes(reason) ? reason : 'other', '', lastMethod, 0, {
        since_attach_ms: attachedAt.has(tabId) ? now - attachedAt.get(tabId) : 0, since_navigate_ms: navigatedAt.has(tabId) ? now - navigatedAt.get(tabId) : 0 });
      void chrome.tabs.get(tabId).then(tab => diagnostic('tab_snapshot', tabId, '', '', lastMethod, 0, tabFacts(tab)), () => diagnostic('tab_snapshot', tabId, 'no_tab'));
      // Recovery restores automation subscriptions, never a recording take.
      // Tell its private receiver to fail rather than repeat an old frame.
      for (const [id, source] of sessions) if (source.tabId === tabId && source.capturing) {
        event('Inspector.detached',{reason:'recording_target_detached'},id,source.clientId || '');
        source.capturing = false;
      }
      if (reason !== 'target_closed') { void unshare(tabId, 'debugger_detached'); return; }
      if (!shared.has(tabId) || recoveries.has(tabId)) return;
      const recovery = {connection:socket, cancelled:false, promise:null};
      recoveries.set(tabId, recovery);
      recovery.promise = restoreTarget(tabId, recovery).finally(() => { if (recoveries.get(tabId) === recovery) recoveries.delete(tabId); });
    },
    onRemoved(tabId) { attachedAt.delete(tabId); navigatedAt.delete(tabId); childFrameLog.delete(tabId); void unshare(tabId, 'tab_closed'); },
    onEvent(source,method,params) {
      if (!shared.has(source.tabId)) return;
      const id = source.sessionId || `session-${source.tabId}`;
      if (method === 'Target.attachedToTarget') {
        const info = params?.targetInfo || {}, facts = frameFacts(info.url || '');
        const types = ['iframe', 'page', 'worker', 'service_worker', 'shared_worker', 'background_page', 'other'];
        diagnostic('child_attached', source.tabId, '', '', lastMethod, 0, { target_type: types.includes(info.type) ? info.type : 'other', scheme: facts.scheme, ext_ids: facts.ext_ids, since_navigate_ms: navigatedAt.has(source.tabId) ? Date.now() - navigatedAt.get(source.tabId) : 0 });
        sessions.set(params.sessionId,{tabId:source.tabId,sessionId:params.sessionId,clientId:clientTabs.get(source.tabId) || ''});
      }
      if (method === 'Target.detachedFromTarget') { diagnostic('child_detached', source.tabId); sessions.delete(params.sessionId); }
      // Frames the page itself creates: type and scheme only, never the address. At most 24 per navigation per tab.
      if ((method === 'Page.frameAttached' || method === 'Page.frameNavigated') && (method === 'Page.frameAttached' || params?.frame?.parentId)) {
        const seen = childFrameLog.get(source.tabId) || 0;
        if (seen < 24) {
          childFrameLog.set(source.tabId, seen + 1);
          const facts = method === 'Page.frameNavigated' ? frameFacts(params.frame.url || '') : { scheme: '', ext_ids: [] };
          diagnostic(method === 'Page.frameAttached' ? 'frame_attached' : 'frame_navigated', source.tabId, '', '', lastMethod, 0, { scheme: facts.scheme, ext_ids: facts.ext_ids, since_navigate_ms: navigatedAt.has(source.tabId) ? Date.now() - navigatedAt.get(source.tabId) : 0 });
        }
      }
      if (!source.sessionId) {
        // Chrome supplies one physical debugger session per tab. Fan out page
        // lifecycle events to distinct logical clients, but send video frames
        // only to the session that started capture.
        for (const [logicalId, dst] of sessions) if (dst.tabId === source.tabId && !dst.sessionId) {
          if (method === 'Page.screencastFrame' && !dst.capturing) continue;
          event(method,params || {},logicalId,dst.clientId || '');
        }
      } else event(method,params || {},id);
    },
    onUpdated(tabId,change,tab) {
      if (!shared.has(tabId)) return;
      try { safeURL(tab.pendingUrl || tab.url || 'about:blank'); shared.set(tabId,tab); if(change.url || change.title) {event('Target.targetInfoChanged',{targetInfo:target(tab)});announceTabs();} }
      catch { void unshare(tabId, 'unsupported_url'); }
    }
  };
  return api;
}

chrome.debugger.onEvent.addListener((source,method,params)=>tabOwners.get(source.tabId)?.onEvent(source,method,params));
chrome.debugger.onDetach.addListener((source,reason)=>tabOwners.get(source.tabId)?.onDetach(source.tabId,reason));
chrome.tabs.onRemoved.addListener(tabId=>tabOwners.get(tabId)?.onRemoved(tabId));
chrome.tabs.onUpdated.addListener((tabId,change,tab)=>tabOwners.get(tabId)?.onUpdated(tabId,change,tab));
chrome.runtime.onMessage.addListener((request,sender,respond)=>{
 if(sender.id !== chrome.runtime.id) return;
 const run=async()=>{
  await ready;
  if(request.action==='state')return state();
  if(request.action==='connect') {
     await connectProject(request.pairing);
   savedTabs.set(selectedScope,[]);await saveRemembered();
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
   selectedScope=request.scope;await saveRemembered();return state();
  }
  if(request.action==='stop-all') {
   savedPairings.clear();savedTabs.clear();savedChatTabs.clear();retries.clear();clearTimeout(retryTimer);
   await saveRemembered();await Promise.allSettled([...connections.values()].map(c=>c.stop()));connections.clear();accountPairing=null;availableProjects=[];selectedScope='';return state();
  }
  if(request.action==='stop'){await forgetProject(selectedScope);return state();}
  const c=connections.get(selectedScope);if(!c)throw new Error('Browser is reconnecting. Wait until connected.');
  if(request.action==='share')await c.share(request.tabId);
  else if(request.action==='newtab')await c.newtab();
  else if(request.action==='group')await c.group();
  else if(request.action==='unshare')await c.unshare(request.tabId);
  else throw new Error('Unknown action');
  return state();
 };
 // Popup mutations serialize so two Share clicks cannot claim one tab twice.
 if(request.action==='state'){void ready.then(()=>{respond({ok:true,...state()});if([...savedPairings.keys()].some(scope=>!connections.get(scope)?.state().connected))queueResume();},e=>respond({ok:false,error:e.message}));return true;}
 popupQueue=popupQueue.then(run,run);
 popupQueue.then(value=>respond({ok:true,...value}),e=>respond({ok:false,error:e.message}));return true;
});

const ready=initialize();
void ready.then(queueResume).catch(()=>{storageError='Could not restore the remembered browser connection.';});
chrome.alarms.onAlarm.addListener(alarm=>{if(alarm.name===retryAlarm)queueResume();});
chrome.runtime.onStartup.addListener(queueResume);
