// Adapter for the code-pinned HyperFrames engine. Credentials stay in memory;
// source bundles contain only authored files and semantic data snapshots.
import fs from 'node:fs'
import path from 'node:path'
import { pathToFileURL } from 'node:url'

const config = JSON.parse(fs.readFileSync(process.argv[2], 'utf8'))
const binary = process.env.PATH.split(path.delimiter).map(dir => path.join(dir, 'hyperframes')).find(file => fs.existsSync(file))
if (!binary) throw new Error('The pinned animation renderer is unavailable.')
const pkg = path.dirname(path.dirname(fs.realpathSync(binary)))
if (JSON.parse(fs.readFileSync(path.join(pkg, 'package.json'), 'utf8')).version !== config.renderer_version) throw new Error('The animation renderer version does not match this export.')
// Internal filenames are part of the reviewed 0.8.99 adapter, not floating npm APIs.
const engine = await import(pathToFileURL(path.join(pkg, 'dist/chunk-VW5OMJNG.js')).href)
const producer = await import(pathToFileURL(path.join(pkg, 'dist/chunk-JXXE3SWC.js')).href)
const fps = config.fps || 30
let duration = config.duration_s || 1
const framesDir = path.join(config.output, '.frames')
fs.mkdirSync(framesDir, { recursive: true })
let previewFrame, snapshot, initialized = false, motionCount = 0
const token = process.env.SECRET_DASHBOARD_EXPORT_TOKEN
const origin = config.preview_origin ? new URL(config.preview_origin).origin : null

function renderError(detail) {
  let message = String(detail).split('\n')[0]
  if (token) message = message.split(token).join('[credential omitted]')
  message = message.replace(/https?:\/\/[^\s)]+/g, value => {
    try { return new URL(value).pathname } catch { return '[request path]' }
  }).replace(/\b(?:authorization|cookie|headers)\s*[:=].*/gi, '[request details omitted]')
    .replace(/\bBearer\s+\S+/gi, '[credential omitted]')
  return new Error('The dashboard animation could not render. ' + message.slice(0, 400))
}

async function prepareDashboard(page) {
  if (initialized) return
  initialized = true
  await page.setRequestInterception(true)
  page.on('request', request => {
    const url = new URL(request.url())
    const headers = { ...request.headers() }
    if (url.origin === origin && url.pathname.startsWith('/api/workflow/report-preview/')) {
      headers.authorization = `Bearer ${token}`
    } else if (headers.authorization === `Bearer ${token}`) {
      delete headers.authorization
    }
    void request.continue({ headers }).catch(() => {})
  })
  const url = new URL('/report-preview/', origin)
  url.search = new URLSearchParams({ workspace: config.workspace, document: config.document, theme: config.theme, width: String(config.width), height: String(config.height), export: '1', token: 'export-header' }).toString()
  await page.evaluate(value => { document.querySelector('iframe').src = value }, url.href)
  const element = await page.$('iframe')
  previewFrame = await element.contentFrame()
  await previewFrame.waitForFunction(() => ['ready', 'error', 'missing', 'failed'].includes(window.__reportPreview?.getState().previewState), { timeout: 30000 })
  const captured = await previewFrame.evaluate(async () => {
    if (window.__reportPreview.getState().previewState !== 'ready') throw new Error('The dashboard could not load. Check its preview before exporting again.')
    await document.fonts.ready
    return { data: await window.__reportPreview.freeze(), motion: window.__reportPreview.motion(), state: window.__reportPreview.getState(), renderer: document.querySelector('iframe')?.contentDocument?.documentElement.dataset.reportRenderer }
  })
  motionCount = captured.motion.length
  if (!config.duration_s && motionCount) duration = Math.max(...captured.motion.map(player => player.duration))
  if (!Number.isFinite(duration) || duration <= 0 || duration > 600) throw new Error('Choose an export duration up to 10 minutes.')
  snapshot = {
    as_of: new Date().toISOString(),
    source: { dashboard_id: config.dashboard_id, revision: config.revision, theme: config.theme, width: config.width, height: config.height },
    data: captured.data,
  }
  fs.writeFileSync(path.join(config.project, 'snapshot.json'), JSON.stringify(snapshot, null, 2))
  // Preserve the runtime used for the capture beside its authored source.
  const runtime = await fetch(new URL('/report-preview/report-preview.js', origin))
  if (!runtime.ok) throw new Error('The dashboard rendering runtime is unavailable.')
  fs.mkdirSync(path.join(config.project, '.runtime'), { recursive: true })
  fs.writeFileSync(path.join(config.project, '.runtime/report-preview.js'), await runtime.text())
  if (captured.renderer === 'json-render') {
    for (const name of ['dashboard-json.js', 'dashboard-json.css']) {
      const asset = await fetch(new URL('/report-preview/' + name, origin))
      if (!asset.ok) throw new Error('The JSON dashboard rendering assets are unavailable.')
      fs.writeFileSync(path.join(config.project, '.runtime', name), Buffer.from(await asset.arrayBuffer()))
    }
  }
}

