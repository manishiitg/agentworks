// Real Chromium, production MCP Apps bundles, real opaque child document and
// protocol messages. Host replies are fixtures; live hosts/LLM execution remain
// separate acceptance checks.
import { execFileSync } from 'node:child_process'
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { mkdir, mkdtemp, readFile, rm, writeFile } from 'node:fs/promises'
import os from 'node:os'
import path from 'node:path'
import { build } from 'vite'
import { chromium } from 'playwright'

const directory = await mkdtemp(path.join(os.tmpdir(), 'mcp-apps-browser-'))
let browser
let server
try {
  for (const entry of ['document', 'main']) {
    process.env.MCP_APP_ENTRY = entry
    await build({ configFile: 'vite.mcp-apps.config.ts', build: { outDir: directory } })
  }
  const main = await readFile(path.join(directory, 'mcp-dashboard.js'), 'utf8')
  const child = await readFile(path.join(directory, 'mcp-dashboard-document.js'), 'utf8')
  execFileSync('npm', ['run', 'build:dashboard'], { stdio: 'inherit' })
  const tsxAssets = { 'dashboard-react.js': await readFile('dist/dashboard-react.js', 'utf8'), 'dashboard.css': await readFile('dist/dashboard.css', 'utf8') }
  const tsxHTML = `<html data-report-renderer="tsx"><head><link rel="stylesheet" href="/dashboard.css"><script defer src="/dashboard-react.js"></script><script>document.addEventListener('DOMContentLoaded',()=>{const {mount,Heading,Metric,AskButton}=window.__agentworksDashboard.modules['@agentworks/dashboard'];const React=window.__agentworksDashboard.modules.react;mount({loadData:report=>report.query('select 42 as n'),default:({data})=>React.createElement(React.Fragment,null,React.createElement(Heading,{text:'TSX dashboard',level:'h1'}),React.createElement(Metric,{label:'Query result',value:data[0].n}),React.createElement(AskButton,{label:'Ask TSX dashboard'}))})})</script></head><body><div id="dashboard-root"></div></body></html>`
  const frame = await readFile('../agent_go/cmd/server/mcp_apps/frame.html', 'utf8')
  const dashboard = { resource_id: '00000000-0000-4000-8000-000000000001', workspace_id: 'work:00000000-0000-4000-8000-000000000002', revision: 'published', title: 'Sales', url: 'https://example.com/d/shared', asset_prefix: 'db/reports/managed/sales/published', files: {}, capability: { can_ask: true, mode: 'question_only' },
    html: `<h1>Sales</h1><p id="total">Loading</p><button data-report-action="count_items">Count items</button><button onclick="window.report.chat.open()">Ask about this dashboard</button><script>window.report.ready(async()=>{document.querySelector('#total').textContent='Total '+(await window.report.query('select 42 as n'))[0].n;try{parent.document.body.dataset.compromised='yes'}catch{document.body.dataset.isolated='yes'}})</script>` }
  const result = { content: [{ type: 'text', text: 'Sales dashboard. https://example.com/d/shared' }], structuredContent: { resource_id: dashboard.resource_id }, _meta: { dashboard } }
  let origin = ''
  server = createServer((request, response) => {
    response.setHeader('Content-Type', 'text/html')
    if (request.url === '/api/mcp-apps/frame') {
      response.setHeader('Content-Security-Policy', "sandbox allow-scripts; default-src 'none'; script-src 'unsafe-inline' blob:; style-src 'unsafe-inline' blob:; img-src data: blob:; font-src data: blob:; connect-src 'none'; frame-src 'none'; form-action 'none'; base-uri 'none'")
      response.end(frame); return
    }
    if (request.url === '/app') {
      // Same resource structure as Go: trusted runtime bytes, no bearer token.
      response.setHeader('Content-Security-Policy', `default-src 'none'; script-src 'unsafe-inline'; style-src 'unsafe-inline'; frame-src ${origin}`)
      const runtimes = JSON.stringify({ document: child, frame_url: origin + '/api/mcp-apps/frame', ...tsxAssets }).replaceAll('<', '\\u003c')
      response.end(`<main id="app"></main><script id="aw-runtimes" type="application/json">${runtimes}</script><script>${main.replaceAll('</script', '<\\/script')}</script>`); return
    }
    response.end(`<iframe id="app" src="/app" style="width:800px;height:1200px"></iframe><script>
      window.calls=[]; const app=document.querySelector('#app'); const view=${JSON.stringify(request.url === '/tsx' ? { ...result, _meta: { dashboard: { ...dashboard, html: tsxHTML } } } : result).replaceAll('<', '\\u003c')};
      addEventListener('message',event=>{
        if(event.source!==app.contentWindow||event.data?.jsonrpc!=='2.0')return;
        const {id,method,params}=event.data;
        if(method==='ui/notifications/initialized'){app.contentWindow.postMessage({jsonrpc:'2.0',method:'ui/notifications/tool-input',params:{arguments:{resource_id:view.structuredContent.resource_id}}},'*');app.contentWindow.postMessage({jsonrpc:'2.0',method:'ui/notifications/tool-result',params:view},'*');return;}
        if(!id)return; let result={};
        if(method==='ui/initialize')result={protocolVersion:'2026-01-26',hostCapabilities:{serverTools:{}},hostInfo:{name:'fixture',version:'1'},hostContext:{theme:'light'}};
        else if(method==='tools/call'){
          calls.push(params); const args=params.arguments;
          if(params.name==='open_dashboard')result=view;
          else if(params.name==='read_dashboard_data')result={structuredContent:{success:true,data:{rows:[{n:42}]}}};
          else if(params.name==='call_tool'){
            const a=args.arguments;
            if(args.name==='functions'&&a.action==='list')result={structuredContent:{functions:[{name:'count_items',input_schema:{type:'object',properties:{quantity:{type:'integer'}},required:['quantity']}}]}};
            else if(args.name==='functions')result={structuredContent:{status:'completed',call_id:'fixture-call',result:{quantity:a.args?.quantity}}};
            else result={structuredContent:{chat_id:'caller-chat',next_after:4,working:false,messages:[{role:'assistant',text:'Hello from this Crew'}]}};
          }
        }
        app.contentWindow.postMessage({jsonrpc:'2.0',id,result},'*');
      });
    </script>`)
  })
  await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
  origin = `http://127.0.0.1:${server.address().port}`
  browser = await chromium.launch({ headless: true, ...(process.env.CHROME_PATH ? { executablePath: process.env.CHROME_PATH } : {}), args: ['--no-sandbox'] })
  const page = await browser.newPage()
  const errors = []
  page.on('pageerror', error => errors.push(error.message))
  await page.goto(origin)
  const app = page.frameLocator('#app')
  const dashboardFrame = app.frameLocator('iframe')
  await dashboardFrame.getByText('Total 42', { exact: true }).waitFor()
  assert.equal(await app.locator('body').getAttribute('data-compromised'), null)
  assert.equal(await dashboardFrame.locator('body').getAttribute('data-isolated'), 'yes')
  const mutations = () => page.evaluate(() => window.calls.filter(call => call.name === 'call_tool' && ['ask', 'call'].includes(call.arguments.arguments.action)))
  assert.equal((await mutations()).length, 0)
  await dashboardFrame.getByRole('button', { name: 'Count items' }).click()
  await app.getByRole('dialog').waitFor()
  assert.equal((await mutations()).length, 0, 'opening a function form must not run it')
  await app.getByLabel('quantity').fill('3')
  await app.getByRole('button', { name: 'Run', exact: true }).click()
  await app.getByRole('dialog').getByText('"quantity": 3', { exact: false }).waitFor()
  assert.equal((await mutations())[0].arguments.arguments.args.quantity, 3)
  await app.getByRole('button', { name: 'Close', exact: true }).click()
  assert.equal(await app.getByLabel('Message to the Crew').isVisible(), false, 'authored page has no second outer chat')
  await dashboardFrame.getByRole('button', { name: 'Ask about this dashboard' }).click()
  await app.getByLabel('Message to the Crew').fill('Hello')
  await app.getByRole('button', { name: 'Send', exact: true }).click()
  await app.getByText('Crew: Hello from this Crew', { exact: true }).waitFor()
  await app.getByRole('button', { name: 'Close', exact: true }).click()
  const count = (await mutations()).length
  await app.getByRole('button', { name: 'Refresh', exact: true }).click()
  await dashboardFrame.getByText('Total 42', { exact: true }).waitFor()
  assert.equal((await mutations()).length, count, 'refresh must not repeat messages or function calls')
  await page.goto(origin + '/tsx')
  await dashboardFrame.getByText('TSX dashboard', { exact: true }).waitFor()
  await dashboardFrame.locator('.dashboard-metric').filter({ hasText: '42' }).waitFor()
  assert(parseFloat(await dashboardFrame.locator('.dashboard-stat').evaluate(element => getComputedStyle(element).paddingLeft)) >= 16, 'shared component stylesheet missing in MCP App')
  assert.equal((await mutations()).length, 0, 'JSON rendering reads data without starting work')
  await dashboardFrame.getByRole('button', { name: 'Ask TSX dashboard' }).click()
  await app.getByLabel('Message to the Crew').waitFor({ state: 'visible' })
  await app.getByRole('button', { name: 'Close', exact: true }).click()
  // Even a hostile embedding page that omits iframe sandbox flags cannot
  // turn the public bootstrap into same-origin script execution.
  assert.equal(await page.evaluate(() => new Promise(resolve => {
    const probe = document.createElement('iframe')
    const listener = event => {
      if (event.source !== probe.contentWindow || event.data?.kind !== 'sandbox-proof') return
      removeEventListener('message', listener); probe.remove(); resolve(event.data.isolated)
    }
    addEventListener('message', listener)
    probe.onload = () => probe.contentWindow.postMessage({ kind: 'agentworks-dashboard-document', html: `<script>let isolated=false;try{localStorage.getItem('sandbox-canary')}catch{isolated=true}parent.postMessage({kind:'sandbox-proof',isolated},'*')<\/script>` }, '*')
    probe.src = '/api/mcp-apps/frame'; document.body.append(probe)
  })), true)
  if (process.env.BROWSER_CHECK_ARTIFACT_DIR) {
    await mkdir(process.env.BROWSER_CHECK_ARTIFACT_DIR, { recursive: true })
    await page.screenshot({ path: path.join(process.env.BROWSER_CHECK_ARTIFACT_DIR, 'mcp-dashboard.png'), fullPage: true })
    const assets = path.join(process.env.BROWSER_CHECK_ARTIFACT_DIR, 'runtime-assets')
    await mkdir(assets, { recursive: true })
    await writeFile(path.join(assets, 'mcp-dashboard.js'), main)
    await writeFile(path.join(assets, 'mcp-dashboard-document.js'), child)
  }
  assert.deepEqual(errors, [])
  console.log('PASS: MCP Apps handshake, opaque document, read data, explicit Ask/function submission, refresh without replay')
} finally {
  await browser?.close()
  if (server) await new Promise(resolve => server.close(resolve))
  await rm(directory, { recursive: true, force: true })
}
