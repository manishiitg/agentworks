const { test } = require('node:test');
const assert = require('node:assert/strict');
const fs = require('node:fs');
const os = require('node:os');
const path = require('node:path');
const { createNativeAutoSetup, installBundledCLI, installNativeSetupIPC } = require('./nativeAutoSetup');

function fixture(t) {
 const home = fs.mkdtempSync(path.join(os.tmpdir(), 'native-setup-'));
 t.after(() => fs.rmSync(home, { recursive: true, force: true }));
 const bin = path.join(home, 'AgentWorks.app/Contents/Resources/bin/agentworks');
 fs.mkdirSync(path.dirname(bin), { recursive: true }); fs.writeFileSync(bin, 'bundled'); fs.chmodSync(bin, 0o700);
 return { home, bin, stateFile: path.join(home, 'state/auto.json') };
}

test('#2867: update runs both setups once, notice survives a remount, Turn off persists across releases', async t => {
 const files = fixture(t); const calls = []; const logs = [];
 let failRemoval = false;
 const config = { ...files, version:'1', log:line=>logs.push(line), run: async (_bin,args) => {
  calls.push(args);
  return {stdout:JSON.stringify({apps:['claude','codex'].map(provider=>({provider,outcome:args.includes('--disable') ? (failRemoval?'failed':'removed'):'installed',cleanup: provider==='claude' ? [{removed:'agentworks-tools@agentworks',kept:'agentworks@agentworks',scope:'user',outcome:'removed'},{removed:'private-provider-output',kept:'agentworks@agentworks',scope:'user',outcome:'removed'}] : []}))})};
 }};
 let setup = createNativeAutoSetup(config);
 await setup.start(); await setup.start();
 assert.equal(calls.length,1);
 assert.equal(await setup.notice(),true); assert.equal(await setup.notice(),true);
 setup.dismissNotice(); assert.equal(await setup.notice(),false);
 setup = createNativeAutoSetup(config); await setup.start(); assert.equal(calls.length,1);
 setup = createNativeAutoSetup({...config,version:'2'}); await setup.start(); assert.equal(calls.length,2);
 failRemoval=true; assert.equal((await setup.turnOff()).ok,false);
 failRemoval=false; setup = createNativeAutoSetup({...config,version:'3'}); await setup.start();
 assert.deepEqual(calls.at(-1),['setup','desktop','--disable']);
 assert.equal(await setup.notice(),false);
 const count=calls.length; setup = createNativeAutoSetup({...config,version:'4'}); await setup.start(); assert.equal(calls.length,count);
 assert.ok(logs.some(s=>s==='[NATIVE_CONNECT] event=auto_setup outcome=installed provider=codex'));
 assert.ok(logs.some(s=>s==='[NATIVE_CONNECT] event=plugin_cleanup removed=agentworks-tools@agentworks kept=agentworks@agentworks scope=user outcome=removed'));
 assert.ok(!logs.some(s=>s.includes('private-provider-output')));
});

test('#2867: CLI update preserves unknown programs; IPC refuses dashboard frames and arbitrary windows', async t => {
 const files = fixture(t); const target=path.join(files.home,'.local/bin/agentworks');
 assert.equal(installBundledCLI(files.bin,files.home),'installed');
 fs.unlinkSync(target); fs.writeFileSync(target,'unrelated');
 assert.equal(installBundledCLI(files.bin,files.home),'kept_existing'); assert.equal(fs.readFileSync(target,'utf8'),'unrelated');
 const handlers={}; let called=0; const frame={url:'http://localhost:123/crew'}; const contents={mainFrame:frame};
 installNativeSetupIPC({ipcMain:{handle:(name,fn)=>handlers[name]=fn},controller:{turnOff:()=>{called++},notice:()=>true,dismissNotice:()=>{}},getWindow:()=>({webContents:contents}),getOrigin:()=> 'http://localhost:123'});
 const off=handlers['native-setup-off'];
 await assert.rejects(off({sender:contents,senderFrame:{url:frame.url}}));
 await assert.rejects(off({sender:{},senderFrame:frame})); assert.equal(called,0);
 await off({sender:contents,senderFrame:frame}); assert.equal(called,1);
 fs.mkdirSync(path.dirname(files.stateFile),{recursive:true});fs.writeFileSync(files.stateFile,'broken');
 assert.throws(()=>createNativeAutoSetup({...files,version:'1'}));
});
