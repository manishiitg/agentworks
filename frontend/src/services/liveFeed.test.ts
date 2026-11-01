// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

vi.mock('./api', () => ({ getApiBaseUrl: () => 'http://host', getAuthToken: () => 'tok' }))

class FakeStream {
  static instances: FakeStream[] = []
  readonly controller: ReadableStreamDefaultController<Uint8Array>
  readonly body: ReadableStream<Uint8Array>
  readonly url: string
  readonly init: RequestInit
  closed = false
  constructor(url: string, init: RequestInit) {
    this.url = url
    this.init = init
    let controller!: ReadableStreamDefaultController<Uint8Array>
    this.body = new ReadableStream({ start(c) { controller = c }, cancel: () => { this.closed = true } })
    this.controller = controller
    FakeStream.instances.push(this)
  }
  emit(type: string, data: unknown) {
    this.controller.enqueue(new TextEncoder().encode(`event: ${type}\r\ndata: ${JSON.stringify(data)}\r\n\r\n`))
  }
}

beforeEach(() => {
  FakeStream.instances = []
  vi.stubGlobal('fetch', vi.fn(async (url: string, init: RequestInit) => {
    const stream = new FakeStream(url, init)
    return { ok: true, body: stream.body }
  }))
  vi.resetModules()
})
afterEach(() => vi.unstubAllGlobals())

it('opens one stream with header authentication, refetches on the first resync, and routes notices by kind and workflow', async () => {
  const { liveFeed } = await import('./liveFeed')
  const report = vi.fn()
  const plan = vi.fn()
  const legacyCrewPlan = vi.fn()
  const header = vi.fn()
  const offReport = liveFeed.subscribe(['report'], 'Workflow/trader/db/reports', report)
  const offPlan = liveFeed.subscribe(['plan'], 'Workflow/trader/planning', plan)
  const offLegacyCrewPlan = liveFeed.subscribe(['plan'], '_users/alice/Chats/Work/projects/team', legacyCrewPlan)
  const offHeader = liveFeed.subscribe(['sessions', 'schedules'], null, header)

  expect(FakeStream.instances).toHaveLength(1)
  const source = FakeStream.instances[0]
  expect(source.url).toBe('http://host/api/live')
  expect(source.init.headers).toMatchObject({ Authorization: 'Bearer tok' })

  source.emit('resync', {})
  await vi.waitFor(() => expect(liveFeed.getStatus()).toBe('live'))
  expect(liveFeed.getStatus()).toBe('live')
  expect(report).toHaveBeenCalledTimes(1)
  expect(header).toHaveBeenCalledTimes(1)
  report.mockClear(); header.mockClear(); plan.mockClear(); legacyCrewPlan.mockClear()

  source.emit('change', { kind: 'report', workflow: 'Workflow/other' })
  source.emit('change', { kind: 'plan', workflow: 'Workflow/other' })
  expect(plan).not.toHaveBeenCalled()
  source.emit('change', { kind: 'plan', workflow: 'Workflow/trader' })
  await vi.waitFor(() => expect(plan).toHaveBeenCalledTimes(1))
  expect(legacyCrewPlan).not.toHaveBeenCalled()
  source.emit('change', { kind: 'plan' })
  await vi.waitFor(() => expect(legacyCrewPlan).toHaveBeenCalledTimes(1))
  source.emit('change', { kind: 'human_inputs', workflow: 'Workflow/trader' })
  expect(report).not.toHaveBeenCalled()
  source.emit('change', { kind: 'report', workflow: 'Workflow/trader' })
  source.emit('change', { kind: 'schedules' })
  await vi.waitFor(() => expect(report).toHaveBeenCalledTimes(1))
  expect(header).toHaveBeenCalledTimes(1)

  // A reconnect's resync may have missed notices: everyone refetches.
  source.emit('resync', {})
  await vi.waitFor(() => expect(report).toHaveBeenCalledTimes(2))
  expect(header).toHaveBeenCalledTimes(2)

  offReport()
  offPlan()
  offLegacyCrewPlan()
  offHeader()
  expect(source.init.signal?.aborted).toBe(true)
  expect(liveFeed.getStatus()).toBe('offline')
})

it('falls back after failed connects, retries slowly, and cancels on last unsubscribe', async () => {
  vi.useFakeTimers()
  try {
    vi.mocked(fetch).mockResolvedValue({ ok: false, status: 401 } as Response)
    const { liveFeed } = await import('./liveFeed')
    const off = liveFeed.subscribe(['sessions'], null, () => {})
    await vi.advanceTimersByTimeAsync(2000)
    expect(fetch).toHaveBeenCalledTimes(3)
    expect(liveFeed.getStatus()).toBe('offline')
    await vi.advanceTimersByTimeAsync(60_000)
    expect(fetch).toHaveBeenCalledTimes(4)
    off()
    await vi.advanceTimersByTimeAsync(60_000)
    expect(fetch).toHaveBeenCalledTimes(4)
  } finally {
    vi.useRealTimers()
  }
})
