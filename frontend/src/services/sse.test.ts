// @vitest-environment node
import { createServer } from 'node:http'
import { once } from 'node:events'
import { expect, it, vi } from 'vitest'

const endpoint = vi.hoisted(() => ({ baseUrl: '', token: 'test-sign-in' }))
vi.mock('./api', () => ({ getApiBaseUrl: () => endpoint.baseUrl, getAuthToken: () => endpoint.token }))
vi.mock('../utils/logger', () => ({ logger: { debug() {}, warn() {}, error() {} } }))

it('browser chat streams authenticate by header and resume complete frame cursors over real HTTP', async () => {
  const requests: { url: string; authorization?: string }[] = []
  const server = createServer((req, res) => {
    requests.push({ url: req.url!, authorization: req.headers.authorization })
    res.writeHead(200, { 'Content-Type': 'text/event-stream' })
    if (requests.length === 1) {
      // Comment heartbeats, a split UTF-8 character, multiline JSON and a
      // cursor-only frame must survive the switch away from EventSource.
      const frames = Buffer.from(': heartbeat\r\nevent: event\r\nid: 8\r\ndata: {"events":[],\r\ndata: "label":"é"}\r\n\r\nevent: cursor\nid: 9\ndata: {}\n\nevent: event\nid: 999\ndata: {"events":[]}')
      const split = frames.indexOf(Buffer.from('é')) + 1
      res.write(frames.subarray(0, split))
      endpoint.token = 'refreshed-sign-in'
      res.end(frames.subarray(split)) // Final incomplete frame must not advance the cursor.
    } else {
      res.write('event: status\ndata: {"session_status":"completed"}\n\n')
    }
  })
  server.listen(0, '127.0.0.1')
  await once(server, 'listening')
  endpoint.baseUrl = `http://127.0.0.1:${(server.address() as { port: number }).port}`
  const { SSEConnection } = await import('./sse')
  const onMessage = vi.fn()
  let finish!: () => void
  const completed = new Promise<void>(resolve => { finish = resolve })
  const connection = new SSEConnection('session-test', 7, {
    onMessage,
    onStatusUpdate: () => { connection.close(); finish() },
  })
  try {
    await Promise.race([completed, new Promise((_, reject) => {
      const timer = setTimeout(() => reject(new Error('stream did not reconnect')), 5000)
      timer.unref()
    })])
    expect(requests).toEqual([
      { url: '/api/sessions/session-test/events/stream?working_set=session&durable_chat=1&since=7', authorization: 'Bearer test-sign-in' },
      { url: '/api/sessions/session-test/events/stream?working_set=session&durable_chat=1&since=9', authorization: 'Bearer refreshed-sign-in' },
    ])
    expect(onMessage).toHaveBeenCalledExactlyOnceWith({ events: [], label: 'é' })
    expect(connection.lastIndex).toBe(9)
    expect(connection.isClosed).toBe(true)
  } finally {
    connection.close()
    server.closeAllConnections()
    await new Promise<void>(resolve => server.close(() => resolve()))
  }
})
