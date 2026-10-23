// Real Chromium against the production ChatVisual component and the existing
// dashboard frame's exact HTML/CSP. Run on Linux, outside a main checkout.
import assert from 'node:assert/strict'
import { createServer } from 'node:http'
import { readFile, writeFile } from 'node:fs/promises'
import { createRequire } from 'node:module'
import path from 'node:path'
const root = process.env.CHAT_VISUAL_QA_DIR
assert(root, 'Set CHAT_VISUAL_QA_DIR to the isolated Linux proof directory')
const frontend = path.join(root, 'source/frontend')
const require = createRequire(path.join(frontend, 'package.json'))
const { build } = require('esbuild')
const { chromium } = createRequire(path.join(root, 'browser-deps/package.json'))('playwright')
await build({ stdin: { contents: `import React from 'react';import{createRoot}from'react-dom/client';import{ChatVisual}from'./src/components/ui/ChatVisual';const root=createRoot(document.getElementById('reply'));window.renderVisual=(html)=>{localStorage.setItem('reply-source',html);root.render(<ChatVisual html={html}/>)};const saved=localStorage.getItem('reply-source');if(saved)window.renderVisual(saved);`, resolveDir: frontend, loader: 'tsx' }, outfile: path.join(root, 'fixture.js'), bundle: true, format: 'iife', jsx: 'automatic', define: { 'process.env.NODE_ENV': '"production"' }, plugins: [{ name: 'fixture-api-base', setup(builder) { builder.onResolve({ filter: /services\/api$/ }, () => ({ path: 'fixture-api', namespace: 'fixture' })); builder.onLoad({ filter: /.*/, namespace: 'fixture' }, () => ({ contents: 'export const getApiBaseUrl=()=>""' })) } }] })
const go = await readFile(path.join(root, 'source/agent_go/cmd/server/external_mcp_apps.go'), 'utf8')
const csp = go.match(/Set\("Content-Security-Policy", "(sandbox allow-scripts; [^"]+)"\)/)[1]
const frameHTML = await readFile(path.join(root, 'source/agent_go/cmd/server/mcp_apps/frame.html'), 'utf8')
const network = []
const server = createServer(async (req, res) => {
  if (req.url === '/api/mcp-apps/frame') { res.setHeader('Content-Type', 'text/html'); res.setHeader('Content-Security-Policy', csp); res.end(frameHTML); return }
  if (req.url === '/fixture.js') { res.setHeader('Content-Type', 'text/javascript'); res.end(await readFile(path.join(root, 'fixture.js'))); return }
  if (req.url === '/') { res.setHeader('Content-Type', 'text/html'); res.end('<!doctype html><html class="dark" data-theme="dark"><head><meta name="viewport" content="width=device-width"><style>body{margin:0;padding:20px;background:#0c0f12;color:#e5e7eb;font:14px/1.6 system-ui}main{max-width:900px;margin:auto}figure{margin:12px 0;border:1px solid #30353c;border-radius:12px;overflow:hidden}summary{padding:8px}pre{white-space:pre-wrap;overflow-wrap:anywhere}iframe{display:block;width:100%;border:0}</style></head><body><main><p>Private UI fixture · Assistant reply</p><div id="reply"></div></main><script src="/fixture.js"></script></body></html>'); return }
  if (req.url !== '/favicon.ico') network.push(req.url)
  res.statusCode = 404; res.end('Unexpected network')
})
await new Promise(resolve => server.listen(0, '127.0.0.1', resolve))
const origin = `http://127.0.0.1:${server.address().port}`
const html = body => `<!doctype html><html><head><title>Weekly activity</title><style>h1{margin:0;font-size:24px}.stats{display:grid;grid-template-columns:repeat(3,1fr);gap:12px;margin:18px 0}.stat{padding:16px;border:1px solid hsl(var(--border));border-radius:10px;background:hsl(var(--card))}.stat strong{display:block;font-size:28px}button{padding:8px 12px;border-radius:8px;border:1px solid hsl(var(--border));background:hsl(var(--card));color:inherit}@media(max-width:500px){.stats{grid-template-columns:1fr}}</style></head><body>${body}</body></html>`
const sample = html('<h1>What happened this week</h1><p>Fictional values for visual review.</p><div class="stats"><section class="stat">Received<strong>128</strong></section><section class="stat">Completed<strong>112</strong></section><section class="stat">Pending<strong id="pending">16</strong></section></div><button onclick="document.getElementById(\'pending\').textContent=\'15\'">Try a local interaction</button><p id="isolation"></p><script>let safe=0;try{parent.document.body.dataset.leak="yes"}catch{safe++}try{localStorage.getItem("private-marker")}catch{safe++}document.getElementById("isolation").textContent=safe===2&&typeof window.report==="undefined"?"Isolated display; no project APIs":"ISOLATION_FAILED"</script>')
let browser
try {
  browser = await chromium.launch({ executablePath: process.env.CHROME_PATH || '/usr/bin/google-chrome', headless: true, args: ['--no-sandbox'] })
  const page = await browser.newPage({ viewport: { width: 1280, height: 900 } })
  await page.goto(origin)
  await page.waitForFunction(() => typeof window.renderVisual === 'function')
  const render = async value => { await page.evaluate(value => window.renderVisual(value), value); await page.waitForTimeout(150) }
  const child = () => page.frames().find(frame => frame.parentFrame()?.parentFrame())
  await render(sample)
  await page.waitForFunction(() => document.querySelector('[data-chat-visual="preview"] iframe')?.style.height !== '320px')
  await child().getByText('Isolated display; no project APIs', { exact: true }).waitFor()
  await child().getByRole('button', { name: 'Try a local interaction' }).click()
  assert.equal(await child().locator('#pending').textContent(), '15')
  assert.equal(await page.evaluate(() => document.body.dataset.leak), undefined)
  await page.screenshot({ path: path.join(root, 'chat-visual-desktop.png'), fullPage: true })
  await page.setViewportSize({ width: 390, height: 900 })
  await page.screenshot({ path: path.join(root, 'chat-visual-mobile.png'), fullPage: true })
  assert(await page.evaluate(() => document.documentElement.scrollWidth <= 390))
  await page.reload(); await page.waitForFunction(() => typeof window.renderVisual === 'function')
  await child().getByText('Isolated display; no project APIs', { exact: true }).waitFor()
  assert.equal(await page.evaluate(() => localStorage.getItem('reply-source')), sample)
  await render(html('<script>throw new Error("fixture failure")</script>'))
  await page.locator('[data-chat-visual="source"]').waitFor()
  assert((await page.locator('[data-chat-visual="source"]').textContent()).includes('fixture failure'))
  await render('incomplete HTML')
  assert.equal(await page.locator('iframe').count(), 0)
  for (const operation of [
    `fetch(${JSON.stringify(origin + '/fetch-leak')}).catch(()=>{})`,
    `location.href=${JSON.stringify(origin + '/navigation-leak')}`,
    `const image=new Image();image.src=${JSON.stringify(origin + '/image-leak')};document.body.append(image)`,
    `const frame=document.createElement('iframe');frame.src=${JSON.stringify(origin + '/frame-leak')};document.body.append(frame)`,
  ]) {
    await render(html(`<h1>Network boundary fixture</h1><script>${operation}</script>`))
    await page.locator('[data-chat-visual="source"]').waitFor()
  }
  assert.deepEqual(network, [])
  const result = { scripts_and_local_interaction: true, parent_and_storage_isolated: true, no_project_api: true, fetch_image_frame_and_navigation_blocked: true, runtime_error_and_invalid_code_fallback: true, source_reloaded: true, desktop_and_mobile: true, unexpected_network: network }
  await writeFile(path.join(root, 'browser-result.json'), JSON.stringify(result, null, 2) + '\n')
  console.log(JSON.stringify(result))
} finally { await browser?.close(); await new Promise(resolve => server.close(resolve)) }
