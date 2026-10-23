// Real compiled TSX, real report host, then only four files on a static server.
import assert from 'node:assert/strict'
import { execFileSync } from 'node:child_process'
import { createServer } from 'node:http'
import { mkdtemp, readFile, mkdir, copyFile, writeFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { fileURLToPath } from 'node:url'
import { chromium } from 'playwright'
const frontend = fileURLToPath(new URL('..', import.meta.url)), repo = path.dirname(frontend)
const output = process.env.BROWSER_CHECK_ARTIFACT_DIR || await mkdtemp(path.join(os.tmpdir(), 'static-dashboard-'))
await mkdir(output, { recursive: true })
execFileSync('go', ['test', './cmd/server', '-run', '^TestStatic', '-count=1'], { cwd: path.join(repo, 'agent_go'), env: { ...process.env, GOWORK: 'off', GOTOOLCHAIN: 'auto' }, stdio: 'inherit' })
const fixture = path.join(output, 'source.html')
execFileSync('go', ['test', './dashboards', '-run', '^TestTSXDashboardLifecycle$', '-count=1'], { cwd: path.join(repo, 'workspace'), env: { ...process.env, DASHBOARD_TSX_TEST_FIXTURE: fixture }, stdio: 'inherit' })
execFileSync('npm', ['run', 'prepare:local-assets'], { cwd: frontend, stdio: 'inherit' })
const names = ['report-preview.js', 'dashboard-react.js', 'dashboard.css']
const runtimes = Object.fromEntries(await Promise.all(names.map(async name => [name, await readFile(path.join(frontend, 'dist', name))])))
await mkdir(path.join(output, 'runtime-assets'), { recursive: true })
for (const name of names) await copyFile(path.join(frontend, 'dist', name), path.join(output, 'runtime-assets', name))
let html = await readFile(fixture, 'utf8')
// Exercise media through the same authored ready/API contract without changing
// the fixture's compiled React source.
html = html.replace('</body>', `<script>report.ready(async()=>{let image=document.getElementById('export-image');if(!image){image=document.createElement('img');image.id='export-image';image.alt='Included image';document.body.append(image)}image.src=await report.fileUrl('db/pixel.png')})</script></body>`)
let total = 42, reads = 0
const sites = new Map()
const pixel = Buffer.from('iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mP8/x8AAwMCAO+j1ioAAAAASUVORK5CYII=', 'base64')
const server = createServer(async (req,res) => {
  const url = new URL(req.url, 'http://localhost')
  if (url.pathname === '/report-preview/') {res.setHeader('Content-Type','text/html');res.end('<html><script src="/report-preview/report-preview.js"></script></html>');return}
  if (url.pathname === '/report-preview/report-preview.js' || names.some(name => url.pathname === '/'+name)) {res.setHeader('Content-Type', url.pathname.endsWith('.css')?'text/css':'text/javascript');res.end(runtimes[path.basename(url.pathname)]);return}
  if (url.pathname.startsWith('/api/')) {
    reads++
    if (url.pathname.endsWith('/file')) {
      const file=url.searchParams.get('path')
      if (file==='db/pixel.png'){res.setHeader('Content-Type','image/png');res.end(pixel);return}
      res.setHeader('Content-Type',file==='db/summary.json'?'application/json':'text/html');res.end(file==='db/summary.json'?JSON.stringify({total}):html);return
    }
    res.setHeader('Content-Type','application/json')
    res.end(url.pathname.endsWith('/query')?'{"success":true,"data":{"rows":[{"value":42}]}}':'{"success":true,"data":{"marker":"SCRIPT_OK"}}');return
  }
  const match=url.pathname.match(/^\/sites\/(first|fresh)\/(.*)$/)
  if(match){const name=match[2]||'index.html';const data=name==='index.html'?sites.get(match[1]):runtimes[name];if(data){res.setHeader('Content-Type',name.endsWith('.html')?'text/html':name.endsWith('.css')?'text/css':'text/javascript');res.end(data);return}}
  res.statusCode=404;res.end('Missing fixture')
})
await new Promise(resolve=>server.listen(0,'127.0.0.1',resolve))
const origin=`http://127.0.0.1:${server.address().port}`, live='https://app.example.com/report/example'
const browser=await chromium.launch({headless:true})
try {
  const page=await browser.newPage({viewport:{width:1280,height:900}})
  const preview=origin+'/report-preview/?workspace=Crew/example&token=PREVIEW_ONLY_FIXTURE&document=db/reports/index.html&theme=dark&export=1&static=1'
  for(const [name,value] of [['first',42],['fresh',84]]){
    total=value;await page.goto(preview);await page.waitForFunction(()=>document.documentElement.dataset.previewState==='ready')
    const result=await page.evaluate(url=>window.__reportPreview.exportStatic(url),live)
    assert(!result.includes('PREVIEW_ONLY_FIXTURE'),'preview credential escaped')
    assert(result.includes('data:image/png;base64,'),'authorized image was not embedded')
    sites.set(name,result);await writeFile(path.join(output, name+'.html'),result)
  }
  const before=reads
  await page.route('https://app.example.com/**',route=>route.fulfill({body:'Sign in to AgentWorks',contentType:'text/html'}))
  for(const [name,value] of [['first',42],['fresh',84]]){
    await page.goto(origin+'/sites/'+name+'/index.html')
    await page.waitForFunction(()=>document.documentElement.dataset.previewState==='ready')
    const frame=page.frames().find(frame=>frame.parentFrame())
    assert.equal(await frame.locator('#total').textContent(),String(value))
    await frame.getByRole('button',{name:'Details',exact:true}).click()
    await frame.getByText('Recorded details').waitFor()
    assert.equal(await frame.getByRole('button',{name:'Ask',exact:true}).count(),0)
    assert.equal(await frame.getByAltText('Included image').evaluate(image=>image.naturalWidth),1)
    assert.match(await page.locator('body').innerText(),/Updated .*Published snapshot/)
    assert.equal(await page.getByRole('link',{name:'Open live dashboard'}).getAttribute('href'),live)
    const missing=await frame.evaluate(()=>report.get('db/not-captured.json').catch(error=>error.message))
    assert.match(missing,/not included.*live dashboard/)
    assert.equal(reads,before,'static site contacted the private data API')
    await page.setViewportSize({width:390,height:844})
    await page.screenshot({path:path.join(output,name+'-static-mobile.png'),fullPage:true})
    const popup=page.waitForEvent('popup')
    await frame.getByRole('button',{name:'Look up',exact:true}).click()
    const opened=await popup;assert.equal(opened.url(),live);await opened.close()
  }
  assert.equal(reads,before)
  console.log('PASS: same compiled TSX, packaged React/shadcn/Recharts/CSS, relative subfolder hosting, authorized frozen data/media, local interaction, live function link, no private API/preview credential, fresh Publish export updates data')
}finally{await browser.close();await new Promise(resolve=>server.close(resolve))}
