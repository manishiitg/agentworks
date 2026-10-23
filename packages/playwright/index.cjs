const { test: base, expect } = require('@playwright/test')
const WebSocket = require('ws')


/** Attach a user-owned Chromium context. Closing the stream never closes it. */
async function attachLiveBrowser(context, options = {}) {
  const apiURL = options.apiURL ?? process.env.MCP_API_URL
  const token = options.token ?? process.env.MCP_API_TOKEN
  if (!apiURL || !token) throw new Error('Live view needs MCP_API_URL and MCP_API_TOKEN from an AgentWorks workflow session.')
  const url = new URL(apiURL)
  if (!['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash) {
    throw new Error('Live view requires an HTTP(S) session API URL without embedded credentials or query parameters.')
  }
  url.pathname = url.pathname.replace(/\/$/, '')
  if (!/\/s\/[^/]+$/.test(url.pathname)) {
    const session = options.sessionID ?? process.env.MCP_SESSION_ID
    if (!session) throw new Error('Live view requires a session-scoped MCP_API_URL or MCP_SESSION_ID.')
    url.pathname += `/s/${encodeURIComponent(session)}`
  }
  url.pathname += '/tools/browser/live'
  url.protocol = url.protocol === 'https:' ? 'wss:' : 'ws:'
  url.searchParams.set('label', (options.label || 'Playwright test').slice(0, 200))
  const socket = new WebSocket(url, { headers: { Authorization: `Bearer ${token}` }, handshakeTimeout: 10000, maxPayload: 16384 })
  const sessionID = await new Promise((resolve, reject) => {
    const timer = setTimeout(() => { socket.terminate(); reject(new Error('Live view registration timed out.')) }, 10000)
    const fail = () => { clearTimeout(timer); reject(new Error('Live view registration failed. Check the workflow session and server connection.')) }
    socket.on('error', fail)
    socket.once('close', fail)
    socket.once('message', raw => {
      try {
        const message = JSON.parse(raw.toString())
        if (message.type !== 'registered' || typeof message.browser_session !== 'string') throw new Error('Invalid registration')
        clearTimeout(timer); resolve(message.browser_session)
      } catch { socket.terminate(); fail() }
    })
  })
  let stopped = false
  let sequence = 0
  let active
  let warning = ''
  let lastFrame = 0
  let frameTimer
  const pages = new Map()
  const pending = new Set()
  const send = message => {
    if (!stopped && socket.readyState === WebSocket.OPEN && socket.bufferedAmount < 2 * 1024 * 1024) socket.send(JSON.stringify(message))
  }
  const tabs = () => send({ type: 'tabs', tabs: [...pages].map(([page, item]) => ({ tabId: item.id, title: item.title, url: page.url().slice(0, 2048), active: page === active })) })
  const flushFrame = () => {
    clearTimeout(frameTimer)
    frameTimer = null
    const frame = pages.get(active)?.frame
    if (stopped || !frame) return
    const delay = 250 - (Date.now() - lastFrame)
    if (delay > 0) { frameTimer = setTimeout(flushFrame, delay); return }
    lastFrame = Date.now()
    send({ type: 'frame', data: frame.data, metadata: { deviceWidth: Math.round(frame.metadata.deviceWidth), deviceHeight: Math.round(frame.metadata.deviceHeight) } })
  }
  const attachPage = async page => {
    if (stopped || pages.has(page) || page.isClosed()) return
    const item = { id: `t${++sequence}`, title: 'Test page', cdp: null, frame: null }
    pages.set(page, item)
    active = page
    item.navigate = async frame => {
      if (frame !== page.mainFrame()) return
      item.title = (await page.title().catch(() => 'Test page')).slice(0, 500)
      tabs()
    }
    item.close = () => {
      pages.delete(page)
      if (active === page) {
        active = [...pages.keys()].at(-1)
        flushFrame()
      }
      tabs()
    }
    page.on('framenavigated', item.navigate)
    page.once('close', item.close)
    try {
      item.cdp = await context.newCDPSession(page)
      if (stopped || page.isClosed()) { await item.cdp.detach().catch(() => {}); return }
      item.cdp.on('Page.screencastFrame', frame => {
        // Ack even dropped frames; never let a slow viewer stall Chromium.
        void item.cdp.send('Page.screencastFrameAck', { sessionId: frame.sessionId }).catch(() => {})
        // Keep the latest frame during throttling: a static page may never emit
        // another frame, so dropping it can leave the panel blank or outdated.
        item.frame = frame
        if (page === active && !frameTimer) flushFrame()
      })
      await item.cdp.send('Page.startScreencast', { format: 'jpeg', quality: 60, maxWidth: 1280, maxHeight: 720, everyNthFrame: 1 })
      await item.navigate(page.mainFrame())
    } catch (error) {
      if (!page.isClosed() && !stopped) throw error
    }
  }
  const onPage = page => {
    const task = attachPage(page).catch(() => { warning = 'Could not stream a Chromium page.' }).finally(() => pending.delete(task))
    pending.add(task)
  }
  const heartbeat = setInterval(() => send({ type: 'ping' }), 10000)
  heartbeat.unref()
  const stop = async () => {
    if (stopped) return
    stopped = true
    clearInterval(heartbeat)
    clearTimeout(frameTimer)
    socket.close()
    const timeout = setTimeout(() => socket.terminate(), 1000)
    timeout.unref()
    socket.once('close', () => clearTimeout(timeout))
    context.off('page', onPage)
    context.off('close', onClose)
    for (const [page, item] of pages) {
      page.off('framenavigated', item.navigate)
      page.off('close', item.close)
    }
    await Promise.allSettled([...pending])
    await Promise.allSettled([...pages.values()].map(async item => {
      if (item.cdp) { await item.cdp.send('Page.stopScreencast').catch(() => {}); await item.cdp.detach().catch(() => {}) }
    }))
    pages.clear()
  }
  const onClose = () => { void stop() }
  socket.on('close', () => { if (!stopped) { warning = 'Live view disconnected; the test continued.'; void stop() } })
  context.on('page', onPage)
  context.once('close', onClose)
  try { for (const page of context.pages()) await attachPage(page) }
  catch { await stop(); throw new Error('Live view could not attach to Chromium. Use a Chromium context.') }
  return { sessionID, stop, get warning() { return warning } }
}

const test = base.extend({
  video: ['on', { option: true, scope: 'worker' }],
  _agentworksLive: [async ({ context, browserName }, use, testInfo) => {
    if (!process.env.MCP_API_URL && !process.env.MCP_API_TOKEN) {
      testInfo.annotations.push({ type: 'live-view', description: 'Not connected to an AgentWorks workflow.' })
      await use(); return
    }
    if (browserName !== 'chromium') {
      testInfo.annotations.push({ type: 'live-view', description: 'Live view currently supports Chromium only.' })
      await use(); return
    }
    const live = await attachLiveBrowser(context, { label: `${testInfo.project.name || 'Chromium'} · ${testInfo.titlePath.join(' › ')} · retry ${testInfo.retry}` })
    testInfo.annotations.push({ type: 'live-browser', description: live.sessionID })
    try { await use() }
    finally {
      await live.stop()
      if (live.warning) testInfo.annotations.push({ type: 'live-view-warning', description: live.warning })
    }
  }, { auto: true }],
})
// Playwright owns the recording lifecycle and attaches videos to its HTML report.
// A caller can override this with test.use({ video: 'off' / 'retain-on-failure' }).


module.exports = { test, expect, attachLiveBrowser }
