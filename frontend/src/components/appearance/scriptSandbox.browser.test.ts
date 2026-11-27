// @vitest-environment node
// #2844: a real Chromium boundary check, not a DOM emulator. The build host may
// set APPEARANCE_TEST_CHROME when its browser isn't on PATH. No app/account used.
import { spawn, spawnSync } from 'node:child_process'
import { createServer } from 'node:http'
import { mkdtemp, readFile, rm } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import { dirname, join, relative } from 'node:path'
import ts from 'typescript'
import { expect, it } from 'vitest'
const chrome=process.env.APPEARANCE_TEST_CHROME || ['google-chrome','google-chrome-stable','chromium','chromium-browser'].map(name=>spawnSync('which',[name],{encoding:'utf8'}).stdout?.trim()).find(Boolean)
it.skipIf(!chrome)('keeps actual Worker scripts opaque/offline and kills an infinite render loop',async()=>{
 // Bundle the sandbox and its relative imports (e.g. randomUUID) into one page script.
 const modules=new Map<string,string>()
 const load=async(file:string)=>{
  if(modules.has(file))return
  const out=ts.transpileModule(await readFile(file,'utf8'),{compilerOptions:{target:ts.ScriptTarget.ES2022,module:ts.ModuleKind.CommonJS}}).outputText
  const deps:string[]=[]
  modules.set(file,out.replace(/require\("(\.[^"]+)"\)/g,(_m,spec:string)=>{const dep=relative('.',join(dirname(file),spec))+'.ts';deps.push(dep);return `require(${JSON.stringify(dep)})`}))
  for(const dep of deps)await load(dep)
 }
 const entry='src/components/appearance/scriptSandbox.ts'
 await load(entry)
 const js=`(()=>{const defs={${[...modules].map(([file,code])=>`${JSON.stringify(file)}:function(exports,require){${code}\n}`).join(',')}};const cache={};const require=name=>{if(!cache[name]){cache[name]={};defs[name](cache[name],require)}return cache[name]};globalThis.mount=require(${JSON.stringify(entry)}).mountScriptSandbox})();`
 let leaks=0
 const server=createServer((req,res)=>{
  if(req.url?.startsWith('/leak'))leaks++
  if(req.url==='/runtime.js'){
   res.setHeader('Content-Type','application/javascript');res.end(js);return
  }
  res.setHeader('Content-Type','text/html')
  res.end(`<div id="host"></div><script>globalThis.statuses=[];localStorage.setItem('private-marker','not-for-theme');</script><script src="/runtime.js"></script>`)
 })
 await new Promise<void>(resolve=>server.listen(0,'127.0.0.1',resolve))
 const address=server.address() as {port:number},origin=`http://127.0.0.1:${address.port}`
 const dir=await mkdtemp(join(tmpdir(),'appearance-browser-'))
 const browser=spawn(chrome!,['--headless=new','--no-sandbox','--disable-dev-shm-usage','--remote-debugging-port=0',`--user-data-dir=${dir}`,'about:blank'],{stdio:['ignore','ignore','pipe']})
 let ws:WebSocket|undefined
 try {
  const endpoint=await new Promise<string>((resolve,reject)=>{
   let output='';const timeout=setTimeout(()=>reject(new Error('Chromium did not start: '+output.slice(-2000))),15000)
   browser.stderr!.on('data',chunk=>{output+=String(chunk);const m=output.match(/DevTools listening on (ws:\/\/[^\s]+)/);if(m){clearTimeout(timeout);resolve(m[1])}})
   browser.on('error',error=>{clearTimeout(timeout);reject(error)})
   browser.once('exit',()=>{clearTimeout(timeout);reject(new Error('Chromium exited before startup: '+output.slice(-2000)))})
  })
  const port=new URL(endpoint).port
  const target=await fetch(`http://127.0.0.1:${port}/json/new?about:blank`,{method:'PUT'}).then(r=>r.json()) as {webSocketDebuggerUrl:string}
  ws=new WebSocket(target.webSocketDebuggerUrl)
  await new Promise<void>((resolve,reject)=>{ws!.addEventListener('open',()=>resolve());ws!.addEventListener('error',reject)})
  let seq=0
  const requests=new Map<number,{resolve:(v:any)=>void;reject:(e:Error)=>void}>()
  ws.addEventListener('message',event=>{const reply=JSON.parse(String(event.data));if(reply.id){const request=requests.get(reply.id);requests.delete(reply.id);if(reply.error)request?.reject(new Error(JSON.stringify(reply.error)));else request?.resolve(reply.result)}})
  const command=(method:string,params:object)=>new Promise<any>((resolve,reject)=>{const id=++seq;requests.set(id,{resolve,reject});ws!.send(JSON.stringify({id,method,params}))})
  const evaluate=async(expression:string)=>{const r=await command('Runtime.evaluate',{expression,awaitPromise:true,returnByValue:true});if(r.exceptionDetails)throw new Error(JSON.stringify(r.exceptionDetails));return r.result.value}
  const waitFor=async(expression:string)=>{for(let i=0;i<80;i++){if(await evaluate(expression))return;await new Promise(r=>setTimeout(r,100))}throw new Error(`Browser condition failed: ${expression}; ${await evaluate('JSON.stringify(globalThis.statuses??[])')}`)}
  await command('Page.navigate',{url:origin})
  await command('Page.bringToFront',{})
  await waitFor('typeof mount === "function"')
  const probe=`async function render({canvas}){
   if(typeof document!=='undefined'||typeof window!=='undefined'||typeof require!=='undefined'||typeof process!=='undefined')throw Error('authority');
   let blocked=false;try{await fetch(${JSON.stringify(origin+'/leak-fetch')});}catch{blocked=true;}if(!blocked)throw Error('fetch');
   blocked=false;try{importScripts(${JSON.stringify(origin+'/leak-import')});}catch{blocked=true;}if(!blocked)throw Error('import');
   const text='</script><img src="${origin}/leak-dom">';
   const ctx=canvas.getContext('2d');ctx.fillStyle='#334455';ctx.fillRect(0,0,64,64);
  }`
  await evaluate(`globalThis.engine=mount(document.querySelector('#host'),{code:${JSON.stringify(probe)},fps:10},()=>({colors:{accent:'#abcdef'},working:true}),s=>statuses.push(s));`)
  await waitFor('statuses.includes("ready")')
  expect(leaks).toBe(0)
  await new Promise(r=>setTimeout(r,250))
  expect(await evaluate('statuses.filter(s=>s==="ready").length')).toBe(1)
  expect(await evaluate(`document.querySelector('iframe').getAttribute('sandbox')`)).toBe('allow-scripts')
  await evaluate(`engine.destroy();statuses=[];engine=mount(document.querySelector('#host'),{code:'function render(){while(true){}}',fps:20},()=>({colors:{},working:false}),s=>statuses.push(s));`)
  await waitFor('statuses.includes("stopped")')
  expect(await evaluate('localStorage.getItem("private-marker")')).toBe('not-for-theme')
  await evaluate('engine.destroy()')
  expect(await evaluate('document.querySelectorAll("iframe").length')).toBe(0)
  // Reduced motion retains one frame and never schedules another.
  await command('Emulation.setEmulatedMedia',{features:[{name:'prefers-reduced-motion',value:'reduce'}]})
  await evaluate(`statuses=[];engine=mount(document.querySelector('#host'),{code:'function render({canvas,reducedMotion}){if(!reducedMotion)throw Error();canvas.getContext("2d").fillRect(0,0,2,2)}',fps:30},()=>({colors:{},working:false}),s=>statuses.push(s));`)
  await waitFor('statuses.includes("ready")')
  await new Promise(r=>setTimeout(r,300))
  expect(await evaluate('statuses.filter(s=>s==="ready").length')).toBe(1)
  await evaluate('engine.destroy()')
  console.log('VERIFY-METRIC: real Chromium opaque/offline Worker, stalled-loop termination, reduced-motion still frame: PASS')
 }finally{
  ws?.close();browser.kill();await new Promise<void>(resolve=>{if(browser.exitCode!==null)resolve();else{browser.once('exit',()=>resolve());setTimeout(resolve,3000)}})
  await new Promise<void>(resolve=>server.close(()=>resolve()));await rm(dir,{recursive:true,force:true})
 }
},30000)
