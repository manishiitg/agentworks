// Real compiled TSX/runtime: a new character arrives during work while the
// selected video DOM, playback position and review draft remain mounted.
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { createServer } from 'node:http'
import { mkdtemp, mkdir, readFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'

const frontend = fileURLToPath(new URL('..',import.meta.url))
const repo = path.dirname(frontend)
const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || await mkdtemp(path.join(os.tmpdir(),'dashboard-media-'))
await mkdir(output,{recursive:true})
const fixture=path.join(output,'media.html')
execFileSync('go',['test','./dashboards','-run','^TestTSXMediaDashboardFixture$','-count=1'],{cwd:path.join(repo,'workspace'),env:{...process.env,DASHBOARD_MEDIA_TEST_FIXTURE:fixture},stdio:'inherit'})
execFileSync('node',['scripts/prepare-local-runtime-assets.mjs'],{cwd:frontend,stdio:'inherit'})
const movie=await readFile(path.join(frontend,'scripts/fixtures/solid-green.webm'))
const pixel=await readFile(path.join(frontend,'scripts/fixtures/solid-blue.png'))
const html=await readFile(fixture,'utf8')
let data=[{id:'clip-a',kind:'media.video',title:'First clip',revision:1,payload:{path:'db/assets/a.webm'}},{id:'clip-b',kind:'media.video',title:'Second clip',revision:1,payload:{path:'db/assets/b.webm'}}]
let mediaURLs=0
const server=createServer(async(req,res)=>{
 const url=new URL(req.url,'http://localhost')
 if(url.pathname==='/report-preview/'){res.setHeader('Content-Type','text/html');res.end('<html><body><script src="/report-preview/report-preview.js"></script></body></html>');return}
 if(url.pathname==='/report-preview/report-preview.js'||/^\/(dashboard-react\.js|dashboard\.css)$/.test(url.pathname)){
  res.setHeader('Content-Type',url.pathname.endsWith('.css')?'text/css':'text/javascript')
  res.end(await readFile(path.join(frontend,'dist',path.basename(url.pathname))));return
 }
 if(url.pathname.endsWith('/file')){
  const file=url.searchParams.get('path')
  if(file==='db/media.json'){res.setHeader('Content-Type','application/json');res.end(JSON.stringify(data));return}
  if(file==='db/assets/host.png'){res.setHeader('Content-Type','image/png');res.end(pixel);return}
  res.setHeader('Content-Type','text/html');res.end(html);return
 }
 if(url.pathname.endsWith('/media-url')){
  mediaURLs++;res.setHeader('Content-Type','application/json');res.end(JSON.stringify({url:'/clip.webm'}));return
 }
 if(url.pathname==='/clip.webm'){res.setHeader('Content-Type','video/webm');res.setHeader('Accept-Ranges','bytes');res.end(movie);return}
 res.statusCode=404;res.end('Missing fixture')
})
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve))
const origin='http://127.0.0.1:'+server.address().port
const browser=await chromium.launch({headless:true})
let page
try{
 page=await browser.newPage({viewport:{width:1200,height:900}})
 const errors=[];page.on('pageerror',error=>errors.push(error.message))
 await page.goto(origin+'/report-preview/?workspace=Crew/example&token=fixture&document=db/reports/index.html&theme=dark')
 await page.waitForFunction(()=>document.documentElement.dataset.previewState==='ready')
 const frame=page.frames().find(value=>value.parentFrame())
 await frame.getByRole('button',{name:'Second clip',exact:true}).click()
 await frame.locator('video').waitFor()
 await frame.waitForFunction(()=>{const video=document.querySelector('video');return video && (video.readyState>=1 || video.error)})
 assert.deepEqual(await frame.locator('video').evaluate(video=>video.error ? {code:video.error.code,message:video.error.message,src:video.currentSrc}:null),null,'The fixture must be playable')
 await frame.getByRole('textbox',{name:'Review note'}).fill('Keep this review draft')
 await frame.evaluate(()=>{window.savedPlayer=document.querySelector('video');window.savedPlayer.currentTime=.5})
 const initialURLs=mediaURLs
 data=[{id:'character',kind:'media.character',title:'New character',revision:1,payload:{image_path:'db/assets/host.png',spec:'# Appearance\n\n**Approved** appearance'}},...data]
 await frame.evaluate(async()=>{await Promise.all(window.__reportQueuedCallbacks.map(callback=>window.__runReportCallback(callback)))})
 await frame.getByRole('img',{name:'New character',exact:true}).waitFor()
 await frame.waitForFunction(()=>document.querySelector('img')?.naturalWidth===16)
 await frame.getByRole('heading',{name:'Appearance',exact:true}).waitFor()
 assert.equal(await frame.locator('strong').filter({hasText:'Approved'}).textContent(),'Approved')
 assert.ok(await frame.getByRole('img',{name:'New character',exact:true}).evaluate(image=>image.getBoundingClientRect().height<=576),'Reference images fit an automatically sized report frame')
 assert.equal(await frame.getByRole('textbox',{name:'Review note'}).inputValue(),'Keep this review draft')
 assert.equal(await frame.evaluate(()=>window.savedPlayer===document.querySelector('video')),true)
 assert.ok(await frame.locator('video').evaluate(video=>video.currentTime>=.4))
 assert.equal(mediaURLs,initialURLs)
 data=[{id:'clip-c',kind:'media.video',title:'New clip',revision:1,payload:{path:'db/assets/c.webm'}},...data]
 await frame.evaluate(async()=>{await Promise.all(window.__reportQueuedCallbacks.map(callback=>window.__runReportCallback(callback)))})
 await frame.getByRole('button',{name:'New clip',exact:true}).waitFor()
 await frame.waitForFunction(()=>document.querySelector('button[aria-pressed="true"]')?.textContent==='New clip')
 assert.equal(await frame.getByRole('textbox',{name:'Review note'}).inputValue(),'Keep this review draft')
 await page.screenshot({path:path.join(output,'live-media-results.png'),fullPage:true})
 assert.deepEqual(errors,[])
 console.log('PASS: live character and video results; selection, draft and existing player preserved on unrelated refresh')
}catch(error){
 if(page){
  console.log('Browser diagnostics:',JSON.stringify(await page.evaluate(()=>window.__reportPreview?.getState())))
  for(const frame of page.frames().filter(value=>value.parentFrame())) console.log('Media diagnostics:',JSON.stringify(await frame.evaluate(()=>({videos:[...document.querySelectorAll('video')].map(v=>({src:v.currentSrc,ready:v.readyState,error:v.error?.message,webm:v.canPlayType('video/webm'),mp4:v.canPlayType('video/mp4')})),alerts:[...document.querySelectorAll('[role=alert]')].map(v=>v.textContent)}))))
  await page.screenshot({path:path.join(output,'media-failure.png'),fullPage:true})
 }
 throw error
}finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
