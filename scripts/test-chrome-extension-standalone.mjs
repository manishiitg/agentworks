// Exercise the extension without Playwright or a second Chrome debugger client.
import { spawn, execFile } from 'node:child_process';
import { promisify } from 'node:util';
import { createServer } from 'node:http';
import { generateKeyPairSync, createHash, randomBytes } from 'node:crypto';
import { mkdtemp, cp, readFile, writeFile, rm } from 'node:fs/promises';
import { tmpdir } from 'node:os';
import path from 'node:path';
import { fileURLToPath } from 'node:url';
import assert from 'node:assert/strict';

const root = path.resolve(path.dirname(fileURLToPath(import.meta.url)), '..');
const base = process.env.CHROME_EXTENSION_E2E_URL;
const executablePath = process.env.CHROME_EXTENSION_E2E_CHROME;
assert.ok(base && executablePath, 'fixture URL and standalone Chrome executable required');
const profile = await mkdtemp(path.join(tmpdir(), 'agentworks-extension-standalone-'));
const extension = path.join(profile, 'extension');
const nonce = randomBytes(16).toString('hex');
let chrome;
let resolveReport;
const report = new Promise(resolve => { resolveReport = resolve; });
const server = createServer((request, response) => {
  if (request.method !== 'POST' || request.url !== `/${nonce}`) { response.writeHead(404).end(); return; }
  let body = '';
  request.on('data', chunk => { body += chunk; if (body.length > 65536) request.destroy(); });
  request.on('end', () => {
    try { resolveReport(JSON.parse(body)); response.writeHead(200).end(); }
    catch { response.writeHead(400).end(); }
  });
});
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve));
let timer;
try {
  await cp(path.join(root, 'extensions/agentworks-chrome'), extension, { recursive: true });
  const manifest = JSON.parse(await readFile(path.join(extension, 'manifest.json'), 'utf8'));
  const { publicKey } = generateKeyPairSync('rsa', { modulusLength: 2048 });
  const key = publicKey.export({ format: 'der', type: 'spki' });
  manifest.key = key.toString('base64');
  manifest.host_permissions = ['http://127.0.0.1/*']; // Throwaway fixture only.
  await writeFile(path.join(extension, 'manifest.json'), JSON.stringify(manifest));
  const id = [...createHash('sha256').update(key).digest().subarray(0, 16)].flatMap(byte => [byte >> 4, byte & 15]).map(n => String.fromCharCode(97 + n)).join('');
  const config = {
    base,
    pairing: { url: base.replace('http', 'ws') + '/api/browser/extension/connect', token: process.env.CHROME_EXTENSION_E2E_TOKEN, scope: process.env.CHROME_EXTENSION_E2E_SCOPE },
    report: `http://127.0.0.1:${server.address().port}/${nonce}`,
    urls: process.env.CHROME_EXTENSION_DIAG_URL ? [process.env.CHROME_EXTENSION_DIAG_URL] : [`${base}/fixture`],
  };
  await writeFile(path.join(extension, 'standalone.html'), '<!doctype html><title>Extension lifecycle test</title><h1>Standalone extension test</h1><script src="standalone.js" type="module"></script>');
  await writeFile(path.join(extension, 'standalone.js'), `
const config = ${JSON.stringify(config)};
const checkpoints = [];
const message = payload => chrome.runtime.sendMessage(payload);
async function tool(command, args=[]) {
  const response = await fetch(config.base+'/fixture/tool', {method:'POST',body:JSON.stringify({command,args})});
  const output = await response.text();
  if(!response.ok) throw new Error(output);
  return JSON.parse(output);
}
try {
  const connected = await message({action:'connect',pairing:JSON.stringify(config.pairing)});
  if(!connected.ok) throw new Error(connected.error);
  for(const url of config.urls) {
    await tool('open',[url]);
    for(let i=0;i<4;i++) {
      await tool('snapshot',['-i']);
      await tool('get',['url']);
      const state=await message({action:'state'});
      checkpoints.push({connected:state.connected,tabs:state.tabs.length});
      if(!state.connected || state.tabs.length!==1) throw new Error('Shared tab lost');
      await new Promise(resolve=>setTimeout(resolve,10000));
    }
  }
  const state = await message({action:'state'});
  await fetch(config.report,{method:'POST',body:JSON.stringify({ok:true,checkpoints,diagnostics:state.diagnostics})});
} catch(error) {
  const state = await message({action:'state'});
  await fetch(config.report,{method:'POST',body:JSON.stringify({ok:false,error:error.message,checkpoints,diagnostics:state.diagnostics})});
}
`);
  // Intentionally omit all --remote-debugging-* flags. Only the extension attaches.
  chrome = spawn(executablePath, [`--user-data-dir=${profile}`, '--no-first-run', '--no-default-browser-check', `--disable-extensions-except=${extension}`, `--load-extension=${extension}`, `chrome-extension://${id}/standalone.html`], { stdio: 'ignore' });
  const result = await Promise.race([report, new Promise((_, reject) => { timer=setTimeout(() => reject(new Error('Standalone Chrome did not report within 90 seconds')), 90000); })]);
  console.log('STANDALONE RESULT', JSON.stringify(result));
  assert.equal(result.ok, true, result.error);
  console.log('PASS standalone Chrome retains its shared tab through navigation and idle periods without an external debugger');
} finally {
  clearTimeout(timer);
  if (chrome && chrome.exitCode === null) { chrome.kill('SIGTERM'); await new Promise(resolve => chrome.once('exit', resolve)); }
  server.close();
  const connection=await fetch(`${base}/fixture/cdp`).then(r=>r.ok?r.json():null).catch(()=>null);
  if(connection)await promisify(execFile)('agent-browser',['--session',connection.session,'close'],{timeout:10000}).catch(()=>{});
  await rm(profile, { recursive: true, force: true });
}
