// Real Chromium playback plus a real snapshot-backed HyperFrames -> MP4 export.
import assert from 'node:assert/strict'
import { createServer } from 'vite'
import { chromium } from 'playwright'
import { mkdtemp, mkdir, writeFile, readFile } from 'node:fs/promises'
import { tmpdir } from 'node:os'
import path from 'node:path'
import { spawnSync } from 'node:child_process'

const fixture = { name: 'dashboard-motion-fixture', configureServer(server) {
  server.middlewares.use('/__bad-video', (_req, res) => { res.statusCode=404;res.end('Not found') })
  server.middlewares.use('/__motion', async (_req, res) => {
    res.setHeader('Content-Type', 'text/html')
    res.end(await server.transformIndexHtml('/__motion', `<!doctype html>
      <iframe style="width:100%;height:200px"></iframe>
      <script type="module">
      import { installReportHost } from '/src/components/workflow/reportWidgets/reportHostRuntime.ts';
      const frame = document.querySelector('iframe');
      frame.srcdoc='<div id="value"></div><button id="play">Play</button><button id="pause">Pause</button><input id="seek" type="range" min="0" max="1" step="0.01">';
      frame.onload=()=>{
        window.errors=[];window.paints=0;
        const dataApi={workspacePath:'Workflow/demo'};
        const install=()=>installReportHost(frame,{title:'Motion',dataApi,theme:'light',tokenSource:null,dispatchData:false});
        install(); window.install=install;
        const win=frame.contentWindow,doc=frame.contentDocument;
        window.make=()=>window.player=win.report.motion.create({id:'history',duration:1,render:s=>{window.paints++;doc.querySelector('#value').textContent=String(Math.floor(s.progress*4));}});
        window.make();
        doc.querySelector('#play').onclick=()=>window.player.play();
        doc.querySelector('#pause').onclick=()=>window.player.pause();
        doc.querySelector('#seek').oninput=e=>window.player.seek(Number(e.target.value));
        window.ready=true;
      };
      </script>`))
  })
} }

