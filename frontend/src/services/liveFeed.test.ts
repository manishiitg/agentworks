// @vitest-environment happy-dom
import { afterEach, beforeEach, expect, it, vi } from 'vitest'

vi.mock('./api', () => ({ getApiBaseUrl: () => 'http://host', getAuthToken: () => 'tok' }))

class FakeEventSource {
  static CONNECTING = 0
  static OPEN = 1
  static CLOSED = 2
  static instances: FakeEventSource[] = []
  readyState = FakeEventSource.OPEN
  onerror: (() => void) | null = null
  private handlers = new Map<string, ((event: MessageEvent<string>) => void)[]>()
  url: string
  constructor(url: string) {
    this.url = url
    FakeEventSource.instances.push(this)
  }
  addEventListener(type: string, handler: (event: MessageEvent<string>) => void) {
    this.handlers.set(type, [...(this.handlers.get(type) ?? []), handler])
  }
  emit(type: string, data: unknown) {
    for (const handler of this.handlers.get(type) ?? []) handler({ data: JSON.stringify(data) } as MessageEvent<string>)
  }
  close() { this.readyState = FakeEventSource.CLOSED }
}

beforeEach(() => {
  FakeEventSource.instances = []
  vi.stubGlobal('EventSource', FakeEventSource)
  vi.resetModules()
})
afterEach(() => vi.unstubAllGlobals())

it('opens one stream with the token, ignores the first resync, and routes notices by kind and workflow', async () => {
  const { liveFeed } = await import('./liveFeed')
  const report = vi.fn()
  const plan = vi.fn()
  const legacyCrewPlan = vi.fn()
  const header = vi.fn()
  const offReport = liveFeed.subscribe(['report'], 'Workflow/trader/db/reports', report)
  const offPlan = liveFeed.subscribe(['plan'], 'Workflow/trader/planning', plan)
  const offLegacyCrewPlan = liveFeed.subscribe(['plan'], '_users/alice/Chats/Work/projects/team', legacyCrewPlan)
  const offHeader = liveFeed.subscribe(['sessions', 'schedules'], null, header)

  expect(FakeEventSource.instances).toHaveLength(1)
  const source = FakeEventSource.instances[0]
  expect(source.url).toBe('http://host/api/live?token=tok')

  source.emit('resync', {})
  expect(liveFeed.getStatus()).toBe('live')
  expect(report).not.toHaveBeenCalled()
  expect(header).not.toHaveBeenCalled()

  source.emit('change', { kind: 'report', workflow: 'Workflow/other' })
  source.emit('change', { kind: 'plan', workflow: 'Workflow/other' })
  expect(plan).not.toHaveBeenCalled()
  source.emit('change', { kind: 'plan', workflow: 'Workflow/trader' })
  expect(plan).toHaveBeenCalledTimes(1)
  expect(legacyCrewPlan).not.toHaveBeenCalled()
  source.emit('change', { kind: 'plan' })
  expect(legacyCrewPlan).toHaveBeenCalledTimes(1)
  source.emit('change', { kind: 'human_inputs', workflow: 'Workflow/trader' })
  expect(report).not.toHaveBeenCalled()
  source.emit('change', { kind: 'report', workflow: 'Workflow/trader' })
  source.emit('change', { kind: 'schedules' })
  expect(report).toHaveBeenCalledTimes(1)
  expect(header).toHaveBeenCalledTimes(1)

  // A reconnect's resync may have missed notices: everyone refetches.
  source.emit('resync', {})
  expect(report).toHaveBeenCalledTimes(2)
  expect(header).toHaveBeenCalledTimes(2)

  offReport()
  offPlan()
  offLegacyCrewPlan()
  offHeader()
  expect(source.readyState).toBe(FakeEventSource.CLOSED)
  expect(liveFeed.getStatus()).toBe('offline')
})

it('reports offline after a refused connection so panels fall back to polling', async () => {
  vi.useFakeTimers()
  try {
    const { liveFeed } = await import('./liveFeed')
    const off = liveFeed.subscribe(['sessions'], null, () => {})
    const source = FakeEventSource.instances[0]
    source.readyState = FakeEventSource.CLOSED
    source.onerror?.()
    expect(liveFeed.getStatus()).toBe('offline')
    vi.advanceTimersByTime(60_000)
    expect(FakeEventSource.instances).toHaveLength(2)
    off()
  } finally {
    vi.useRealTimers()
  }
})
