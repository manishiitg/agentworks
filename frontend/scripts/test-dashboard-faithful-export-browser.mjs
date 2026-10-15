// Real JWT + workspace publication/media handlers, shared preview host,
// Chromium, the pinned HyperFrames engine and a decoded MP4 final frame.
import assert from 'node:assert/strict'
import { spawn, spawnSync } from 'node:child_process'
import { readFile, mkdir, writeFile, readdir } from 'node:fs/promises'
import path from 'node:path'
import { createServer } from 'vite'
import { chromium } from 'playwright'

if (!process.env.DASHBOARD_FIXTURE_URL) {
  const check = spawnSync('go', ['test', './cmd/server', '-run', '^TestDashboardFaithfulExportChromium$', '-count=1', '-v'], {
    cwd: path.resolve('../agent_go'), env: { ...process.env, DASHBOARD_EXPORT_BROWSER_CHECK: '1' }, stdio: 'inherit', timeout: 600000,
  })
  process.exit(check.status ?? 1)
}

const root = path.join(process.env.DASHBOARD_FIXTURE_ROOT, 'Workflow/invoices')
const revision = process.env.DASHBOARD_FIXTURE_REVISION
const token = process.env.SECRET_DASHBOARD_EXPORT_TOKEN
const backend = process.env.DASHBOARD_FIXTURE_URL
const fixture = { name: 'faithful-preview', configureServer(server) {
  server.middlewares.use(async (req, res, next) => {
    if (req.url === '/__playback') {
      res.setHeader('Content-Type', 'text/html')
      res.end('<!doctype html><video muted preload="auto"></video>')
      return
    }
    if (req.url.startsWith('/report-preview/report-preview.js')) {
      res.setHeader('Content-Type', 'text/javascript')
      res.end((await server.transformRequest('/src/report-preview/main.ts')).code)
      return
    }
    if (!req.url.startsWith('/report-preview/')) return next()
    res.setHeader('Content-Type', 'text/html')
    res.end(await server.transformIndexHtml('/report-preview/', '<!doctype html><html><body><script type="module" src="/src/report-preview/main.ts"></script></body></html>'))
  })
} }
const server = await createServer({ configFile: false, logLevel: 'silent', plugins: [fixture], optimizeDeps: { entries: [] }, server: { host: '127.0.0.1', port: 0, proxy: { '/api': backend } } })
let browser
const run = (program, args, options = {}) => new Promise((resolve, reject) => {
  const child = spawn(program, args, options)
  let stdout = '', stderr = ''
  child.stdout.on('data', value => { stdout += value })
  child.stderr.on('data', value => { stderr += value })
  child.on('error', reject)
  child.on('close', status => status === 0 ? resolve({ stdout, stderr }) : reject(new Error(`${program} failed (${status}): ${stderr}\n${stdout}`)))
})
try {
  await server.listen()
  const origin = server.resolvedUrls.local[0]
  for (const name of ['ffmpeg', 'ffprobe']) {
    const provision = spawnSync('python3', [path.resolve(`../deploy/rootless-linux/install-${name}.py`)], { env: { ...process.env, REMOTE_APP: root }, encoding: 'utf8', timeout: 180000 })
    assert.equal(provision.status, 0, provision.stderr)
  }
  const env = { ...process.env, PATH: path.join(root, 'tools/bin') + path.delimiter + process.env.PATH, AGENT_BROWSER_EXECUTABLE_PATH: chromium.executablePath() }
  const config = {
    source_mode: 'dashboard', dashboard_id: 'faithful', revision, document: `db/reports/managed/faithful/${revision}/index.html`,
    composition_path: `db/reports/managed/faithful/${revision}`, output_path: 'db/assets/motion/faithful-check', cache_path: '.sandbox-cache/dashboard-motion',
    caller: 'owner', renderer_version: '0.8.99', preview_origin: origin, workspace: 'Workflow/invoices', theme: 'dark', width: 320, height: 180, fps: 30,
  }
  const rendered = await run('python3', [path.resolve('../agent_go/pkg/dashboardmotion/render.py'), Buffer.from(JSON.stringify(config)).toString('base64')], { cwd: root, env })
  const result = JSON.parse(rendered.stdout.trim())
  const evidence = process.env.RUNNER_TEMP && path.join(process.env.RUNNER_TEMP, 'browser-check-artifacts')
  if (evidence) {
    await mkdir(evidence, { recursive: true })
    await writeFile(path.join(evidence, 'dashboard-faithful-export.json'), JSON.stringify(result, null, 2))
    await writeFile(path.join(evidence, 'dashboard-faithful-final-frame.png'), await readFile(path.join(root, result.final_frame_path)))
  }
  assert.equal(result.source_mode, 'dashboard')
  assert.equal(result.theme, 'dark')
  assert.equal(result.revision, revision)
  assert.equal(result.frames, 30)
  assert.equal(result.note, '')
  const snapshot = JSON.parse(await readFile(path.join(root, result.snapshot_path), 'utf8'))
  assert.equal(snapshot.data.length, 1)
  assert.equal(snapshot.data[0].method, 'get')
  assert.equal(snapshot.data[0].value.total, 96)
  const decoded = spawnSync(path.join(root, 'tools/bin/ffmpeg'), ['-v', 'error', '-i', path.join(root, result.path), '-vf', 'select=eq(n\\,29)', '-frames:v', '1', '-f', 'rawvideo', '-pix_fmt', 'rgb24', 'pipe:1'], { maxBuffer: 1024 * 1024 })
  assert.equal(decoded.status, 0, decoded.stderr.toString())
  const pixel = (x, y) => [...decoded.stdout.subarray((y * 320 + x) * 3, (y * 320 + x) * 3 + 3)]
  const close = (actual, expected) => actual.every((value, index) => Math.abs(value - expected[index]) < 22)
  assert.ok(close(pixel(305, 40), [20, 160, 220]), `final recorded value 96 missing: ${pixel(305, 40)}`)
  assert.ok(pixel(319, 160).every((value, index) => Math.abs(value - [10, 20, 30][index]) <= 5), `authored theme/height missing: ${pixel(319, 160)}`)
  const staticRevision = process.env.DASHBOARD_FIXTURE_STATIC_REVISION
  const staticConfig = { ...config, dashboard_id: 'still', revision: staticRevision, document: `db/reports/managed/still/${staticRevision}/index.html`, composition_path: `db/reports/managed/still/${staticRevision}`, output_path: 'db/assets/motion/still-check', duration_s: 0.4, fps: 5 }
  const still = await run('python3', [path.resolve('../agent_go/pkg/dashboardmotion/render.py'), Buffer.from(JSON.stringify(staticConfig)).toString('base64')], { cwd: root, env: { ...env, SECRET_DASHBOARD_EXPORT_TOKEN: process.env.SECRET_DASHBOARD_EXPORT_STATIC_TOKEN } })
  const stillResult = JSON.parse(still.stdout.trim())
  assert.equal(stillResult.success, true)
  assert.match(stillResult.note, /still video/)
  assert.equal(stillResult.frames, 2)
  const stillDecoded = spawnSync(path.join(root, 'tools/bin/ffmpeg'), ['-v', 'error', '-i', path.join(root, stillResult.path), '-f', 'framemd5', 'pipe:1'])
  assert.equal(stillDecoded.status, 0, stillDecoded.stderr.toString())
  const hashes = stillDecoded.stdout.toString().split('\n').filter(line => line && !line.startsWith('#')).map(line => line.split(',').at(-1).trim())
  assert.equal(hashes.length, 2)
  assert.equal(hashes[0], hashes[1], 'a no-clock export must remain a still video')

  const api = async (endpoint, body) => fetch(new URL(endpoint, backend), { method: 'POST', headers: { Authorization: `Bearer ${token}`, 'Content-Type': 'application/json' }, body: JSON.stringify(body) })
  const bad = await api('/api/workflow/report-preview/media-url', { workspace: 'Workflow/invoices', path: 'outputs/clip.mp4' })
  assert.equal(bad.status, 400)
  assert.match(await bad.text(), /path returned by Export/)
  const media = await api('/api/workflow/report-preview/media-url', { workspace: 'Workflow/invoices', path: result.path })
  assert.equal(media.status, 200)
  const link = (await media.json()).url
  const range = await fetch(new URL(link, backend), { headers: { Range: 'bytes=0-15' } })
  assert.equal(range.status, 206)
  assert.equal(range.headers.get('content-range'), `bytes 0-15/${result.bytes}`)
  assert.equal((await range.arrayBuffer()).byteLength, 16)
  // Stock Chrome supplies the H.264 decoder required for MP4 playback.
  browser = await chromium.launch({ headless: true, channel: 'chrome' })
  const page = await browser.newPage()
  await page.goto(new URL('/__playback', origin).href)
  assert.ok(await page.evaluate(() => document.querySelector('video').canPlayType('video/mp4; codecs="avc1.42E01E"')), 'the playback browser needs an H.264 decoder')
  await page.evaluate(url => { document.querySelector('video').src = url }, new URL(link, origin).href)
  await page.waitForFunction(() => document.querySelector('video').readyState >= 2 || document.querySelector('video').error)
  assert.equal(await page.evaluate(() => document.querySelector('video').error?.message || null), null, 'the exported video must actually decode in the browser')
  const duration = await page.evaluate(() => document.querySelector('video').duration)
  assert.ok(duration >= 0.99 && duration <= 1.01)
  await page.evaluate(() => document.querySelector('video').play())
  await page.waitForFunction(() => document.querySelector('video').currentTime > 0.15)
  await page.evaluate(() => { const video = document.querySelector('video'); video.pause(); video.currentTime = 0.8 })
  await page.waitForFunction(() => !document.querySelector('video').seeking && document.querySelector('video').currentTime >= 0.79)
  const wrong = await fetch(new URL('/api/workflow/report-preview/file?workspace=Workflow%2Finvoices&path=db%2Freports%2Fmanaged%2Fstill%2F' + staticRevision + '%2Findex.html', backend), { headers: { Authorization: `Bearer ${token}` } })
  assert.equal(wrong.status, 403)
  async function checkNoCredential(dir) {
    for (const entry of await readdir(dir, { withFileTypes: true })) {
      const file = path.join(dir, entry.name)
      if (entry.isDirectory()) await checkNoCredential(file)
      else assert.ok(!(await readFile(file)).includes(Buffer.from(token)), 'credential was saved in export source')
    }
  }
  await checkNoCredential(path.join(root, 'db/assets/motion/faithful-check'))
  console.log(JSON.stringify({ passed: true, frames: result.frames, duration, snapshot_total: 96, final_pixel: pixel(305, 40), theme_pixel: pixel(319, 160) }))
} finally {
  await browser?.close()
  await server.close()
}
