// The setup tab chooses the deployment; fragments carry only a one-use handoff.
export function setupConnection(raw) {
 const url = new URL(raw);
 if (url.username || url.password || url.pathname !== '/api/browser/extension/setup' || url.search || !(url.protocol === 'https:' || url.protocol === 'http:' && ['localhost','127.0.0.1'].includes(url.hostname))) throw new Error('In AgentWorks, choose Connect browser and keep its setup tab open.');
 const handoff = new URLSearchParams(url.hash.slice(1)).get('handoff');
 if (!/^[A-Za-z0-9_-]{43}$/.test(handoff || '')) throw new Error('Open a new connection from AgentWorks.');
 return { origin:url.origin, handoff, url:url.href, permission:`${url.origin}/*` };
}

export async function exchangePairing(setup, action, fetcher = fetch) {
 const response = await fetcher(`${setup.origin}/api/browser/extension/pairing/${action}`, {method:'POST',credentials:'omit',redirect:'error',headers:{'Content-Type':'application/json'},body:JSON.stringify({handoff:setup.handoff})});
 if (!response.ok) throw new Error(response.status === 410 ? 'This connection link expired or was already used. In AgentWorks, choose Connect browser again.' : 'Could not connect. Open a new connection from AgentWorks.');
 return response.json();
}