async function beforeCapture(page, time) {
  if (config.source_mode === 'dashboard') {
    await prepareDashboard(page)
    const frames = Math.max(1, Math.ceil(duration * fps))
    const last = (frames - 1) / fps
    const error = await previewFrame.evaluate(seconds => {
      window.__reportPreview.seek(seconds)
      const state = window.__reportPreview.getState()
      if (state.previewState !== 'ready' || state.report.state === 'error') return state.report.errors[0] || 'Check its preview before exporting again.'
      return null
    }, time >= last - 1e-7 ? duration : time)
    if (error) throw renderError(error)
  }
}

async function finishComposition(page) {
  // The engine keeps clips visible at the final sample (< duration). Seek only
  // their animation timelines to the terminal state before capturing it.
  await page.evaluate(seconds => {
    const finish = win => {
      for (const timeline of Object.values(win.__timelines || {})) {
        timeline.pause?.()
        if (typeof timeline.seek === 'function') timeline.seek(typeof timeline.duration === 'function' ? timeline.duration() : seconds, false)
      }
      for (const animation of win.document.getAnimations()) {
        const end = animation.effect?.getComputedTiming().endTime
        if (Number.isFinite(end)) { animation.pause(); animation.currentTime = end }
      }
      for (const frame of win.document.querySelectorAll('iframe')) {
        try { finish(frame.contentWindow) } catch { /* Cross-origin media has its own renderer. */ }
      }
    }
    finish(window)
  }, duration)
}

let server, session
try {
  server = await producer.createFileServer({ projectDir: config.capture_project || config.project, fps: { num: fps, den: 1 } })
  session = await engine.createCaptureSession(server.url, framesDir, {
    width: config.width, height: config.height, fps: { num: fps, den: 1 }, format: 'png', deviceScaleFactor: 1,
  }, beforeCapture, {
    forceScreenshot: true, enableBrowserPool: false,
    chromePath: process.env.HYPERFRAMES_BROWSER_PATH || process.env.AGENT_BROWSER_EXECUTABLE_PATH,
  })
  await engine.initializeSession(session)
  if (config.source_mode === 'dashboard') {
    await prepareDashboard(session.page)
    const count = Math.max(1, Math.ceil(duration * fps))
    // The published host owns its clock. Seeking the composition shell also
    // dispatches into nested frames, interfering with that independent host.
    // Reuse the pinned engine's browser after initialization, then seek only
    // the dashboard and capture its viewport directly.
    const capture = async frame => {
      await beforeCapture(session.page, frame / fps)
      const file = path.join(framesDir, `frame_${String(frame).padStart(6, '0')}.png`)
      await session.page.screenshot({ path: file, type: 'png', clip: { x: 0, y: 0, width: config.width, height: config.height } })
      return { path: file }
    }
    const first = await capture(0)
    for (let frame = 1; frame < count; frame++) {
      if (motionCount) await capture(frame)
      else fs.copyFileSync(first.path, path.join(framesDir, `frame_${String(frame).padStart(6, '0')}.png`))
    }
    fs.writeFileSync(path.join(config.output, 'capture.json'), JSON.stringify({
      duration_s: duration, frames: count, fps, snapshot,
      note: motionCount ? '' : 'This dashboard has no seekable animation clock. Its settled page was exported as a still video.',
    }))
  } else {
    const last = Math.max(0, (config.frames - 1) / fps)
    // Let the engine finish its normal seek (including asynchronous adapters)
    // before changing authored timelines. Its pending seek otherwise restores
    // the pre-terminal sample after the before-capture hook.
    await engine.captureFrame(session, 0, last)
    await finishComposition(session.page)
    await session.page.screenshot({ path: path.join(config.output, 'final-frame.png'), type: 'png', clip: { x: 0, y: 0, width: config.width, height: config.height } })
  }
} finally {
  if (session) await engine.closeCaptureSession(session)
  if (server) await server.close()
}