const server = await createServer({ configFile:false, plugins:[fixture], optimizeDeps:{entries:[]}, server:{host:'127.0.0.1',port:0} })
let browser
try {
  await server.listen()
  browser = await chromium.launch({headless:true})
  const page = await browser.newPage()
  await page.goto(server.resolvedUrls.local[0]+'__motion')
  await page.waitForFunction(()=>window.ready)
  assert.equal(await page.evaluate(()=>window.player.getState().playing),false)
  const report=page.frameLocator('iframe')
  await report.getByRole('button',{name:'Play',exact:true}).click()
  await page.waitForFunction(()=>window.player.getState().time>0.1)
  await report.getByRole('button',{name:'Pause',exact:true}).click()
  const stopped=await page.evaluate(()=>window.player.getState().time)
  await page.waitForTimeout(100)
  assert.equal(await page.evaluate(()=>window.player.getState().time),stopped)
  await page.evaluate(()=>{const slider=document.querySelector('iframe').contentDocument.querySelector('#seek');slider.value='0.75';slider.dispatchEvent(new Event('input'))})
  assert.equal(await report.locator('#value').textContent(),'3')
  await page.evaluate(()=>{const slider=document.querySelector('iframe').contentDocument.querySelector('#seek');slider.value='0.25';slider.dispatchEvent(new Event('input'))})
  assert.equal(await report.locator('#value').textContent(),'1')
  await page.evaluate(()=>{window.install();window.make();window.player.setSpeed(2)})
  assert.equal(await page.evaluate(()=>window.player.getState().time),0.25)
  assert.equal(await page.evaluate(()=>window.player.getState().speed),2)
  await page.evaluate(()=>window.player.replay())
  await page.waitForFunction(()=>!window.player.getState().playing)
  assert.equal(await report.locator('#value').textContent(),'4')
  await page.emulateMedia({reducedMotion:'reduce'})
  await page.evaluate(()=>{window.player.destroy();window.make()})
  assert.equal(await page.evaluate(()=>window.player.getState().time),1)
  await page.evaluate(()=>{const slider=document.querySelector('iframe').contentDocument.querySelector('#seek');slider.value='0.5';slider.dispatchEvent(new Event('input'))})
  assert.equal(await report.locator('#value').textContent(),'2')
  const errors=await page.evaluate(()=>window.errors)
  assert.deepEqual(errors,[])
  await page.evaluate(()=>{const doc=document.querySelector('iframe').contentDocument;const video=doc.createElement('video');video.src='/__bad-video';doc.body.appendChild(video)})
  await report.locator('[data-report-error]').waitFor()
  assert.match(await report.locator('[data-report-error]').textContent(),/video or audio could not be loaded/)
  console.log('Chromium: play/pause, backward seek, replay, speed, refresh position and reduced motion passed.')

  const root=await mkdtemp(path.join(tmpdir(),'dashboard-motion-'))
  const encoder=spawnSync('python3',[path.resolve('../deploy/rootless-linux/install-ffmpeg.py')],{env:{...process.env,REMOTE_APP:root},encoding:'utf8',timeout:180000})
  assert.equal(encoder.status,0,encoder.stderr)
  const provision=spawnSync('python3',[path.resolve('../deploy/rootless-linux/install-ffprobe.py')],{env:{...process.env,REMOTE_APP:root},encoding:'utf8',timeout:180000})
  assert.equal(provision.status,0,provision.stderr)
  const repeat=spawnSync('python3',[path.resolve('../deploy/rootless-linux/install-ffprobe.py')],{env:{...process.env,REMOTE_APP:root},encoding:'utf8',timeout:30000})
  assert.equal(repeat.status,0,repeat.stderr)
  assert.match(repeat.stdout,/unchanged/)
  const source=path.join(root,'code/motion/check')
  await mkdir(source,{recursive:true})
  const gsap=await fetch('https://cdn.jsdelivr.net/npm/gsap@3.14.2/dist/gsap.min.js')
  assert.ok(gsap.ok)
  await writeFile(path.join(source,'gsap.js'),await gsap.text())
  await writeFile(path.join(source,'snapshot.json'),JSON.stringify({as_of:'2026-10-15T00:00:00Z',source:'Recorded sample totals',data:[10,20]}))
  await writeFile(path.join(source,'index.html'),`<!doctype html><html><head><meta charset="utf-8"><script src="gsap.js"></script><style>body{margin:0;background:white;color:black}#root{width:320px;height:180px}#bar{height:80px;width:0;background:rgb(20,160,220)}#value{font:40px sans-serif}</style></head><body><div id="root" data-composition-id="main" data-width="320" data-height="180" data-duration="1"><div id="bar"></div><div id="value">0</div></div><script>window.__timelines=window.__timelines||{};const timeline=gsap.timeline({paused:true});timeline.fromTo('#bar',{width:0},{width:307,duration:1,ease:'none'},0);timeline.fromTo('#value',{textContent:0},{textContent:20,duration:1,ease:'none',snap:{textContent:1}},0);window.__timelines.main=timeline;</script></body></html>`)
  const runner=path.resolve('../agent_go/pkg/dashboardmotion/render.py')
  const config={composition_path:'code/motion/check',output_path:'db/assets/motion/check',cache_path:'.sandbox-cache/dashboard-motion',caller:'test-author',renderer_version:'0.8.99'}
  const rendered=spawnSync('python3',[runner,Buffer.from(JSON.stringify(config)).toString('base64')],{cwd:root,env:{...process.env,PATH:path.join(root,'tools/bin')+path.delimiter+process.env.PATH,AGENT_BROWSER_EXECUTABLE_PATH:chromium.executablePath()},encoding:'utf8',timeout:600000,maxBuffer:8*1024*1024})
  process.stdout.write(rendered.stderr||'')
  assert.equal(rendered.status,0,rendered.stdout)
  const result=JSON.parse(rendered.stdout.trim())
  assert.equal(result.success,true)
  assert.equal(result.width,320)
  assert.equal(result.height,180)
  assert.ok(result.duration_s>=0.9 && result.duration_s<=1.2)
  assert.ok(result.bytes>0)
  assert.equal(result.created_by,'test-author')
  const decoded=spawnSync(path.join(root,'tools/bin/ffmpeg'),['-v','error','-i',path.join(root,result.path),'-vf','select=eq(n\\,29)','-frames:v','1','-f','rawvideo','-pix_fmt','rgb24','pipe:1'],{maxBuffer:1024*1024})
  assert.equal(decoded.status,0,decoded.stderr.toString())
  const finalPixel=[...decoded.stdout.subarray((40*320+305)*3,(40*320+305)*3+3)]
  const evidence=process.env.RUNNER_TEMP && path.join(process.env.RUNNER_TEMP,'browser-check-artifacts')
  if(evidence){await mkdir(evidence,{recursive:true});await writeFile(path.join(evidence,'dashboard-motion-final-frame.png'),await readFile(path.join(root,result.final_frame_path)));await writeFile(path.join(evidence,'dashboard-motion-export.json'),JSON.stringify(result,null,2))}
  assert.ok(finalPixel.every((v,i)=>Math.abs(v-[20,160,220][i])<22),`final tween did not reach its endpoint: ${finalPixel}`)
  assert.deepEqual(JSON.parse(await readFile(path.join(root,result.snapshot_path),'utf8')).data,[10,20])
  console.log('HyperFrames: real 320x180 MP4 encoded and fully decoded; source, snapshot, renderer version and caller preserved.')
} finally {
  await browser?.close()
  await server.close()
}
