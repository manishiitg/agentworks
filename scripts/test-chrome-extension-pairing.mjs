// PLAT-746-282: the setup tab's origin is the only pairing destination.
import assert from 'node:assert/strict';
import { setupConnection, exchangePairing } from '../extensions/agentworks-chrome/pairing.js';
const handoff = 'a'.repeat(43);
const setup = setupConnection(`https://example.com/api/browser/extension/setup#handoff=${handoff}`);
assert.equal(setup.origin, 'https://example.com');
for (const raw of [
 `http://example.com/api/browser/extension/setup#handoff=${handoff}`,
 `https://user:pass@example.com/api/browser/extension/setup#handoff=${handoff}`,
 `https://example.com/other#handoff=${handoff}`,
 `https://example.com/api/browser/extension/setup?server=https://other.example.com#handoff=${handoff}`,
 'https://example.com/api/browser/extension/setup#handoff=short',
]) assert.throws(() => setupConnection(raw));
for (const host of ['localhost','127.0.0.1']) assert.equal(setupConnection(`http://${host}:8123/api/browser/extension/setup#handoff=${handoff}`).origin,`http://${host}:8123`);
let seen;
const identity = await exchangePairing(setup,'peek',async (url, options)=>{seen={url,options};return {ok:true,json:async()=>({account:'owner@example.com',account_id:'owner'})};});
assert.equal(identity.account,'owner@example.com');
assert.equal(seen.url,'https://example.com/api/browser/extension/pairing/peek');
assert.equal(seen.options.redirect,'error');
assert.equal(seen.options.credentials,'omit');
assert.deepEqual(JSON.parse(seen.options.body),{handoff});
await assert.rejects(exchangePairing(setup,'redeem',async()=>({ok:false,status:410})),/expired or was already used/);
console.log('PASS setup origin, HTTPS/loopback, URL credential/path rejection and expired handoff errors');
