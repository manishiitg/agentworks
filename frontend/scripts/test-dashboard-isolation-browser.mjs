// PLAT-750-24: real opaque documents and the production website/preview runtimes.
// Backend responses and native conversation resolution are fixtures, not live LLM proof.
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { readFile, mkdir, mkdtemp } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { createServer } from 'vite'
import react from '@vitejs/plugin-react'
import { chromium } from 'playwright'

execFileSync('npm', ['run', 'build:report-preview'], { stdio: 'inherit' })
const runtime = await readFile('dist/report-preview.js', 'utf8')
const literal = value => JSON.stringify(value).replaceAll('<', '\\u003c')
const html = `<!doctype html><html><head><title>Scoped dashboard</title><style>body{margin:0}#panel{width:70%;height:220px;margin-left:15%}button{padding:12px}#total{height:80px}</style></head><body>
<h1>Sample report</h1><p id="total">Loading</p><div id="panel" data-report-chat-panel></div>
<button data-report-chat-button>Ask</button><button data-report-action="sample_read">Read sample</button>
<button id="python">Run Python</button><button id="message">Send message</button><button id="write">Save value</button><button id="file">Load file</button><button id="escape">Escape</button><div id="result"></div><a href="other.html">Other dashboard</a><a href="javascript:window.compromised=true">Unsafe link</a>
<script>
window.probe={messageLeak:false};addEventListener('message',event=>{try{if(JSON.stringify(event.data).includes(['PREVIEW','ONLY','FIXTURE'].join('_')))probe.messageLeak=true}catch{}});for(const [name,fn] of Object.entries({parentDOM:()=>parent.document.body.innerHTML,parentStorage:()=>parent.localStorage.getItem('private'),cookies:()=>document.cookie})){try{probe[name]=fn()}catch{probe[name]='denied'}}
probe.urls=[location.href,document.baseURI,document.referrer];
document.querySelector('#escape').onclick=()=>{try{probe.popup=window.open(location.href)===null?'blocked':'opened'}catch{probe.popup='blocked'}try{top.location.href=location.href;probe.top='requested'}catch{probe.top='blocked'}};
window.report.ready(async()=>{document.querySelector('#total').textContent='Total '+(await report.query('select 42 as n'))[0].n;
window.player=report.motion.create({id:'history',duration:2,render:s=>document.body.dataset.clock=String(s.time)});
document.querySelector('#python').onclick=async()=>document.querySelector('#result').textContent=(await report.run('code/sample.py',{})).marker;
document.querySelector('#message').onclick=async()=>document.querySelector('#result').textContent=(await report.sendChatMessage('Explain 42')).status;
document.querySelector('#write').onclick=async()=>{await report.updateFields('items',{id:1},{status:'checked'});document.querySelector('#result').textContent='saved'};
document.querySelector('#file').onclick=async()=>{probe.file=await report.fileUrl('db/sample.html');document.querySelector('#result').textContent=await(await fetch(probe.file)).text()};
});</script></body></html>`
const fixture = { name: 'isolated-dashboard', configureServer(server) {
  server.middlewares.use(async (req, res, next) => {
    const url = new URL(req.url, 'http://localhost')
    if (url.pathname === '/report-preview.js' || url.pathname === '/report-preview/report-preview.js') { res.setHeader('Content-Type','text/javascript'); res.end(runtime); return }
    if (url.pathname === '/report-preview/') { res.setHeader('Content-Type','text/html'); res.end('<!doctype html><html><body><script src="/report-preview/report-preview.js"></script></body></html>'); return }
    if (url.pathname.startsWith('/api/workflow/report-preview/')) {
      if(url.pathname.endsWith('/file')){res.setHeader('Content-Type','text/html');res.end(url.searchParams.get('path')==='db/sample.html'?'FILE_BYTES':html+(url.searchParams.get('path')==='db/reports/large.html'?'<!--'+'x'.repeat(5*1024*1024)+'-->':''));return}
      res.setHeader('Content-Type','application/json');res.end('{"success":true,"data":{"rows":[{"n":42}]}}');return
    }
    if (!['/__native','/__html','/__large','/__large-supported'].includes(url.pathname)) return next()
    res.setHeader('Content-Type','text/html')
    const content = url.pathname === '/__html' ? html.replace(/window.report.ready\(async\(\)=>\{[\s\S]*?\}\);<\/script>/, '</script>') : html + (url.pathname.startsWith('/__large')?'<!--'+'x'.repeat(url.pathname==='/__large'?5*1024*1024:1_450_000)+'-->':'')
    res.end(await server.transformIndexHtml(url.pathname, `<!doctype html><style>body{margin:0}#root{margin:30px;position:relative;overflow:hidden;width:calc(100% - 60px)}iframe{width:100%;height:600px;border:0}#private{height:20px}</style><div id="private">Private app text <input id="outside" aria-label="Other field"></div><div id="root"></div><script type="module">
      import React from 'react'; import {createRoot} from 'react-dom/client';
      import {HtmlReportFrame} from '/src/components/workflow/reportWidgets/HtmlWidgetFrame.tsx';
      import {HtmlRenderer} from '/src/components/ui/HtmlRenderer.tsx';
      import {ReportEmbedProvider} from '/src/components/workflow/reportWidgets/reportEmbedContext.tsx';
      import {installDashboardWidgetHost} from '/src/components/workflow/reportWidgets/dashboardWidgetHost.ts';
      import {useSelectedReportDocument} from '/src/components/workflow/reportDocuments.ts';
      localStorage.setItem('private','PRIVATE_STORAGE');document.cookie='session=PRIVATE_COOKIE;SameSite=Lax';window.calls=[];window.slots=[];window.opens=0;window.popup=false;
      const host=document.querySelector('#root');const data={workspacePath:'Crew/sample',query:async()=>{calls.push('query');return[{n:42}]},get:async()=>({n:42}),getText:async()=>'',getHtml:async()=>'',renderMarkdown:s=>s,fileUrl:async()=>URL.createObjectURL(new Blob(['FILE_BYTES'],{type:'text/html'})),mediaUrl:async()=>'',openFile:()=>{},run:async()=>{calls.push('run');return{marker:'PYTHON_OK'}},sendChatMessage:async()=>{calls.push('send');return{status:'queued',tabId:'caller-chat'}},updateField:async()=>{},updateFields:async()=>{calls.push('write')}};
      const chatWidgets=(frame,bridge)=>{window.channel=bridge;window.frame=frame;const binding=installDashboardWidgetHost(frame,bridge,{container:()=>host,available:true,functionsAvailable:true,openConversation:async()=>{window.opens++;window.openedAt=Date.now()},openAction:action=>{calls.push('action:'+action.name)},popup:open=>{window.popup=open},error:message=>{throw Error(message)},changed:slots=>{window.slots=slots;if(!slots.length){host.querySelector('[data-native]')?.remove();return}for(const slot of slots){let el=host.querySelector('[data-native]');if(!el){el=document.createElement('section');el.dataset.native='';el.innerHTML='<textarea aria-label="Native composer"></textarea>';host.append(el)}Object.assign(el.style,{position:'absolute',left:slot.left+'px',top:slot.top+'px',width:slot.width+'px',height:slot.height+'px',clipPath:slot.clipPath,visibility:slot.visible?'visible':'hidden'})}}});window.controls=binding.api;return binding};
      const component=${JSON.stringify(url.pathname === '/__html')}?React.createElement(HtmlRenderer,{content:${literal(content)},autoHeight:true}):React.createElement(HtmlReportFrame,{html:${literal(content)},title:'Sample',className:'',autoHeight:true});
      function Fixture(){const selected=useSelectedReportDocument('Crew/sample');return React.createElement(ReportEmbedProvider,{value:{data,chatWidgets}},selected==='db/reports/index.html'?component:React.createElement(HtmlReportFrame,{html:'<h1>Other dashboard</h1>',title:'Other',className:''}))};
      createRoot(host).render(React.createElement(Fixture));
    </script>`))
  })
} }
const server=await createServer({configFile:false,plugins:[react(),fixture],optimizeDeps:{entries:[],include:['react','react-dom/client','react-dom/server','react/jsx-runtime','react/jsx-dev-runtime','react-markdown','remark-gfm'],noDiscovery:true},server:{host:'127.0.0.1',port:0}})
let browser
try {
  await server.listen(); const origin=server.resolvedUrls.local[0].replace(/\/$/,'')
  browser=await chromium.launch({headless:true,args:['--no-sandbox']})
  const page=await browser.newPage({viewport:{width:1100,height:900}})
  await page.addInitScript(()=>{
    window.frameAssignments=[]
    const property=Object.getOwnPropertyDescriptor(HTMLIFrameElement.prototype,'src')
    Object.defineProperty(HTMLIFrameElement.prototype,'src',{...property,set(value){
      if(String(value).startsWith('data:text/html')&&!this.hasAttribute('data-reopened-proof'))window.frameAssignments.push({sandbox:this.getAttribute('sandbox'),referrer:this.referrerPolicy})
      property.set.call(this,value)
    }})
  })
  const reopen = async () => {
    await page.evaluate(()=>{const original=document.querySelector('iframe');const frame=document.createElement('iframe');frame.id='reopened';frame.setAttribute('data-reopened-proof','');frame.style.display='none';frame.referrerPolicy='no-referrer';frame.src=original.src;document.body.append(frame)})
    const reopened=await page.locator('#reopened').elementHandle();const frame=await reopened.contentFrame()
    await frame.waitForFunction(()=>Boolean(window.probe),null,{polling:50})
    const result=await frame.evaluate(()=>({dom:probe.parentDOM,storage:probe.parentStorage,cookies:probe.cookies}))
    assert.deepEqual(result,{dom:'denied',storage:'denied',cookies:'denied'},'reopened document gained app origin')
    await page.evaluate(()=>document.querySelector('#reopened').remove())
  }
  const errors=[];page.on('pageerror',e=>errors.push(e.message))
  for(const surface of ['native','html','preview','large-supported','large','preview-large']){
    console.log('Checking isolated surface:',surface)
    await page.goto(surface.startsWith('preview')?origin+'/report-preview/?workspace=Crew/sample&token=PREVIEW_ONLY_FIXTURE&theme=dark'+(surface==='preview-large'?'&document=db/reports/large.html':''):origin+'/__'+surface)
    const child=page.frameLocator('iframe')
    if(surface==='large'||surface==='preview-large'){
      await child.getByRole('alert').filter({hasText:'1.5 MB limit'}).waitFor()
      assert.equal(await page.evaluate(()=>frameAssignments.length>0&&frameAssignments.every(value=>value.sandbox==='allow-scripts'&&value.referrer==='no-referrer')),true)
      if(surface==='large')assert.deepEqual(await page.evaluate(()=>calls),[],'oversized authored script ran')
      else {await page.waitForFunction(()=>window.__reportPreview.getState().previewState==='error');assert.match((await page.evaluate(()=>window.__reportPreview.getState())).report.errors[0],/1.5 MB limit/)}
      continue
    }
    if(surface!=='html')await child.getByText('Total 42',{exact:true}).waitFor().catch(async error=>{console.log('Load diagnostics',surface,errors,await page.evaluate(()=>window.__reportPreview?.getState()),page.frames().map(frame=>frame.url()));throw error})
    else await child.getByText('Sample report',{exact:true}).waitFor()
    const f=page.frames().find(f=>f.parentFrame());await f.waitForLoadState('load')
    const probe=await f.evaluate(()=>({parentDOM:window.probe.parentDOM,parentStorage:window.probe.parentStorage,cookies:window.probe.cookies,urlLeak:window.probe.urls.some(url=>(url.startsWith('data:')?atob(url.split(',')[1]):url).includes(['PREVIEW','ONLY','FIXTURE'].join('_')))}))
    assert.equal(probe.parentDOM,'denied');assert.equal(probe.parentStorage,'denied');assert.equal(probe.cookies,'denied')
    assert.equal(probe.urlLeak,false)
    assert.equal(await page.locator('iframe').getAttribute('sandbox'),'allow-scripts')
    assert.equal(await page.evaluate(()=>document.querySelector('iframe').contentDocument),null)
    await child.getByRole('button',{name:'Escape',exact:true}).click()
    assert.equal((await f.evaluate(()=>probe)).popup,'blocked');assert.equal((await f.evaluate(()=>probe)).top,'blocked')
    assert.equal(page.context().pages().length,1)
    assert.equal(await page.evaluate(()=>frameAssignments.length>0&&frameAssignments.every(value=>value.sandbox==='allow-scripts'&&value.referrer==='no-referrer')),true,'src was assigned before sandbox/referrer policy')
    assert.equal(await page.evaluate(()=>document.querySelector('iframe').src.startsWith('data:text/html;charset=utf-8;base64,')),true)
    await reopen()
    if(surface==='large-supported')assert.equal(await page.evaluate(()=>atob(document.querySelector('iframe').src.split(',')[1]).length>1_450_000),true)
    if(surface==='html'||surface==='large-supported')continue
    await child.getByRole('button',{name:'Load file',exact:true}).click()
    await child.getByText('FILE_BYTES',{exact:true}).waitFor()
    assert.match(await f.evaluate(()=>probe.file),/^blob:null\//)
    assert.equal(await f.evaluate(()=>probe.messageLeak),false,'preview credential crossed the document bridge')
    if(surface==='preview'){
      await page.waitForFunction(()=>window.__reportPreview.getState().previewState==='ready')
      const checks=await page.evaluate(()=>window.__reportPreview.getUIChecks());assert(checks)
      await page.evaluate(()=>window.__reportPreview.setTheme('light'))
      await child.locator('html[data-theme="light"]').waitFor()
      await page.evaluate(()=>window.__reportPreview.seek(1))
      assert.equal(await child.locator('body').getAttribute('data-clock'),'1')
      assert.equal((await page.evaluate(()=>window.__reportPreview.motion())).length,1)
      await page.evaluate(()=>window.__reportPreview.freeze());continue
    }
    await child.getByRole('button',{name:'Run Python',exact:true}).click();await child.getByText('PYTHON_OK',{exact:true}).waitFor()
    await child.getByRole('button',{name:'Send message',exact:true}).click();await child.getByText('queued',{exact:true}).waitFor()
    await child.getByRole('button',{name:'Save value',exact:true}).click();await child.getByText('saved',{exact:true}).waitFor()
    await child.getByRole('button',{name:'Read sample',exact:true}).click()
    await page.waitForFunction(()=>calls.includes('action:sample_read'))
    await page.waitForFunction(()=>window.opens>0&&window.slots.length===1)
    await page.getByLabel('Native composer').fill('Unsent draft')
    await page.evaluate(()=>controls.close('#panel'));await page.waitForFunction(()=>!slots[0].visible)
    await child.getByRole('button',{name:'Ask',exact:true}).click();await page.waitForFunction(()=>slots[0].visible&&opens>=2)
    assert.equal(await page.getByLabel('Native composer').inputValue(),'Unsent draft')
    await page.setViewportSize({width:420,height:900});await page.waitForFunction(()=>slots[0]?.width<400)
    assert.equal(await page.getByLabel('Native composer').inputValue(),'Unsent draft')
    const channel=await f.evaluate(()=>JSON.parse(document.getElementById('aw-native-document-config').textContent).channel)
    const before=await page.evaluate(()=>calls.length)
    await page.evaluate(channel=>window.postMessage({channel,method:'data.query',args:['spoof']},'*'),channel)
    await f.evaluate(()=>parent.postMessage({channel:'wrong',method:'data.query',args:['spoof']},'*'))
    await f.evaluate(()=>report.get('db/barrier.json'));assert.equal(await page.evaluate(()=>calls.length),before)
    await page.evaluate(()=>{document.documentElement.classList.add('dark')});await child.locator('html[data-theme="dark"]').waitFor()
    // A child may lie about geometry. The host clips it independently to its iframe.
    await f.evaluate(channel=>parent.postMessage({channel,method:'widgets.layout',args:[[{key:1,left:-500,top:-500,width:2000,height:2000,visible:true,clipPath:'inset(0px 0px 0px 0px)'}],true]},'*'),channel)
    const clip=await page.waitForFunction(()=>slots[0]?.width===2000&&slots[0].clipPath);assert.match(await clip.jsonValue(),/inset\(500px /)
    await f.evaluate(channel=>parent.postMessage({channel,method:'widgets.layout',args:[[{key:1,left:-50,top:0,width:1e308,height:200,visible:true,clipPath:'inset(0px 0px 0px 0px)'}],true]},'*'),channel)
    await page.waitForFunction(()=>slots.length===0)
    await f.evaluate(()=>document.querySelector('#panel').remove())
    await page.waitForFunction(()=>slots.length===0)
    await page.getByLabel('Other field').fill('Keep typing')
    await page.waitForFunction(()=>Date.now()-window.openedAt>=600)
    assert.match(await page.evaluate(()=>controls.open().catch(error=>error.message)),/Finish editing/)
    assert.equal(await page.evaluate(()=>document.activeElement.id),'outside')
    await child.getByRole('link',{name:'Unsafe link'}).click()
    assert.equal(await f.evaluate(()=>window.compromised),undefined)
    await child.getByRole('link',{name:'Other dashboard'}).click();await child.getByRole('heading',{name:'Other dashboard'}).waitFor()
    assert.equal(await page.evaluate(()=>localStorage.getItem('report-document:Crew/sample')),'db/reports/other.html')
    await page.evaluate(()=>localStorage.removeItem('report-document:Crew/sample'))
    await page.setViewportSize({width:1100,height:900})
  }
  console.log('All three isolated surface and interaction checks passed.')
  // The sandbox remains safe even when its trusted child runtime never starts.
  await page.route('**/report-preview.js', route => route.fulfill({ contentType:'text/javascript', body:'/* unavailable child runtime */' }))
  await page.goto(origin+'/__native')
  await page.frameLocator('iframe').getByText('Sample report',{exact:true}).waitFor()
  await page.frames().find(frame=>frame.parentFrame()).waitForLoadState('load')
  assert.deepEqual(await page.evaluate(()=>calls),[])
  await reopen()
  await page.unroute('**/report-preview.js')
  assert.deepEqual(errors.filter(e=>!e.includes('ResizeObserver loop')),[])
  const out=process.env.BROWSER_CHECK_ARTIFACT_DIR||await mkdtemp(path.join(tmpdir(),'dashboard-isolation-'));await mkdir(out,{recursive:true});await page.screenshot({path:path.join(out,'isolated-preview.png')})
  console.log('PASS: native, HTML viewer and preview opaque; app DOM/storage/cookies inaccessible; popup/top navigation blocked; document URL opaque even when reopened without sandbox;1.45MB document loads;5MB source has a plain refusal in app and preview; file blobs opaque; query/Python/write/message/Ask/function controls/theme/resize/motion/preview capture work; spoofed source/channel ignored and native slots clipped. Service and conversation replies are fixtures.')
} finally{await browser?.close();await server.close()}
